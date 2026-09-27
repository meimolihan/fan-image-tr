package handler

import (
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/meimolihan/fan-image-tr/internal/config"
	"github.com/meimolihan/fan-image-tr/internal/ffmpeg"
	"github.com/meimolihan/fan-image-tr/internal/service"
)

// mediaHandler 提供图片目录浏览、探测、缩略图、原图访问与上传接口。
type mediaHandler struct {
	cfg   *config.Config
	ff    *ffmpeg.Client
	media *service.MediaService
	log   *zap.Logger
}

// listDir 列出目录内容。
func (h *mediaHandler) listDir(c *gin.Context) {
	list, err := h.media.List(c.Query("path"))
	if err != nil {
		respondErr(c, err)
		return
	}
	c.JSON(http.StatusOK, list)
}

// info 返回单张图片的详细信息。
func (h *mediaHandler) info(c *gin.Context) {
	info, err := h.media.Info(c.Request.Context(), c.Query("path"))
	if err != nil {
		respondErr(c, err)
		return
	}
	c.JSON(http.StatusOK, info)
}

// probeBatch 批量探测（前端勾选多张图片后一次性获取尺寸等信息）。
func (h *mediaHandler) probeBatch(c *gin.Context) {
	var req struct {
		Paths []string `json:"paths"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		respondMsg(c, http.StatusBadRequest, "请求格式错误")
		return
	}
	if len(req.Paths) == 0 {
		c.JSON(http.StatusOK, gin.H{"items": []any{}})
		return
	}
	if len(req.Paths) > 500 {
		respondMsg(c, http.StatusBadRequest, "单次最多探测 500 张图片")
		return
	}
	items := make([]gin.H, 0, len(req.Paths))
	for _, p := range req.Paths {
		info, err := h.media.Info(c.Request.Context(), p)
		if err != nil {
			items = append(items, gin.H{"path": p, "error": errMessage(err)})
			continue
		}
		items = append(items, gin.H{"path": p, "info": info})
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

// thumb 返回缩略图（JPEG，带 ETag 强缓存）。
func (h *mediaHandler) thumb(c *gin.Context) {
	size := atoiDefault(c.Query("size"), 0)
	data, err := h.media.Thumbnail(c.Request.Context(), c.Query("path"), size)
	if err != nil {
		respondErr(c, err)
		return
	}
	tag := etagOf(data)
	c.Header("ETag", tag)
	// 缩略图是不可变派生数据，可长期缓存；URL 带 path+size，内容变化由 ETag 区分
	c.Header("Cache-Control", "private, max-age=3600")
	if matchETag(c.GetHeader("If-None-Match"), tag) {
		c.Status(http.StatusNotModified)
		return
	}
	c.Data(http.StatusOK, "image/jpeg", data)
}

// raw 返回原图 / 产物文件（支持 Range，便于浏览器大图预览与下载）。
func (h *mediaHandler) raw(c *gin.Context) {
	abs, err := h.media.ResolvePath(c.Query("path"))
	if err != nil {
		respondErr(c, err)
		return
	}
	f, info, ok := openImage(c, abs)
	if !ok {
		return
	}
	defer f.Close()
	c.Header("Cache-Control", "private, max-age=300")
	http.ServeContent(c.Writer, c.Request, info.Name(), info.ModTime(), f)
}

// download 以附件形式下载文件。
func (h *mediaHandler) download(c *gin.Context) {
	abs, err := h.media.ResolvePath(c.Query("path"))
	if err != nil {
		respondErr(c, err)
		return
	}
	f, info, ok := openImage(c, abs)
	if !ok {
		return
	}
	defer f.Close()
	c.Header("Cache-Control", "no-store")
	c.Header("Content-Disposition", contentDisposition(info.Name()))
	http.ServeContent(c.Writer, c.Request, info.Name(), info.ModTime(), f)
}

// rename 重命名文件或目录。
func (h *mediaHandler) rename(c *gin.Context) {
	var req struct {
		Path    string `json:"path"`
		NewName string `json:"new_name"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		respondMsg(c, http.StatusBadRequest, "请求格式错误")
		return
	}
	if err := h.media.Rename(req.Path, req.NewName); err != nil {
		respondErr(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// remove 删除文件或空目录。
func (h *mediaHandler) remove(c *gin.Context) {
	var req struct {
		Path string `json:"path"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		respondMsg(c, http.StatusBadRequest, "请求格式错误")
		return
	}
	if err := h.media.Delete(req.Path); err != nil {
		respondErr(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// mkdir 新建子目录。
func (h *mediaHandler) mkdir(c *gin.Context) {
	var req struct {
		Path string `json:"path"`
		Name string `json:"name"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		respondMsg(c, http.StatusBadRequest, "请求格式错误")
		return
	}
	if err := h.media.CreateFolder(req.Path, req.Name); err != nil {
		respondErr(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// estimate 预估转换后的尺寸与体积（前端实时反馈，不落盘）。
func (h *mediaHandler) estimate(c *gin.Context) {
	var req struct {
		Options ffmpeg.ConvertOptions `json:"options"`
		// 多张图片时用第一张作为基准
		Path string `json:"path"`
		// 或直接给出源图尺寸
		Width  int `json:"width"`
		Height int `json:"height"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		respondMsg(c, http.StatusBadRequest, "请求格式错误")
		return
	}
	srcW, srcH := req.Width, req.Height
	if req.Path != "" && (srcW <= 0 || srcH <= 0) {
		info, err := h.media.Info(c.Request.Context(), req.Path)
		if err != nil {
			respondErr(c, err)
			return
		}
		srcW, srcH = info.Width, info.Height
	}
	est, err := service.Estimate(req.Options, srcW, srcH)
	if err != nil {
		respondErr(c, err)
		return
	}
	c.JSON(http.StatusOK, est)
}

// preview 返回当前参数对应的 ffmpeg 命令（仅展示，不执行）。
func (h *mediaHandler) preview(c *gin.Context) {
	var req struct {
		Options ffmpeg.ConvertOptions `json:"options"`
		Path    string                `json:"path"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		respondMsg(c, http.StatusBadRequest, "请求格式错误: "+errMessage(err))
		return
	}
	caps := h.ff.Detect(c.Request.Context())
	opts := req.Options
	if req.Path != "" {
		info, err := h.media.Info(c.Request.Context(), req.Path)
		if err != nil {
			respondErr(c, err)
			return
		}
		opts.SourceWidth, opts.SourceHeight, opts.SourceAlpha = info.Width, info.Height, info.Alpha
	}
	opts.Threads = h.cfg.FFmpeg.Threads
	opts.Accel = h.cfg.FFmpeg.Accel
	opts.VAAPIDevice = h.cfg.FFmpeg.VAAPIDevice
	opts.HWDecode = h.cfg.FFmpeg.HWDecode

	clamped, err := ffmpeg.Clamp(opts, caps)
	if err != nil {
		respondErr(c, err)
		return
	}
	// 未指定文件时用占位名；指定了才解析真实路径
	in, base := "<源文件>", "<源文件名>"
	if req.Path != "" {
		if abs, err := h.media.ResolvePath(req.Path); err == nil {
			in = abs
			stem := filepath.Base(abs)
			base = strings.TrimSuffix(stem, filepath.Ext(stem))
		}
	}
	f, _ := ffmpeg.FormatByID(clamped.Format)
	// 用与任务一致的命名规则推导文件名，避免预览里出现"输出.webp"这种占位名
	out := filepath.Join(h.media.OutputDir(), service.OutputName(base, "", f.Ext))
	args, err := h.ff.BuildConvertArgs(*clamped, caps, in, out)
	if err != nil {
		respondErr(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"command": ffmpeg.CommandLine(h.ff.FFmpegBin(), args),
	})
}

// upload 上传图片（多文件），落盘到上传目录后返回可直接用于转换的相对路径。
func (h *mediaHandler) upload(c *gin.Context) {
	if !h.cfg.App.AllowUpload {
		respondMsg(c, http.StatusForbidden, "服务端已关闭上传功能")
		return
	}
	form, err := c.MultipartForm()
	if err != nil {
		respondMsg(c, http.StatusBadRequest, "上传请求解析失败: "+errMessage(err))
		return
	}
	defer form.RemoveAll()

	files := form.File["files"]
	if len(files) == 0 {
		files = form.File["file"]
	}
	if len(files) == 0 {
		respondMsg(c, http.StatusBadRequest, "没有收到文件")
		return
	}
	if len(files) > 100 {
		respondMsg(c, http.StatusBadRequest, "单次最多上传 100 个文件")
		return
	}
	limit := h.cfg.MaxUploadBytes()
	dir := h.cfg.UploadDir()

	items := make([]gin.H, 0, len(files))
	failures := make([]string, 0)
	for _, fh := range files {
		name, err := h.saveUpload(dir, fh, limit)
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s: %s", fh.Filename, errMessage(err)))
			continue
		}
		items = append(items, gin.H{"path": name})
	}
	if len(items) == 0 {
		respondMsg(c, http.StatusBadRequest, "上传失败: "+strings.Join(failures, "; "))
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"files":    items,
		"failed":   failures,
		"uploaded": len(items),
	})
}

// saveUpload 保存单个上传文件，返回可浏览的相对路径。
func (h *mediaHandler) saveUpload(dir string, fh *multipart.FileHeader, limit int64) (string, error) {
	filename := filepath.Base(strings.ReplaceAll(fh.Filename, "\\", "/"))
	if filename == "." || filename == "/" || filename == "" {
		return "", errors.New("文件名无效")
	}
	if !ffmpeg.IsImageFile(filename) {
		return "", errors.New("不支持的文件类型")
	}
	if limit > 0 && fh.Size > limit {
		return "", fmt.Errorf("超过大小上限 %d MB", h.cfg.App.MaxUploadMB)
	}

	// 打开上传文件
	src, err := fh.Open()
	if err != nil {
		return "", err
	}
	defer src.Close()

	// 同名文件自动追加序号，避免覆盖已有上传
	stamp := time.Now().Format("20060102_150405")
	base := strings.TrimSuffix(filename, filepath.Ext(filename))
	ext := strings.ToLower(filepath.Ext(filename))
	dst := filepath.Join(dir, fmt.Sprintf("%s_%s%s", base, stamp, ext))
	for i := 1; ; i++ {
		if _, err := os.Stat(dst); os.IsNotExist(err) {
			break
		}
		dst = filepath.Join(dir, fmt.Sprintf("%s_%s_%d%s", base, stamp, i, ext))
		if i > 999 {
			return "", errors.New("文件名冲突，请重试")
		}
	}

	// 写临时文件后改名，避免中断产生半截文件
	tmp, err := os.CreateTemp(dir, ".upload-*")
	if err != nil {
		return "", err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	written, err := copyLimited(tmp, src, limit)
	if err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(tmpName, dst); err != nil {
		return "", err
	}
	if written == 0 {
		_ = os.Remove(dst)
		return "", errors.New("文件内容为空")
	}
	return h.media.RelPath(dst), nil
}

// copyLimited 复制数据并在超过上限时中断。
func copyLimited(dst io.Writer, src io.Reader, limit int64) (int64, error) {
	var reader io.Reader = src
	if limit > 0 {
		reader = io.LimitReader(src, limit+1)
	}
	n, err := io.Copy(dst, reader)
	if err != nil {
		return n, err
	}
	if limit > 0 && n > limit {
		return n, fmt.Errorf("文件超过大小上限 %d MB", limit>>20)
	}
	return n, nil
}

// contentDisposition 构造附件下载头（中文文件名走 RFC 5987 编码）。
func contentDisposition(name string) string {
	ascii := make([]rune, 0, len(name))
	for _, r := range name {
		if r < 128 && r != '"' {
			ascii = append(ascii, r)
		} else {
			ascii = append(ascii, '_')
		}
	}
	return fmt.Sprintf(`attachment; filename="%s"; filename*=UTF-8''%s`,
		string(ascii), urlEscape(name))
}

func urlEscape(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') ||
			c == '-' || c == '_' || c == '.' || c == '~' {
			b.WriteByte(c)
			continue
		}
		b.WriteString(fmt.Sprintf("%%%02X", c))
	}
	return b.String()
}

func atoiDefault(s string, def int) int {
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return n
}

// openImage 打开一个位于允许目录内的图片文件。
// 除路径越权外，还要求扩展名在受支持的输入格式白名单内，
// 避免 raw / download 退化成任意文件读取接口（例如 tasks.json）。
func openImage(c *gin.Context, abs string) (*os.File, os.FileInfo, bool) {
	if !ffmpeg.IsImageFile(abs) {
		respondMsg(c, http.StatusBadRequest, "不是受支持的图片文件")
		return nil, nil, false
	}
	f, err := os.Open(abs)
	if err != nil {
		respondMsg(c, http.StatusNotFound, "文件不存在")
		return nil, nil, false
	}
	info, err := f.Stat()
	if err != nil || info.IsDir() {
		f.Close()
		respondMsg(c, http.StatusNotFound, "文件不存在")
		return nil, nil, false
	}
	return f, info, true
}
