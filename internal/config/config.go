// Package config 负责应用配置的定义与加载。
//
// 加载优先级（低 -> 高）：
//  1. 内置默认值
//  2. 配置文件 config.yaml（当前目录 / ./data / /etc/fan-image-tr）
//  3. 环境变量（FIT_ 前缀，如 FIT_APP_PORT=8791）
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/viper"
)

// ==================== 子配置结构体 ====================

// AppConfig 应用运行环境配置
type AppConfig struct {
	// 服务器监听端口，默认 8791
	Port int `mapstructure:"port"`
	// 调试模式，默认 false
	Debug bool `mapstructure:"debug"`
	// 运行环境标识：development / production / testing
	Env string `mapstructure:"env"`
	// 数据目录（转换产物、模板 profiles.json、临时上传），默认 ./data
	DataDir string `mapstructure:"data_dir"`
	// 前端静态文件目录（可选），留空时使用二进制内嵌资源
	WebDir string `mapstructure:"web_dir"`
	// 自定义 favicon 图标文件路径（可选，默认空）；相对路径基于数据目录解析。
	// 留空时依次回退：<数据目录>/favicon.svg（免配置替换）→ 内嵌默认图标。
	Favicon string `mapstructure:"favicon"`
	// 图片浏览根目录（可选），留空时从当前工作目录开始浏览
	MediaDir string `mapstructure:"media_dir"`
	// 转换输出目录，默认 <data_dir>/output
	OutputDir string `mapstructure:"output_dir"`
	// 转换任务并发数，默认 2（单张图片处理通常在秒级，机械盘建议 1~2）
	Worker int `mapstructure:"worker"`
	// 浏览器拖拽上传的落盘目录（相对路径基于 data_dir），默认 uploads
	UploadDir string `mapstructure:"upload_dir"`
	// 上传单文件大小上限（MB），默认 64
	MaxUploadMB int `mapstructure:"max_upload_mb"`
	// 是否允许拖拽 / 表格单上传图片（关闭后仅能浏览服务器本地图片）
	AllowUpload bool `mapstructure:"allow_upload"`
}

// FFmpegConfig FFmpeg 相关配置
type FFmpegConfig struct {
	// FFmpeg 可执行文件路径
	Path string `mapstructure:"path"`
	// FFprobe 可执行文件路径
	FFprobePath string `mapstructure:"ffprobe_path"`
	// 处理线程数；0 表示由 ffmpeg 自动探测
	Threads int `mapstructure:"threads"`
	// 默认输出格式：jpg / png / webp / avif / …
	Format string `mapstructure:"format"`
	// 默认质量（1~100，各格式由服务端换算为自身量纲）
	Quality int `mapstructure:"quality"`
	// 默认硬件加速：auto / none / nvenc / qsv / vaapi
	Accel string `mapstructure:"accel"`
	// VAAPI 渲染设备节点
	VAAPIDevice string `mapstructure:"vaapi_device"`
	// 硬件解码：false = 仅硬件编码 + 软件解码（兼容性最好）
	HWDecode bool `mapstructure:"hw_decode"`
	// 启动时实际跑一次微型编码自检，避免把不可用的加速方式标成可用
	DetectAccel bool `mapstructure:"detect_accel"`
	// 缩略图生成器（imagick / gm / ffmpeg），留空自动探测
	ThumbPath string `mapstructure:"thumb_path"`
	// 单张缩略图最长边（像素）
	ThumbSize int `mapstructure:"thumb_size"`
	// 探测单张图片信息超时（秒）
	ProbeTimeout int `mapstructure:"probe_timeout"`
}

// LoggingConfig 日志记录设置
type LoggingConfig struct {
	// 日志级别: debug / info / warn / error
	Level string `mapstructure:"level"`
	// 日志输出格式: json / console
	Format string `mapstructure:"format"`
	// 日志输出文件路径，留空则输出到 stdout
	OutputPath string `mapstructure:"output_path"`
	// 是否输出到 stderr（与 OutputPath 互斥）
	ToStderr bool `mapstructure:"to_stderr"`
}

// ==================== 主配置结构体 ====================

// Config 应用主配置（聚合所有子模块）
type Config struct {
	App     AppConfig     `mapstructure:"app"`
	FFmpeg  FFmpegConfig  `mapstructure:"ffmpeg"`
	Logging LoggingConfig `mapstructure:"logging"`
}

// Load 加载配置（详见包注释中的优先级说明）。
func Load() (*Config, error) {
	setDefaults()

	viper.SetConfigName("config")
	viper.SetConfigType("yaml")
	viper.AddConfigPath(".")
	viper.AddConfigPath("./data")
	viper.AddConfigPath("/etc/fan-image-tr")

	viper.SetEnvPrefix("FIT")
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	viper.AutomaticEnv()

	_ = viper.ReadInConfig()

	cfg := &Config{}
	if err := viper.Unmarshal(cfg); err != nil {
		return nil, fmt.Errorf("解析配置失败: %w", err)
	}

	// 确保目录存在（相对路径基于数据目录解析，见 OutputDir）
	for _, dir := range []string{cfg.App.DataDir, cfg.OutputDir(), cfg.UploadDir()} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, fmt.Errorf("创建目录 %s 失败: %w", dir, err)
		}
	}

	return cfg, nil
}

// setDefaults 设置所有默认值
func setDefaults() {
	viper.SetDefault("app.port", 8791)
	viper.SetDefault("app.debug", false)
	viper.SetDefault("app.env", "production")
	viper.SetDefault("app.data_dir", "./data")
	viper.SetDefault("app.web_dir", "")
	viper.SetDefault("app.favicon", "")
	viper.SetDefault("app.media_dir", "")
	viper.SetDefault("app.output_dir", "output")
	viper.SetDefault("app.worker", 2)
	viper.SetDefault("app.upload_dir", "uploads")
	viper.SetDefault("app.max_upload_mb", 64)
	viper.SetDefault("app.allow_upload", true)

	viper.SetDefault("ffmpeg.path", "ffmpeg")
	viper.SetDefault("ffmpeg.ffprobe_path", "ffprobe")
	viper.SetDefault("ffmpeg.threads", 0)
	viper.SetDefault("ffmpeg.format", "webp")
	viper.SetDefault("ffmpeg.quality", 82)
	viper.SetDefault("ffmpeg.accel", "auto")
	viper.SetDefault("ffmpeg.vaapi_device", "/dev/dri/renderD128")
	viper.SetDefault("ffmpeg.hw_decode", false)
	viper.SetDefault("ffmpeg.detect_accel", true)
	viper.SetDefault("ffmpeg.thumb_path", "")
	viper.SetDefault("ffmpeg.thumb_size", 512)
	viper.SetDefault("ffmpeg.probe_timeout", 20)

	viper.SetDefault("logging.level", "info")
	viper.SetDefault("logging.format", "console")
	viper.SetDefault("logging.output_path", "")
	viper.SetDefault("logging.to_stderr", false)
}

// ==================== 便捷访问方法 ====================

// OutputDir 返回转换输出目录。绝对路径原样返回；相对路径基于数据目录解析，
// 因此 -data 覆盖数据目录后会自动得到正确的输出位置。
func (c *Config) OutputDir() string {
	if filepath.IsAbs(c.App.OutputDir) {
		return c.App.OutputDir
	}
	if c.App.OutputDir == "" {
		return filepath.Join(c.App.DataDir, "output")
	}
	return filepath.Join(c.App.DataDir, c.App.OutputDir)
}

// UploadDir 返回拖拽上传文件的落盘目录（相对路径基于数据目录解析）。
func (c *Config) UploadDir() string {
	if filepath.IsAbs(c.App.UploadDir) {
		return c.App.UploadDir
	}
	if c.App.UploadDir == "" {
		return filepath.Join(c.App.DataDir, "uploads")
	}
	return filepath.Join(c.App.DataDir, c.App.UploadDir)
}

// MaxUploadBytes 返回上传单文件大小上限（字节）；<=0 表示不限制。
func (c *Config) MaxUploadBytes() int64 {
	if c.App.MaxUploadMB <= 0 {
		return 0
	}
	return int64(c.App.MaxUploadMB) * 1024 * 1024
}

// ProfilesPath 返回转换预设（模板）文件路径。
func (c *Config) ProfilesPath() string {
	return filepath.Join(c.App.DataDir, "profiles.json")
}

// FaviconPath 返回自定义 favicon 文件路径。未配置返回空串；
// 相对路径基于数据目录解析（与 OutputDir 同规则，-data 覆盖后自动跟随）。
func (c *Config) FaviconPath() string {
	if c.App.Favicon == "" {
		return ""
	}
	if filepath.IsAbs(c.App.Favicon) {
		return c.App.Favicon
	}
	return filepath.Join(c.App.DataDir, c.App.Favicon)
}

// FFmpegBin 返回 FFmpeg 可执行文件
func (c *Config) FFmpegBin() string { return c.FFmpeg.Path }

// FFprobeBin 返回 FFprobe 可执行文件
func (c *Config) FFprobeBin() string { return c.FFmpeg.FFprobePath }

// HomeDir 返回图片浏览的默认根目录：显式配置优先，否则退回当前工作目录。
func (c *Config) HomeDir() string {
	if dir := strings.TrimSpace(c.App.MediaDir); dir != "" {
		return filepath.Clean(dir)
	}
	if dir, err := os.Getwd(); err == nil {
		return dir
	}
	return string(filepath.Separator)
}
