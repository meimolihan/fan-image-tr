package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"go.uber.org/zap"

	"github.com/meimolihan/fan-image-tr/internal/config"
	"github.com/meimolihan/fan-image-tr/internal/ffmpeg"
	"github.com/meimolihan/fan-image-tr/internal/handler"
	"github.com/meimolihan/fan-image-tr/internal/logger"
	"github.com/meimolihan/fan-image-tr/internal/service"
	"github.com/meimolihan/fan-image-tr/internal/version"
)

// run 解析命令并执行。
func run(args []string) error {
	if len(args) > 0 {
		switch args[0] {
		case "help", "-h", "--help":
			printUsage(os.Stdout)
			return nil
		case "version", "-v", "--version":
			fmt.Println("fan-image-tr", version.Long())
			return nil
		case "check":
			return runCheck(args[1:])
		case "status":
			return runStatus(args[1:])
		case "serve", "start-web":
			args = args[1:]
		default:
			if len(args[0]) > 0 && args[0][0] == '-' {
				break // 以 - 开头的当作启动选项
			}
			return fmt.Errorf("未知命令: %s（运行 fan-image-tr help 查看用法）", args[0])
		}
	}
	return runServer(args)
}

// serverFlags 启动选项。
type serverFlags struct {
	port     int
	data     string
	media    string
	output   string
	upload   string
	worker   int
	web      string
	debug    bool
	version  bool
	logLevel string
}

// parseFlags 解析启动选项。
func parseFlags(args []string) (*serverFlags, error) {
	f := &serverFlags{}
	fs := flag.NewFlagSet("fan-image-tr", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.IntVar(&f.port, "port", 0, "监听端口")
	fs.StringVar(&f.data, "data", "", "数据目录")
	fs.StringVar(&f.media, "media", "", "图片浏览根目录")
	fs.StringVar(&f.output, "output", "", "转换产物目录")
	fs.StringVar(&f.upload, "upload", "", "上传文件目录")
	fs.IntVar(&f.worker, "worker", 0, "转换并发数")
	fs.StringVar(&f.web, "web", "", "前端静态资源目录")
	fs.BoolVar(&f.debug, "debug", false, "开启调试日志")
	fs.BoolVar(&f.version, "v", false, "打印版本号")
	fs.StringVar(&f.logLevel, "log-level", "", "日志级别 debug/info/warn/error")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	return f, nil
}

// runServer 启动 Web 服务。
func runServer(args []string) error {
	flags, err := parseFlags(args)
	if err != nil {
		printUsage(os.Stderr)
		return err
	}
	if flags.version {
		fmt.Println("fan-image-tr", version.Long())
		return nil
	}

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("加载配置失败: %w", err)
	}
	applyFlags(cfg, flags)
	// 命令行覆盖后需要重新确保目录存在（数据目录可能变了）
	for _, dir := range []string{cfg.App.DataDir, cfg.OutputDir(), cfg.UploadDir()} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("创建目录 %s 失败: %w", dir, err)
		}
	}

	log, err := logger.New(cfg)
	if err != nil {
		return err
	}
	defer func() { _ = log.Sync() }()

	ff, err := ffmpeg.New(ffmpeg.Options{
		FFmpegBin:   cfg.FFmpegBin(),
		FFprobeBin:  cfg.FFprobeBin(),
		Threads:     cfg.FFmpeg.Threads,
		Format:      cfg.FFmpeg.Format,
		Quality:     cfg.FFmpeg.Quality,
		Accel:       cfg.FFmpeg.Accel,
		VAAPIDevice: cfg.FFmpeg.VAAPIDevice,
		HWDecode:    cfg.FFmpeg.HWDecode,
		ThumbSize:   cfg.FFmpeg.ThumbSize,
		ProbeTO:     cfg.FFmpeg.ProbeTimeout,
	})
	if err != nil {
		log.Error("FFmpeg 环境检查失败", zap.Error(err))
		fmt.Fprintln(os.Stderr, "FFmpeg 环境检查失败: ", err)
		fmt.Fprintln(os.Stderr, "请安装 ffmpeg 与 ffprobe：")
		fmt.Fprintln(os.Stderr, "  Debian/Ubuntu: apt install ffmpeg")
		fmt.Fprintln(os.Stderr, "  Alpine:        apk add ffmpeg")
		fmt.Fprintln(os.Stderr, "  CentOS:        yum install ffmpeg")
		fmt.Fprintln(os.Stderr, "或通过 FIT_FFMPEG_PATH / FIT_FFMPEG_FFPROBE_PATH 指定可执行文件路径")
		return err
	}

	ctx := context.Background()
	caps := ff.Detect(ctx)
	if cfg.FFmpeg.DetectAccel {
		log.Info("格式能力已就绪", zap.String("摘要", summarizeCaps(caps)))
	}

	media := service.NewMediaService(cfg, ff, log)
	tasks := service.NewTaskService(cfg, media, ff, log)
	presets, err := service.NewPresetService(cfg)
	if err != nil {
		return fmt.Errorf("加载转换预设失败: %w", err)
	}
	tasks.Start()
	defer tasks.Stop()

	h := handler.New(cfg, ff, media, tasks, presets, log)
	srv := &http.Server{
		Addr:              fmt.Sprintf(":%d", cfg.App.Port),
		Handler:           h.Router(),
		ReadHeaderTimeout: 20 * time.Second,
		// 图片文件可能较大，转换与下载不设置写超时
		IdleTimeout: 120 * time.Second,
	}

	log.Info("服务启动",
		zap.String("version", version.Current()),
		zap.Int("port", cfg.App.Port),
		zap.String("data", cfg.App.DataDir),
		zap.String("output", cfg.OutputDir()),
		zap.String("media", cfg.HomeDir()),
		zap.Int("worker", cfg.App.Worker),
		zap.Bool("upload", cfg.App.AllowUpload),
	)
	fmt.Printf("fan-image-tr %s 已启动: http://127.0.0.1:%d\n", version.Current(), cfg.App.Port)
	fmt.Printf("  图片浏览根目录: %s\n  转换产物目录: %s\n", cfg.HomeDir(), cfg.OutputDir())

	errCh := make(chan error, 1)
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	// SIGHUP 默认动作是终止进程，而本服务不支持热重载配置：
	// 终端关闭、systemd reload 等场景发来的 SIGHUP 不应该把服务带走，直接忽略。
	signal.Ignore(syscall.SIGHUP)
	select {
	case err := <-errCh:
		if err != nil {
			return fmt.Errorf("HTTP 服务异常退出: %w", err)
		}
		return nil
	case <-quit:
	}

	log.Info("收到退出信号，正在优雅关闭")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Warn("HTTP 优雅关闭超时", zap.Error(err))
	}
	log.Info("服务已停止")
	return nil
}

// applyFlags 用命令行参数覆盖配置。
func applyFlags(cfg *config.Config, f *serverFlags) {
	if f.port > 0 {
		cfg.App.Port = f.port
	}
	if f.data != "" {
		cfg.App.DataDir = filepath.Clean(f.data)
	}
	if f.media != "" {
		cfg.App.MediaDir = filepath.Clean(f.media)
	}
	if f.output != "" {
		cfg.App.OutputDir = filepath.Clean(f.output)
	}
	if f.upload != "" {
		cfg.App.UploadDir = filepath.Clean(f.upload)
	}
	if f.worker > 0 {
		cfg.App.Worker = f.worker
	}
	if f.web != "" {
		cfg.App.WebDir = filepath.Clean(f.web)
	}
	if f.debug {
		cfg.App.Debug = true
		cfg.Logging.Level = "debug"
	}
	if f.logLevel != "" {
		cfg.Logging.Level = f.logLevel
	}
}

// summarizeCaps 生成一行能力摘要（启动日志用）。
func summarizeCaps(caps *ffmpeg.Capabilities) string {
	var avail, unavail []string
	for _, f := range caps.Formats {
		if f.Available {
			avail = append(avail, f.Format.ID)
		} else {
			unavail = append(unavail, f.Format.ID)
		}
	}
	s := "可用格式 " + joinList(avail)
	if len(unavail) > 0 {
		s += "，不可用 " + joinList(unavail)
	}
	var hw []string
	for _, a := range caps.HWAccel {
		if a.ID != ffmpeg.AccelNone && a.Available && len(caps.HWFormats) > 0 {
			used := false
			for _, hwf := range caps.HWFormats {
				if hwf.Accel == a.ID {
					used = true
					break
				}
			}
			if used {
				hw = append(hw, a.ID)
			}
		}
	}
	if len(hw) == 0 {
		return s + "，硬件加速：无（仅软件编码）"
	}
	return s + "，硬件加速：" + joinList(hw)
}

func joinList(items []string) string {
	out := ""
	for i, v := range items {
		if i > 0 {
			out += " "
		}
		out += v
	}
	if out == "" {
		return "无"
	}
	return out
}

// printUsage 输出帮助信息。
func printUsage(w io.Writer) {
	fmt.Fprint(w, `fan-image-tr 图片处理工具（内置 FFmpeg 能力）

用法:
  fan-image-tr [命令] [选项]

命令:
  （无命令）        启动 Web 服务
  check             检查 FFmpeg 环境，输出格式与硬件加速能力
  status            查看本机服务运行状态
  version, -v       打印版本号
  help, -h          显示本帮助

启动选项:
  -port int       监听端口（默认 8791）
  -data string    数据目录（任务快照、预设、上传与输出产物）
  -media string   图片浏览根目录（留空则浏览当前工作目录）
  -output string  转换产物目录（相对路径基于 -data）
  -upload string  上传文件目录（相对路径基于 -data）
  -worker int     转换并发数（默认 2）
  -web string     前端静态资源目录（留空则使用内嵌资源）
  -debug          开启调试日志
  -log-level str  日志级别 debug/info/warn/error

环境变量:
  FIT_APP_PORT / FIT_APP_DATA_DIR / FIT_APP_MEDIA_DIR / FIT_APP_OUTPUT_DIR
  FIT_APP_WORKER / FIT_FFMPEG_PATH / FIT_FFMPEG_FFPROBE_PATH / FIT_FFMPEG_ACCEL

示例:
  fan-image-tr -port 8791 -media /volume1/photo -output output
  FIT_APP_PORT=8791 FIT_APP_MEDIA_DIR=/photos fan-image-tr
  fan-image-tr check
`)
}
