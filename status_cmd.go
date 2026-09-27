package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/meimolihan/fan-image-tr/internal/config"
	"github.com/meimolihan/fan-image-tr/internal/version"
)

// runStatus 查看本机服务运行状态。
func runStatus(args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("加载配置失败: %w", err)
	}
	port := cfg.App.Port
	debug := cfg.App.Debug
	if flags, err := parseFlags(args); err == nil {
		if flags.port > 0 {
			port = flags.port
		}
		if flags.debug {
			debug = true
		}
	}
	if env := os.Getenv("FIT_APP_PORT"); env != "" {
		if n, err := strconv.Atoi(env); err == nil {
			port = n
		}
	}

	fmt.Println("fan-image-tr", version.Long())
	fmt.Println("  版本      :", version.Current())
	fmt.Println("  环境      :", cfg.App.Env)
	fmt.Println("  监听端口  :", port)
	fmt.Println("  数据目录  :", cfg.App.DataDir)
	fmt.Println("  浏览根目录:", cfg.HomeDir())
	fmt.Println("  输出目录  :", cfg.OutputDir())
	fmt.Println("  上传目录  :", cfg.UploadDir())
	fmt.Println("  转换并发  :", cfg.App.Worker)
	fmt.Println("  允许上传  :", yesNo(cfg.App.AllowUpload))

	// 尝试请求本机健康检查接口
	addr := fmt.Sprintf("http://127.0.0.1:%d/api/health", port)
	client := &http.Client{Timeout: 3 * time.Second}
	if resp, err := client.Get(addr); err == nil {
		defer resp.Body.Close()
		var body map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&body)
		fmt.Println("  运行状态  : 运行中（健康检查通过）")
		if st := body["status"]; st != nil {
			fmt.Println("  接口返回  :", st)
		}
	} else {
		fmt.Println("  运行状态  : 未运行（无法连接", addr, "）")
	}

	// 输出常用目录占用情况，便于判断磁盘空间
	printDirSize("任务快照", tasksFile(cfg))
	printDirSize("转换产物", cfg.OutputDir())
	printDirSize("上传文件", cfg.UploadDir())

	if debug {
		fmt.Println("\n  当前配置（FIT_ 环境变量可覆盖）:")
		fmt.Printf("    FIT_APP_PORT=%d\n", port)
		fmt.Printf("    FIT_APP_DATA_DIR=%s\n", cfg.App.DataDir)
		fmt.Printf("    FIT_APP_MEDIA_DIR=%s\n", cfg.HomeDir())
		fmt.Printf("    FIT_APP_OUTPUT_DIR=%s\n", cfg.OutputDir())
		fmt.Printf("    FIT_APP_WORKER=%d\n", cfg.App.Worker)
		fmt.Printf("    FIT_FFMPEG_PATH=%s\n", cfg.FFmpegBin())
	}
	return nil
}

// tasksFile 返回任务快照文件路径。
func tasksFile(cfg *config.Config) string {
	return filepath.Join(cfg.App.DataDir, "tasks.json")
}

// printDirSize 打印目录或文件体积。
func printDirSize(label, path string) {
	var total int64
	var count int
	err := filepath.Walk(path, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			return nil
		}
		total += info.Size()
		count++
		return nil
	})
	if err != nil {
		fmt.Printf("  %s: 读取失败（%s）\n", label, path)
		return
	}
	if count == 0 {
		fmt.Printf("  %s: 暂无内容（%s）\n", label, path)
		return
	}
	fmt.Printf("  %s: %d 个文件 / %s（%s）\n", label, count, humanBytes(total), path)
}

// humanBytes 人类可读的体积。
func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit && exp < 4; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTP"[exp])
}
