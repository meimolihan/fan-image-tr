package version

import (
	"os"
	"strings"
)

// Version 由发布构建通过 -ldflags 注入；本地未注入时使用默认值。
var Version = "0.0.4"

// Commit 源码提交号，由发布构建注入，本地未注入时为 unknown。
var Commit = "unknown"

// BuildTime 构建时间（UTC，RFC3339），由发布构建注入。
var BuildTime = ""

// Current 返回当前应用版本，优先使用运行环境变量覆盖。
func Current() string {
	if envVersion := os.Getenv("FIT_VERSION"); envVersion != "" {
		return envVersion
	}
	if envVersion := os.Getenv("APP_VERSION"); envVersion != "" {
		return envVersion
	}
	return Version
}

// Short 返回不含 "v" 前缀的版本号，便于拼接镜像标签。
func Short() string {
	return strings.TrimPrefix(Current(), "v")
}

// Long 返回完整的版本描述：版本号 + 提交号 + 构建时间（缺失部分自动省略）。
func Long() string {
	parts := []string{"v" + Short()}
	if Commit != "" && Commit != "unknown" {
		parts = append(parts, Commit)
	}
	if BuildTime != "" {
		parts = append(parts, BuildTime)
	}
	return strings.Join(parts, " ")
}
