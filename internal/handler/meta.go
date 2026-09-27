package handler

import (
	"net/http"
	"runtime"
	"strings"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/meimolihan/fan-image-tr/internal/config"
	"github.com/meimolihan/fan-image-tr/internal/ffmpeg"
	"github.com/meimolihan/fan-image-tr/internal/service"
	"github.com/meimolihan/fan-image-tr/internal/version"
)

// metaHandler 提供版本信息、FFmpeg 能力与前端表单字典。
type metaHandler struct {
	cfg   *config.Config
	media *service.MediaService
	tasks *service.TaskService
	ff    *ffmpeg.Client
	log   *zap.Logger
}

// version 返回服务与运行时版本信息。
func (h *metaHandler) version(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"version":      version.Version,
		"commit":       version.Commit,
		"build_time":   version.BuildTime,
		"go_version":   runtime.Version(),
		"platform":     runtime.GOOS + "/" + runtime.GOARCH,
		"port":         h.cfg.App.Port,
		"env":          h.cfg.App.Env,
		"allow_upload": h.cfg.App.AllowUpload,
		"max_upload":   h.cfg.App.MaxUploadMB,
	})
}

// capabilities 返回 FFmpeg 环境能力（前端据此渲染格式列表与加速选项）。
func (h *metaHandler) capabilities(c *gin.Context) {
	caps := h.ff.Detect(c.Request.Context())
	c.JSON(http.StatusOK, caps)
}

// options 返回前端表单所需的全部选项字典。
// 这是前端唯一的数据源：新增格式 / 选项只需改动 ffmpeg 包，前端自动适配。
func (h *metaHandler) options(c *gin.Context) {
	ctx := c.Request.Context()
	caps := h.ff.Detect(ctx)

	// 格式列表附带本机可用性，便于前端禁用不可用格式并提示原因
	support := make(map[string]ffmpeg.FormatSupport, len(caps.Formats))
	for _, fs := range caps.Formats {
		support[fs.Format.ID] = fs
	}
	formats := make([]gin.H, 0, len(ffmpeg.Formats()))
	for _, f := range ffmpeg.Formats() {
		s := support[f.ID]
		formats = append(formats, gin.H{
			"id":                 f.ID,
			"name":               f.Name,
			"ext":                f.Ext,
			"mime_type":          f.MimeType,
			"alpha":              f.Alpha,
			"lossless":           f.Lossless,
			"animated":           f.Animated,
			"note":               f.Note,
			"available":          s.Available,
			"reason":             s.Reason,
			"default_quality":    f.DefaultQuality,
			"quality":            f.Quality,
			"animated_available": s.AnimatedAvailable,
		})
	}

	// 硬件加速：仅保留真实自检通过（HWFormats 中出现过）的组合
	hwUsed := map[string][]string{}
	for id, hw := range caps.HWFormats {
		hwUsed[hw.Accel] = append(hwUsed[hw.Accel], id)
	}
	accels := make([]gin.H, 0, len(caps.HWAccel))
	for _, a := range caps.HWAccel {
		formats4accel := hwUsed[a.ID]
		sortStrings(formats4accel)
		accels = append(accels, gin.H{
			"id":        a.ID,
			"name":      a.Name,
			"available": a.Available && len(formats4accel) > 0,
			"reason":    accelReason(a, len(formats4accel)),
			"device":    a.Device,
			"formats":   formats4accel,
			"encoders":  a.Encoders,
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"formats":         formats,
		"resize_modes":    ffmpeg.ResizeModes(),
		"adapts":          ffmpeg.Adapts(),
		"color_modes":     ffmpeg.ColorModes(),
		"chromas":         ffmpeg.Chromas(),
		"rotations":       ffmpeg.Rotations(),
		"positions":       ffmpeg.WatermarkPositions(),
		"accels":          accels,
		"default_format":  h.ff.DefaultFormat(),
		"default_quality": h.ff.DefaultQuality(),
		"thumb_size":      h.ff.ThumbSize(),
		"worker":          h.cfg.App.Worker,
		"allow_upload":    h.cfg.App.AllowUpload,
		"max_upload":      h.cfg.App.MaxUploadMB,
		"output_dir":      h.media.RelPath(h.media.OutputDir()),
		"upload_dir":      h.media.RelPath(h.media.UploadDir()),
		"home_dir":        h.media.RelPath(h.media.HomeDir()),
		"home_is_root":    true,
	})
}

// accelReason 说明加速不可用的原因。
func accelReason(a ffmpeg.AccelSupport, verified int) string {
	if a.Available && verified > 0 {
		return ""
	}
	if !a.Available {
		return a.Reason
	}
	return "该加速下没有通过实测的图片编码器"
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && strings.Compare(s[j-1], s[j]) > 0; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}
