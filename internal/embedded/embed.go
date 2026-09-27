// Package embedded 将前端静态资源（web/）内嵌进二进制。
//
// 默认从二进制内嵌副本提供页面；当配置了 app.web_dir（或部署时放置了外部
// 静态目录）且其中存在 index.html 时，优先使用磁盘版本以便直接替换页面文件。
// 任何安装形态（仅下载 Release 二进制的机器、无前端源文件的容器）都能直接
// 打开网页，不会出现 404。
package embedded

import (
	"embed"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
)

//go:embed all:web
var webFS embed.FS

// Resolve 返回前端静态资源服务的 http.FileSystem。
// 优先使用磁盘版 webDir，仅当磁盘上不存在 index.html 时回退到二进制内嵌副本。
func Resolve(webDir string) http.FileSystem {
	if webDir != "" {
		if _, err := os.Stat(filepath.Join(webDir, "index.html")); err == nil {
			return http.Dir(webDir)
		}
	}
	sub, err := fs.Sub(webFS, "web")
	if err != nil {
		panic(err)
	}
	return http.FS(sub)
}

// IndexHTML 返回 index.html 内容，优先磁盘 webDir，缺失时回退内嵌副本。
func IndexHTML(webDir string) ([]byte, error) {
	if webDir != "" {
		if b, err := os.ReadFile(filepath.Join(webDir, "index.html")); err == nil {
			return b, nil
		}
	}
	return webFS.ReadFile("web/index.html")
}
