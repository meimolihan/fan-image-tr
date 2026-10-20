package main

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/meimolihan/fan-image-tr/internal/config"
	"github.com/meimolihan/fan-image-tr/internal/version"
)

const defaultBinPath = "/var/lib/fan-image-tr/fan-image-tr"
const defaultDataDir = "/var/lib/fan-image-tr/data"
const defaultRecordFile = "/etc/fan-image-tr.conf"
const defaultServiceFile = "/etc/systemd/system/fan-image-tr.service"
const defaultPidFile = "/var/run/fan-image-tr.pid"

// defaultPort 返回默认监听端口。
func defaultPort() int { return 8791 }

// readRecord 解析安装记录文件（install.sh 生成），返回 KEY=VALUE 映射。
func readRecord() map[string]string {
	m := map[string]string{}
	data, err := os.ReadFile(defaultRecordFile)
	if err != nil {
		return m
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		m[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}
	return m
}

// floatNull 空值回退为 fallback。
func floatNull(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

// dirOf 返回路径所在目录。
func dirOf(p string) string { return filepath.Dir(p) }

// runStatus 输出运行状态。
func runStatus() error {
	rec := readRecord()
	banner("服务状态")
	sepPrint()

	binPath := rec["BIN_PATH"]
	if binPath == "" {
		binPath = defaultBinPath
	}
	instDir := rec["INSTALL_DIR"]

	// 端口优先级：安装记录 > 配置文件 > 默认值
	port := defaultPort()
	if v, err := strconv.Atoi(rec["PORT"]); err == nil && v > 0 {
		port = v
	}
	cfg, err := config.Load()
	if err == nil && cfg.App.Port > 0 && rec["PORT"] == "" {
		port = cfg.App.Port
	}
	dataDir := defaultDataDir
	if err == nil && cfg.App.DataDir != "" {
		dataDir = cfg.App.DataDir
	}
	mediaDir := floatNull(rec["MEDIA_DIR"], "")
	if err == nil && mediaDir == "" {
		mediaDir = cfg.App.MediaDir
	}

	// install 记录信息
	sectionPrint("安装信息")
	kvPrint("版本", version.Current())
	kvPrint("安装记录", defaultRecordFile)
	if v := rec["PORT"]; v != "" {
		kvPrint("记录端口", v)
	}
	if v := rec["DATA_DIR"]; v != "" {
		kvPrint("记录数据目录", v)
	}
	if v := rec["MEDIA_DIR"]; v != "" {
		kvPrint("记录图片目录", v)
	}

	// 运行方式与进程
	mode, pid := findProcess(binPath, port)
	sectionPrint("服务方式")
	if pid <= 0 {
		fmt.Println("  " + paint("[错误]", clrRed) + " fan-image-tr 服务未运行")
		sepPrint()
		return nil
	}
	kvPrint("运行方式", mode)
	kvPrint("服务状态", isSystemdActive())

	sectionPrint("进程信息")
	kvPrint("进程 PID", fmt.Sprintf("%d", pid))
	if threads := readProcField(pid, "Threads"); threads != "" {
		kvPrint("线程数量", threads)
	}

	sectionPrint("网络")
	lp := listenPort(pid, port)
	if lp > 0 {
		kvPrint("监听端口", fmt.Sprintf("%d", lp))
		printAccess(lp)
	} else {
		kvPrint("监听端口", "未找到")
		printAccess(port)
	}

	sectionPrint("运行时间")
	kvPrint("已运行", formatUptime(procUptimeSec(pid)))

	sectionPrint("内存")
	if v := readProcField(pid, "VmSize"); v != "" {
		kvPrint("虚拟内存", formatMemKB(v))
	}
	if v := readProcField(pid, "VmRSS"); v != "" {
		kvPrint("物理内存", formatMemKB(v))
	}
	if n, err := countFD(pid); err == nil {
		kvPrint("打开文件", fmt.Sprintf("%d", n))
	}

	sectionPrint("路径")
	kvPrint("安装目录", floatNull(instDir, dirOf(binPath)))
	kvPrint("程序路径", binPath)
	kvPrint("数据目录", floatNull(rec["DATA_DIR"], dataDir))
	if mediaDir != "" {
		kvPrint("图片目录", mediaDir)
	}
	kvPrint("安装记录", defaultRecordFile)

	// 健康检查
	sectionPrint("健康检查")
	client := &http.Client{Timeout: 3 * time.Second}
	if resp, herr := client.Get(fmt.Sprintf("http://127.0.0.1:%d/api/health", port)); herr == nil {
		_ = resp.Body.Close()
		kvPrint("状态", fmt.Sprintf("正常（http://127.0.0.1:%d/api/health）", port))
	} else {
		kvPrint("状态", "无响应")
	}

	sepPrint()
	return nil
}

// isSystemdActive 判断 systemd 服务是否激活。
func isSystemdActive() string {
	if !isSystemd() {
		return "运行中（非 systemd）"
	}
	out, err := exec.Command("systemctl", "is-active", "--quiet", "fan-image-tr.service").CombinedOutput()
	if err != nil || strings.TrimSpace(string(out)) != "" {
		return "运行中（systemd）"
	}
	return "运行中（systemd）"
}

// countFD 统计进程打开的文件描述符数。
func countFD(pid int) (int, error) {
	es, err := os.ReadDir(fmt.Sprintf("/proc/%d/fd", pid))
	if err != nil {
		return 0, err
	}
	return len(es), nil
}

// formatMemKB 将 kB 数值转为可读字符串（含括号单位）。
func formatMemKB(kbStr string) string {
	parts := strings.Fields(kbStr)
	if len(parts) == 0 {
		return kbStr
	}
	kb, err := strconv.Atoi(parts[0])
	if err != nil || kb <= 0 {
		return kbStr
	}
	if kb >= 1024*1024 {
		return fmt.Sprintf("%s（%.2f GB）", kbStr, float64(kb)/1024/1024)
	}
	if kb >= 1024 {
		return fmt.Sprintf("%s（%.2f MB）", kbStr, float64(kb)/1024)
	}
	return kbStr
}

// pidAlive 判断 PID 对应的进程是否存活。
func pidAlive(pid string) bool {
	pidNum, err := strconv.Atoi(pid)
	if err != nil || pidNum <= 0 {
		return false
	}
	proc, err := os.FindProcess(pidNum)
	if err != nil {
		return false
	}
	// signal 0 仅检查进程是否存在，不发送实际信号
	return proc.Signal(syscall.Signal(0)) == nil
}

func isSystemd() bool {
	_, err := os.Stat("/run/systemd/system")
	return err == nil
}
