// Package logger 根据配置创建结构化日志实例（基于 zap）。
package logger

import (
	"io"
	"os"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/meimolihan/fan-image-tr/internal/config"
)

// New 根据配置创建 zap.Logger。
// 支持 console / json 两种格式，可输出到 stdout、stderr 或指定文件。
// 输出路径为空且未开启 to_stderr 时默认输出到 stdout。
func New(cfg *config.Config) (*zap.Logger, error) {
	level := zapcore.InfoLevel
	if cfg.Logging.Level != "" {
		if lv, err := zapcore.ParseLevel(cfg.Logging.Level); err == nil {
			level = lv
		}
	}

	encCfg := zap.NewProductionEncoderConfig()
	encCfg.EncodeTime = zapcore.ISO8601TimeEncoder
	encCfg.EncodeLevel = zapcore.CapitalColorLevelEncoder
	encCfg.ConsoleSeparator = " "

	var encoder zapcore.Encoder
	if cfg.Logging.Format == "json" {
		encCfg = zap.NewProductionEncoderConfig()
		encCfg.EncodeTime = zapcore.ISO8601TimeEncoder
		encoder = zapcore.NewJSONEncoder(encCfg)
	} else {
		encoder = zapcore.NewConsoleEncoder(encCfg)
	}

	var sink io.Writer = os.Stdout
	switch {
	case cfg.Logging.OutputPath != "":
		f, err := os.OpenFile(cfg.Logging.OutputPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
		if err != nil {
			return nil, err
		}
		sink = f
	case cfg.Logging.ToStderr:
		sink = os.Stderr
	}

	core := zapcore.NewCore(encoder, zapcore.AddSync(sink), level)
	return zap.New(core, zap.AddCaller(), zap.AddStacktrace(zapcore.ErrorLevel)), nil
}
