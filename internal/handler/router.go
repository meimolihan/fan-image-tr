// Package handler 提供 HTTP API 与前端静态资源路由（基于 gin）。
package handler

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/meimolihan/fan-image-tr/internal/config"
	"github.com/meimolihan/fan-image-tr/internal/embedded"
	"github.com/meimolihan/fan-image-tr/internal/ffmpeg"
	"github.com/meimolihan/fan-image-tr/internal/service"
)

// assetVersionPlaceholder 是 index.html 内静态资源 URL 上的版本占位符，
// 提供 index.html 时替换为资源内容哈希（见 computeAssetVersion）。
const assetVersionPlaceholder = "__ASSET_V__"

// nowFunc 便于测试替换时钟。
var nowFunc = time.Now

// Handler 聚合 HTTP 处理器。
type Handler struct {
	cfg     *config.Config
	ff      *ffmpeg.Client
	media   *service.MediaService
	tasks   *service.TaskService
	presets *service.PresetService
	log     *zap.Logger
	webRoot http.FileSystem
	assetV  string
}

// New 构建 Handler。
func New(cfg *config.Config, ff *ffmpeg.Client, media *service.MediaService, tasks *service.TaskService, presets *service.PresetService, log *zap.Logger) *Handler {
	if log == nil {
		log = zap.NewNop()
	}
	h := &Handler{
		cfg:     cfg,
		ff:      ff,
		media:   media,
		tasks:   tasks,
		presets: presets,
		log:     log,
		webRoot: embedded.Resolve(cfg.App.WebDir),
	}
	h.assetV = h.computeAssetVersion()
	return h
}

// computeAssetVersion 以 index.html 与 css/js 的内容哈希作为前端资源版本号。
// 资源内容变化 → 版本变化 → 浏览器请求的 URL 变化，从而不会命中旧缓存；
// /css、/js 的长缓存（immutable）也因此变得安全。
// 读取失败时退化为进程内唯一值，保证 URL 依然不会复用。
func (h *Handler) computeAssetVersion() string {
	sum := sha256.New()
	for _, name := range []string{"index.html", "css/style.css", "js/app.js"} {
		f, err := h.webRoot.Open(name)
		if err != nil {
			h.log.Warn("计算前端资源版本失败", zap.String("file", name), zap.Error(err))
			return strconv.FormatInt(time.Now().UnixNano(), 36)
		}
		_, err = io.Copy(sum, f)
		f.Close()
		if err != nil {
			h.log.Warn("计算前端资源版本失败", zap.String("file", name), zap.Error(err))
			return strconv.FormatInt(time.Now().UnixNano(), 36)
		}
	}
	return hex.EncodeToString(sum.Sum(nil))[:12]
}

// Router 构建并返回 gin 路由。
func (h *Handler) Router() *gin.Engine {
	if !h.cfg.App.Debug {
		gin.SetMode(gin.ReleaseMode)
	}
	r := gin.New()
	r.Use(gin.Recovery(), requestLogger(h.log))
	r.MaxMultipartMemory = 8 << 20

	api := r.Group("/api")
	{
		api.GET("/health", func(c *gin.Context) {
			c.JSON(http.StatusOK, gin.H{"status": "ok"})
		})
		api.GET("/version", h.metaH().version)

		// 能力与选项字典（前端表单的唯一数据源）
		api.GET("/capabilities", h.metaH().capabilities)
		api.GET("/options", h.metaH().options)

		// 图片浏览
		api.GET("/media/dir", h.mediaH().listDir)
		api.GET("/media/info", h.mediaH().info)
		api.POST("/media/probe", h.mediaH().probeBatch)
		api.GET("/media/thumb", h.mediaH().thumb)
		api.GET("/media/raw", h.mediaH().raw)
		api.GET("/media/download", h.mediaH().download)
		api.POST("/media/rename", h.mediaH().rename)
		api.POST("/media/delete", h.mediaH().remove)
		api.POST("/media/mkdir", h.mediaH().mkdir)
		api.POST("/media/upload", h.mediaH().upload)

		// 体积预估与命令预览
		api.POST("/estimate", h.mediaH().estimate)
		api.POST("/preview", h.mediaH().preview)

		// 转换任务
		api.GET("/tasks", h.taskH().list)
		api.POST("/tasks", h.taskH().create)
		api.DELETE("/tasks", h.taskH().clear)
		// 静态段须先于 /tasks/:id 注册，避免被通配吞掉
		api.GET("/tasks/stats", h.taskH().stats)
		api.GET("/tasks/:id", h.taskH().get)
		api.POST("/tasks/:id/cancel", h.taskH().cancel)
		api.POST("/tasks/:id/retry", h.taskH().retry)
		api.DELETE("/tasks/:id", h.taskH().remove)

		// 转换预设
		api.GET("/presets", h.presetH().list)
		api.POST("/presets", h.presetH().save)
		api.DELETE("/presets/:name", h.presetH().remove)
	}

	h.serveStatic(r)
	return r
}

// 子处理器（惰性构造，便于测试替换）
func (h *Handler) metaH() *metaHandler {
	return &metaHandler{cfg: h.cfg, media: h.media, tasks: h.tasks, ff: h.ff, log: h.log}
}

func (h *Handler) mediaH() *mediaHandler {
	return &mediaHandler{cfg: h.cfg, ff: h.ff, media: h.media, log: h.log}
}

func (h *Handler) taskH() *taskHandler {
	return &taskHandler{tasks: h.tasks, log: h.log}
}

func (h *Handler) presetH() *presetHandler {
	return &presetHandler{presets: h.presets, log: h.log}
}

// requestLogger 精简的访问日志：跳过静态资源与健康检查，避免刷屏。
func requestLogger(log *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		p := c.Request.URL.Path
		if p == "/api/health" || p == "/favicon.svg" ||
			strings.HasPrefix(p, "/css/") || strings.HasPrefix(p, "/js/") || strings.HasPrefix(p, "/assets/") {
			c.Next()
			return
		}
		start := nowFunc()
		c.Next()
		log.Debug("请求完成",
			zap.String("method", c.Request.Method),
			zap.String("path", p),
			zap.Int("status", c.Writer.Status()),
			zap.Duration("cost", nowFunc().Sub(start)),
		)
	}
}

// serveStatic 提供前端静态资源：/ 与 asset 路径从 webRoot（磁盘或内嵌）读取，
// 未匹配到文件的路径回退到 index.html（单页应用友好）。
func (h *Handler) serveStatic(r *gin.Engine) {
	r.GET("/favicon.svg", h.favicon)
	routes := []string{"/css/", "/js/", "/assets/"}
	r.NoRoute(func(c *gin.Context) {
		p := c.Request.URL.Path
		clean := path.Clean("/" + p)
		if !strings.HasPrefix(clean, "/api/") {
			for _, prefix := range routes {
				if strings.HasPrefix(clean, prefix) {
					h.serveFile(c, strings.TrimPrefix(clean, "/"), "public, max-age=31536000, immutable")
					return
				}
			}
			h.serveIndex(c)
			return
		}
		c.JSON(http.StatusNotFound, gin.H{"error": "接口不存在"})
	})
}

// serveIndex 提供 SPA 入口页：把资源 URL 上的版本占位符替换为内容哈希，
// 使前端资源在重新构建后自动获得新 URL，无需用户手动清缓存。
func (h *Handler) serveIndex(c *gin.Context) {
	f, err := h.webRoot.Open("/index.html")
	if err != nil {
		c.Status(http.StatusNotFound)
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || info.IsDir() {
		c.Status(http.StatusNotFound)
		return
	}
	raw, err := io.ReadAll(f)
	if err != nil {
		c.Status(http.StatusInternalServerError)
		return
	}
	page := bytes.ReplaceAll(raw, []byte(assetVersionPlaceholder), []byte(h.assetV))
	tag := etagOf(page)
	c.Header("ETag", tag)
	if matchETag(c.GetHeader("If-None-Match"), tag) {
		c.Status(http.StatusNotModified)
		return
	}
	c.Header("Cache-Control", "no-cache")
	c.Data(http.StatusOK, "text/html; charset=utf-8", page)
}

// favicon 提供站点图标（替换免重编译、免清缓存生效）。
// 解析优先级：app.favicon 配置 > <数据目录>/favicon.svg > web_dir/内嵌默认图标。
func (h *Handler) favicon(c *gin.Context) {
	const cacheControl = "no-cache"
	if p := h.cfg.FaviconPath(); p != "" {
		if h.serveDiskFile(c, p, cacheControl) {
			return
		}
	}
	if p := filepath.Join(h.cfg.App.DataDir, "favicon.svg"); h.serveDiskFile(c, p, cacheControl) {
		return
	}
	h.serveFile(c, "favicon.svg", cacheControl)
}

// serveDiskFile 从磁盘提供单个文件；文件缺失返回 false。
func (h *Handler) serveDiskFile(c *gin.Context, p, cacheControl string) bool {
	f, err := os.Open(p)
	if err != nil {
		return false
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || info.IsDir() {
		return false
	}
	h.serveFileInfo(c, f, info, cacheControl)
	return true
}

// serveFile 从 webRoot 提供单个文件（支持缓存头）。
func (h *Handler) serveFile(c *gin.Context, name, cacheControl string) {
	f, err := h.webRoot.Open(path.Clean("/" + name))
	if err != nil {
		c.Status(http.StatusNotFound)
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || info.IsDir() {
		c.Status(http.StatusNotFound)
		return
	}
	h.serveFileInfo(c, f, info, cacheControl)
}

// serveFileInfo 写缓存头并输出文件内容。
func (h *Handler) serveFileInfo(c *gin.Context, f io.ReadSeeker, info fs.FileInfo, cacheControl string) {
	if cacheControl != "" {
		c.Header("Cache-Control", cacheControl)
	}
	http.ServeContent(c.Writer, c.Request, info.Name(), info.ModTime(), f)
}

// ==================== 通用工具 ====================

// etagOf 计算内容 ETag（sha256 前 16 位十六进制）。
func etagOf(b []byte) string {
	sum := sha256.Sum256(b)
	return `"` + hex.EncodeToString(sum[:])[:16] + `"`
}

// matchETag 判断客户端缓存是否仍然有效。
func matchETag(header, tag string) bool {
	for _, v := range strings.Split(header, ",") {
		v = strings.TrimSpace(v)
		if v == "*" || strings.TrimPrefix(v, "W/") == tag {
			return true
		}
	}
	return false
}

// errMessage 提取错误信息。
func errMessage(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// respondMsg 返回统一格式的错误响应。
func respondMsg(c *gin.Context, status int, msg string) {
	c.AbortWithStatusJSON(status, gin.H{"error": msg})
}

// respondErr 根据错误类型选择合适的 HTTP 状态码。
func respondErr(c *gin.Context, err error) {
	msg := errMessage(err)
	if errors.Is(err, service.ErrPathNotAllowed) {
		respondMsg(c, http.StatusForbidden, msg)
		return
	}
	if errors.Is(err, service.ErrTaskNotFound) || errors.Is(err, service.ErrPresetNotFound) {
		respondMsg(c, http.StatusNotFound, msg)
		return
	}
	if errors.Is(err, service.ErrTaskConflict) {
		respondMsg(c, http.StatusConflict, msg)
		return
	}
	var de *ffmpeg.DetectError
	if errors.As(err, &de) {
		respondMsg(c, http.StatusServiceUnavailable, msg)
		return
	}
	switch {
	case strings.Contains(msg, "不存在"):
		respondMsg(c, http.StatusNotFound, msg)
	case strings.Contains(msg, "不支持"), strings.Contains(msg, "无效"), strings.Contains(msg, "请"), strings.Contains(msg, "超出"):
		respondMsg(c, http.StatusBadRequest, msg)
	default:
		respondMsg(c, http.StatusInternalServerError, msg)
	}
}
