// Package ffmpeg 封装对 FFmpeg / FFprobe 的调用，提供图片探测、编码器与封装
// 能力探测、图片格式转换、缩略图生成等能力。FFmpeg 通过内置路径或配置指定，
// 调用方需保证环境中存在 ffmpeg / ffprobe 可执行文件。
package ffmpeg

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// DetectError 表示未找到可执行文件或当前环境无法运行 FFmpeg。
type DetectError struct{ bin string }

func (e *DetectError) Error() string {
	return fmt.Sprintf("未找到可执行文件 %q，请安装 ffmpeg 或在配置中指定 ffmpeg.path / ffmpeg.ffprobe_path", e.bin)
}

// Client FFmpeg 调用客户端。
type Client struct {
	ffmpegBin   string
	ffprobeBin  string
	threads     int
	format      string
	quality     int
	accel       string
	vaapiDevice string
	hwDecode    bool
	thumbSize   int
	probeTO     time.Duration

	capOnce sync.Once
	caps    *Capabilities
}

// Options FFmpeg 客户端配置项。
type Options struct {
	FFmpegBin   string
	FFprobeBin  string
	Threads     int
	Format      string
	Quality     int
	Accel       string
	VAAPIDevice string
	HWDecode    bool
	ThumbSize   int
	ProbeTO     int
}

// New 创建 FFmpeg 客户端，并校验可执行文件存在。
func New(opts Options) (*Client, error) {
	ffmpegBin := opts.FFmpegBin
	if ffmpegBin == "" {
		ffmpegBin = "ffmpeg"
	}
	ffprobeBin := opts.FFprobeBin
	if ffprobeBin == "" {
		ffprobeBin = "ffprobe"
	}
	if _, err := exec.LookPath(ffmpegBin); err != nil {
		return nil, &DetectError{bin: ffmpegBin}
	}
	if _, err := exec.LookPath(ffprobeBin); err != nil {
		return nil, &DetectError{bin: ffprobeBin}
	}
	quality := opts.Quality
	if quality <= 0 {
		quality = 82
	}
	accel := strings.ToLower(strings.TrimSpace(opts.Accel))
	if accel == "" {
		accel = AccelAuto
	}
	device := opts.VAAPIDevice
	if device == "" {
		device = "/dev/dri/renderD128"
	}
	thumb := opts.ThumbSize
	if thumb <= 0 {
		thumb = 512
	}
	probeTO := time.Duration(opts.ProbeTO) * time.Second
	if probeTO <= 0 {
		probeTO = 20 * time.Second
	}
	return &Client{
		ffmpegBin:   ffmpegBin,
		ffprobeBin:  ffprobeBin,
		threads:     opts.Threads,
		format:      DefaultFormatID(opts.Format),
		quality:     quality,
		accel:       accel,
		vaapiDevice: device,
		hwDecode:    opts.HWDecode,
		thumbSize:   thumb,
		probeTO:     probeTO,
	}, nil
}

// FFmpegBin 返回 ffmpeg 可执行文件路径。
func (c *Client) FFmpegBin() string { return c.ffmpegBin }

// FFprobeBin 返回 ffprobe 可执行文件路径。
func (c *Client) FFprobeBin() string { return c.ffprobeBin }

// DefaultThreads 返回客户端默认线程配置（0 表示交由 ffmpeg 自动探测）。
func (c *Client) DefaultThreads() int { return c.threads }

// DefaultAccel 返回客户端默认硬件加速策略。
func (c *Client) DefaultAccel() string { return c.accel }

// ThumbSize 返回缩略图最长边（像素）。
func (c *Client) ThumbSize() int { return c.thumbSize }

// DefaultFormat 返回客户端默认输出格式标识。
func (c *Client) DefaultFormat() string { return c.format }

// DefaultQuality 返回客户端默认质量值（1~100）。
func (c *Client) DefaultQuality() int { return c.quality }

// ==================== 图片探测 ====================

// ImageInfo 图片探测结果。
type ImageInfo struct {
	Path string `json:"path"`
	Name string `json:"name"`
	// Size 文件体积（字节）
	Size int64 `json:"size"`
	// FormatName FFmpeg 识别的容器 / 编码名（如 png_pipe、image2）
	FormatName string `json:"format_name"`
	Width      int    `json:"width"`
	Height     int    `json:"height"`
	// PixFmt 像素格式（如 rgb24、yuvj420p、rgba）
	PixFmt string `json:"pix_fmt"`
	// BitDepth 单通道位深
	BitDepth int `json:"bit_depth"`
	// ColorSpace / ColorRange / ColorPrimaries 色彩描述（可能为空）
	ColorSpace     string `json:"color_space,omitempty"`
	ColorRange     string `json:"color_range,omitempty"`
	ColorPrimaries string `json:"color_primaries,omitempty"`
	// Duration 单帧时长（动图有效）
	Duration float64 `json:"duration"`
	// Frames 帧数（0 表示未知）
	Frames int `json:"frames"`
	// Animated 是否为多帧图片（动图）
	Animated bool `json:"animated"`
	// Alpha 是否带透明通道
	Alpha bool `json:"alpha"`
	// Orientation EXIF 旋转角度（0 / 90 / 180 / 270）
	Orientation int `json:"orientation"`
	// HasMetadata 是否携带元数据（EXIF / IPTC 等）
	HasMetadata bool `json:"has_metadata"`
	// BitRate 码率（部分容器可给出）
	BitRate int64 `json:"bit_rate"`
	// MimeType 由扩展名推导
	MimeType string `json:"mime_type"`

	// Aspect 宽高比
	Aspect float64 `json:"aspect"`
	// MegaPixels 百万像素数
	MegaPixels float64 `json:"megapixels"`
	// OrientationLabel 画面方向：横图 / 竖图 / 方形
	OrientationLabel string `json:"orientation_label"`
}

type ffprobeFormat struct {
	Duration   string `json:"duration"`
	Size       string `json:"size"`
	BitRate    string `json:"bit_rate"`
	FormatName string `json:"format_name"`
}

type ffprobeSideData struct {
	SideDataType string `json:"side_data_type"`
	Rotation     *int   `json:"rotation"`
}

type ffprobeStream struct {
	Index            int               `json:"index"`
	CodecType        string            `json:"codec_type"`
	CodecName        string            `json:"codec_name"`
	Width            int               `json:"width"`
	Height           int               `json:"height"`
	Duration         string            `json:"duration"`
	BitRate          string            `json:"bit_rate"`
	NBFrames         string            `json:"nb_frames"`
	PixFmt           string            `json:"pix_fmt"`
	BitsPerRawSample string            `json:"bits_per_raw_sample"`
	SampleAspect     string            `json:"sample_aspect_ratio"`
	ColorSpace       string            `json:"color_space"`
	ColorRange       string            `json:"color_range"`
	ColorPrimaries   string            `json:"color_primaries"`
	Disposition      map[string]int    `json:"disposition"`
	SideDataList     []ffprobeSideData `json:"side_data_list"`
	Tags             map[string]string `json:"tags"`
}

type ffprobeOutput struct {
	Format  ffprobeFormat   `json:"format"`
	Streams []ffprobeStream `json:"streams"`
}

// Probe 使用 ffprobe 探测图片信息（宽高、像素格式、色彩、EXIF 方向等）。
func (c *Client) Probe(ctx context.Context, path string) (*ImageInfo, error) {
	probeCtx, cancel := context.WithTimeout(ctx, c.probeTO)
	defer cancel()

	args := []string{
		"-v", "error",
		"-print_format", "json",
		"-show_format",
		"-show_streams",
		path,
	}
	cmd := exec.CommandContext(probeCtx, c.ffprobeBin, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("ffprobe 探测 %s 失败: %s", path, msg)
	}

	var out ffprobeOutput
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		return nil, fmt.Errorf("解析 ffprobe 输出失败: %w", err)
	}

	info := &ImageInfo{Path: path, Name: filepath.Base(path)}
	info.Duration = parseFloat(out.Format.Duration)
	info.Size = parseInt64(out.Format.Size)
	info.BitRate = parseInt64(out.Format.BitRate)
	info.FormatName = out.Format.FormatName
	info.MimeType = mimeForInputExt(filepath.Ext(path))

	for _, s := range out.Streams {
		if s.CodecType != "video" && s.CodecType != "image" {
			continue
		}
		// 内嵌缩略图等 attached_pic 流排在前面时优先取主图
		if info.Width == 0 || s.Disposition["attached_pic"] != 1 {
			if info.Width != 0 && s.Disposition["attached_pic"] == 1 {
				continue
			}
			info.Width = s.Width
			info.Height = s.Height
			info.PixFmt = s.PixFmt
			info.ColorSpace = s.ColorSpace
			info.ColorRange = s.ColorRange
			info.ColorPrimaries = s.ColorPrimaries
			info.BitDepth = int(parseInt64(s.BitsPerRawSample))
			if d := parseFloat(s.Duration); d > info.Duration {
				info.Duration = d
			}
			if n := parseInt64(s.NBFrames); n > 0 {
				info.Frames = int(n)
			}
			if o := orientationOf(s); o != 0 {
				info.Orientation = o
			}
			if len(s.Tags) > 0 {
				info.HasMetadata = true
			}
		}
		if len(s.SideDataList) > 0 {
			info.HasMetadata = true
		}
		break
	}

	if info.Width == 0 {
		return nil, fmt.Errorf("%s 中没有可识别的图像流", filepath.Base(path))
	}
	if st, err := os.Stat(path); err == nil {
		if info.Size <= 0 {
			info.Size = st.Size()
		}
	}
	if info.Frames == 0 {
		// GIF 等容器给不出帧数时，用时长估算（默认帧率 25）
		if info.Duration > 0.05 {
			info.Frames = int(info.Duration*25 + 0.5)
		} else {
			info.Frames = 1
		}
	}
	info.Animated = info.Frames > 1
	info.Alpha = pixFmtHasAlpha(info.PixFmt)
	if info.BitDepth == 0 {
		info.BitDepth = 8
	}
	if info.Duration > 0 && info.BitRate <= 0 && info.Size > 0 {
		info.BitRate = int64(float64(info.Size) * 8 / info.Duration)
	}

	// 派生字段
	info.Aspect = 1
	if info.Height > 0 {
		info.Aspect = float64(info.Width) / float64(info.Height)
	}
	info.MegaPixels = float64(info.Width) * float64(info.Height) / 1e6
	info.OrientationLabel = orientationLabel(info.Width, info.Height, info.Orientation)
	return info, nil
}

// orientationOf 从 side_data / tags 中解析 EXIF 旋转角度。
func orientationOf(s ffprobeStream) int {
	raw := 0
	for _, sd := range s.SideDataList {
		if sd.Rotation != nil {
			raw = *sd.Rotation
		}
	}
	if raw == 0 {
		if v, err := strconv.Atoi(strings.TrimSpace(s.Tags["rotate"])); err == nil {
			raw = v
		}
	}
	if raw == 0 {
		if v, err := strconv.Atoi(strings.TrimSpace(s.Tags["orientation"])); err == nil {
			// EXIF Orientation 1~8：5~8 涉及镜像，这里只处理旋转部分
			switch v {
			case 3, 4:
				raw = 180
			case 5, 6:
				raw = -90
			case 7, 8:
				raw = 90
			}
		}
	}
	deg := ((raw % 360) + 360) % 360
	return (deg / 90) * 90
}

// pixFmtHasAlpha 判断像素格式是否带透明通道。
func pixFmtHasAlpha(pf string) bool {
	pf = strings.ToLower(strings.TrimSpace(pf))
	if pf == "" {
		return false
	}
	switch {
	case strings.HasPrefix(pf, "yuva"), strings.HasPrefix(pf, "ya8"),
		pf == "rgba", pf == "bgra", pf == "argb", pf == "abgr", pf == "raa",
		strings.HasPrefix(pf, "pal8a"):
		return true
	}
	return false
}

// orientationLabel 生成画面方向描述（EXIF 方向以显示效果为准）。
func orientationLabel(w, h, orientation int) string {
	if orientation == 90 || orientation == 270 {
		w, h = h, w
	}
	switch {
	case w == h:
		return "方形"
	case w > h:
		return "横图"
	default:
		return "竖图"
	}
}

// EnsureFFmpegAvailable 供启动时快速自检，返回检测到的版本字符串。
func (c *Client) EnsureFFmpegAvailable(ctx context.Context) (string, error) {
	out, err := exec.CommandContext(ctx, c.ffmpegBin, "-version").Output()
	if err != nil {
		return "", err
	}
	return firstLine(string(out)), nil
}

// EnsureFFprobeAvailable 供启动时快速自检 FFprobe。
func (c *Client) EnsureFFprobeAvailable(ctx context.Context) error {
	if _, err := exec.CommandContext(ctx, c.ffprobeBin, "-version").Output(); err != nil {
		return err
	}
	return nil
}

// ==================== 硬件加速标识 ====================

// Accel 硬件加速标识。
const (
	AccelNone  = "none"
	AccelAuto  = "auto"
	AccelNVENC = "nvenc"
	AccelQSV   = "qsv"
	AccelVAAPI = "vaapi"
)

// AccelNames 返回全部硬件加速标识（展示顺序）。
func AccelNames() []string {
	return []string{AccelNone, AccelNVENC, AccelQSV, AccelVAAPI}
}

// AccelSupport 描述一种硬件加速在本机的可用性。
type AccelSupport struct {
	// ID 加速标识：none / nvenc / qsv / vaapi
	ID string `json:"id"`
	// Name 中文展示名
	Name string `json:"name"`
	// Available 是否可用（编码器存在且设备节点存在）
	Available bool `json:"available"`
	// Reason 不可用原因（可用时为空）
	Reason string `json:"reason,omitempty"`
	// Device 设备节点（nvenc / vaapi 有值）
	Device string `json:"device,omitempty"`
	// Formats 该加速下可用的图片格式 ID
	Formats []string `json:"formats"`
	// Encoders 检测到的编码器名
	Encoders []string `json:"encoders"`
}

// FormatSupport 描述一种输出格式在本机的可用性。
type FormatSupport struct {
	Format Format `json:"format"`
	// Available 是否可用
	Available bool `json:"available"`
	// Reason 不可用原因
	Reason string `json:"reason,omitempty"`
	// Encoders 本机可用的软件编码器名
	Encoders []string `json:"encoders"`
	// Muxer 实际使用的封装格式
	Muxer string `json:"muxer,omitempty"`
	// AnimatedMuxer 输出多帧时使用的封装
	AnimatedMuxer string `json:"animated_muxer,omitempty"`
	// AnimatedAvailable 动图输出是否可用
	AnimatedAvailable bool `json:"animated_available"`
}

// HWFormat 记录某种格式经真实编码自检通过的硬件编码器。
type HWFormat struct {
	// Accel 加速标识
	Accel string `json:"accel"`
	// Encoder 自检通过的编码器名
	Encoder string `json:"encoder"`
}

// Capabilities FFmpeg 环境能力描述（前端据此渲染格式列表）。
type Capabilities struct {
	// FFmpeg ffmpeg 版本首行
	FFmpeg string `json:"ffmpeg"`
	// FFprobe ffprobe 版本首行
	FFprobe string `json:"ffprobe"`
	// FfmpegPath ffmpeg 可执行文件路径
	FfmpegPath string `json:"ffmpeg_path"`
	// Encoders 本机全部可用视频编码器
	Encoders []string `json:"encoders"`
	// Muxers 本机全部可用封装格式
	Muxers []string `json:"muxers"`
	// Formats 各输出格式的可用性
	Formats []FormatSupport `json:"formats"`
	// HWAccel 硬件加速可用性列表
	HWAccel []AccelSupport `json:"hw_accel"`
	// HWFormats 格式标识 → 经自检的硬件编码器
	HWFormats map[string]HWFormat `json:"hw_formats"`
	// HWDecode 解码器 ID 列表
	HWDecode []string `json:"hw_decode"`
	// Detected 探测时间
	Detected string `json:"detected"`
}

// ResolveMuxer 返回该格式实际可用的封装名（结合能力探测结果）。
func (c *Capabilities) ResolveMuxer(f Format, animated bool) string {
	if sup := c.FormatSupportFor(f.ID); sup != nil {
		if animated && sup.AnimatedMuxer != "" {
			return sup.AnimatedMuxer
		}
		if animated && sup.AnimatedMuxer == "" && f.AnimatedMuxer != "" {
			return ""
		}
		if sup.Muxer != "" {
			return sup.Muxer
		}
	}
	return f.OutputMuxer(animated)
}

// Detect 探测本机 FFmpeg 能力（编码器、封装、硬件加速）。结果在进程内缓存。
func (c *Client) Detect(ctx context.Context) *Capabilities {
	c.capOnce.Do(func() { c.caps = c.detect(ctx) })
	return c.caps
}

// detect 执行实际探测（无缓存）。
func (c *Client) detect(ctx context.Context) *Capabilities {
	caps := &Capabilities{
		FfmpegPath: c.ffmpegBin,
		Encoders:   []string{},
		Muxers:     []string{},
		Formats:    []FormatSupport{},
		HWAccel:    []AccelSupport{},
		HWFormats:  map[string]HWFormat{},
		HWDecode:   []string{},
		Detected:   time.Now().Format(time.RFC3339),
	}
	if out, err := runLimited(ctx, 20*time.Second, c.ffmpegBin, "-version"); err == nil {
		caps.FFmpeg = firstLine(out)
	}
	if out, err := runLimited(ctx, 20*time.Second, c.ffprobeBin, "-version"); err == nil {
		caps.FFprobe = firstLine(out)
	}

	encoders := map[string]bool{}
	if out, err := runLimited(ctx, 30*time.Second, c.ffmpegBin, "-hide_banner", "-encoders"); err == nil {
		for _, name := range parseCodecList(out, 'V') {
			encoders[name] = true
		}
	}
	for name := range encoders {
		caps.Encoders = append(caps.Encoders, name)
	}
	sort.Strings(caps.Encoders)

	muxers := map[string]bool{}
	if out, err := runLimited(ctx, 30*time.Second, c.ffmpegBin, "-hide_banner", "-muxers"); err == nil {
		for _, name := range parseMuxerList(out) {
			muxers[name] = true
		}
	}
	for name := range muxers {
		caps.Muxers = append(caps.Muxers, name)
	}
	sort.Strings(caps.Muxers)

	// 各格式可用性
	for _, f := range formats() {
		fs := FormatSupport{Format: f, Encoders: []string{}}
		for _, enc := range f.SoftwareEncoders {
			if encoders[enc] {
				fs.Encoders = append(fs.Encoders, enc)
			}
		}
		fs.Muxer = resolveMuxer(f, muxers)
		if f.AnimatedMuxer != "" && muxers[f.AnimatedMuxer] {
			fs.AnimatedMuxer = f.AnimatedMuxer
		}
		if f.AnimatedEncoder != "" && encoders[f.AnimatedEncoder] &&
			(f.AnimatedMuxer == "" || fs.AnimatedMuxer != "") {
			fs.AnimatedAvailable = true
		}
		if fs.Muxer == "" {
			fs.Reason = "当前 FFmpeg 缺少 " + f.Muxer + " 封装器"
		}
		if len(fs.Encoders) == 0 {
			reason := "当前 FFmpeg 未编译该格式的编码器（" + strings.Join(f.SoftwareEncoders, " / ") + "）"
			if fs.Reason == "" {
				fs.Reason = reason
			} else {
				fs.Reason += "；" + reason
			}
		}
		if fs.Reason == "" {
			fs.Available = true
		}
		caps.Formats = append(caps.Formats, fs)
	}

	// 硬件解码器
	if out, err := runLimited(ctx, 30*time.Second, c.ffmpegBin, "-hide_banner", "-hwaccels"); err == nil {
		for _, line := range strings.Split(out, "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "Hardware acceleration") {
				continue
			}
			caps.HWDecode = append(caps.HWDecode, line)
		}
	}

	// 硬件加速可用性：逐个格式做真实编码自检。
	// 同一个加速下不同格式的编码器支持度差别很大（QSV 能编 MJPEG 却未必能编 AV1），
	// 只验证一个编码器就整体判定可用，会把任务导向必然失败的路径。
	device := c.findRenderDevice()
	nvidia := findNvidiaDevice()
	accels := []struct {
		id, name, dev string
	}{
		{AccelNone, "软件编码（CPU）", ""},
		{AccelNVENC, "NVIDIA NVENC", nvidia},
		{AccelQSV, "Intel QSV", device},
		{AccelVAAPI, "VAAPI（Intel/AMD）", device},
	}
	for _, accel := range accels {
		sup := AccelSupport{ID: accel.id, Name: accel.name, Device: accel.dev, Encoders: []string{}, Formats: []string{}}
		switch {
		case accel.id == AccelNone:
			sup.Available = true
		case accel.dev == "":
			if accel.id == AccelNVENC {
				sup.Reason = "未检测到 NVIDIA 设备（/dev/nvidia*）"
			} else {
				sup.Reason = "未检测到 /dev/dri 渲染设备"
			}
		default:
			var failures []string
			for _, f := range formats() {
				enc, ok := f.HWEncoders[accel.id]
				if !ok || !encoders[enc] {
					continue
				}
				if resolveMuxer(f, muxers) == "" {
					continue
				}
				if err := c.probeAccel(ctx, accel.id, enc, accel.dev); err != nil {
					failures = append(failures, f.Name+"："+err.Error())
					continue
				}
				sup.Encoders = append(sup.Encoders, enc)
				sup.Formats = append(sup.Formats, f.ID)
				if _, taken := caps.HWFormats[f.ID]; !taken {
					caps.HWFormats[f.ID] = HWFormat{Accel: accel.id, Encoder: enc}
				}
			}
			if len(sup.Formats) == 0 {
				sup.Reason = "该加速无法编码任何图片格式"
				if len(failures) > 0 {
					sup.Reason += "（" + summarizeErr(strings.Join(failures, "；")) + "）"
				}
			} else {
				sup.Available = true
			}
		}
		caps.HWAccel = append(caps.HWAccel, sup)
	}

	return caps
}

// resolveMuxer 依据实际可用的封装列表选定封装名，返回空串表示不可用。
func resolveMuxer(f Format, muxers map[string]bool) string {
	if f.Muxer == "" {
		// 留空即 image2（FFmpeg 必备的图片封装）
		return "image2"
	}
	if muxers[f.Muxer] {
		return f.Muxer
	}
	if f.MuxerFallback != "" && muxers[f.MuxerFallback] {
		return f.MuxerFallback
	}
	return ""
}

// probeAccel 用一张测试图验证硬件编码器是否真的可用。
// 编译支持 + 设备节点存在并不等于可用：容器设备映射错误、驱动缺失时，
// 真实调用仍会失败。返回压缩后的首行错误信息。
func (c *Client) probeAccel(ctx context.Context, accel, encoder, device string) error {
	args := []string{"-hide_banner", "-loglevel", "error", "-f", "lavfi",
		"-i", "color=c=black:s=128x128:d=1", "-frames:v", "1",
		"-c:v", encoder}
	switch accel {
	case AccelVAAPI:
		args = append(args, "-vaapi_device", device, "-vf", "format=nv12,hwupload")
	case AccelQSV:
		args = append(args, "-vf", "format=nv12")
	}
	args = append(args, "-f", "null", "-")

	out, err := runLimited(ctx, 15*time.Second, c.ffmpegBin, args...)
	if err == nil {
		return nil
	}
	return errors.New(summarizeErr(out))
}

// findRenderDevice 查找第一个可用的 /dev/dri/renderD* 设备节点。
// 优先返回配置指定的设备，不存在时回退到系统首个渲染节点。
func (c *Client) findRenderDevice() string {
	if c.vaapiDevice != "" {
		if _, err := os.Stat(c.vaapiDevice); err == nil {
			return c.vaapiDevice
		}
	}
	if matches, err := filepath.Glob("/dev/dri/renderD*"); err == nil && len(matches) > 0 {
		return matches[0]
	}
	return ""
}

// findNvidiaDevice 查找 NVIDIA 设备节点。
func findNvidiaDevice() string {
	for _, p := range []string{"/dev/nvidiactl", "/dev/nvidia0", "/dev/nvidia"} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// parseCodecList 解析 `ffmpeg -encoders` / `-decoders` 输出，返回指定流类型的名称。
// 输出形如 " V....D h264_nvenc           NVIDIA NVENC H.264 encoder"：
// 首列固定为 6 个能力标志位，其首位字符即流类型（V 视频 / A 音频 / S 字幕）。
func parseCodecList(out string, streamType byte) []string {
	var names []string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if len(line) < 8 {
			continue
		}
		fields := strings.Fields(line)
		// 至少要有「能力标志 + 名称 + 描述」三段，缺描述的是表头或异常行
		if len(fields) < 3 {
			continue
		}
		flags := fields[0]
		if len(flags) != 6 || flags[0] != streamType {
			continue
		}
		name := fields[1]
		// 过滤图例行（如 "V..... = Video"）与异常行
		if strings.ContainsAny(name, "=,;") || strings.HasPrefix(name, "-") {
			continue
		}
		names = append(names, name)
	}
	return names
}

// parseMuxerList 解析 `ffmpeg -muxers` 输出，返回可用的封装格式名。
// 输出形如 "  E  avif            AVIF"：标志列 1~2 个字符，首位为 E 表示可封装。
// 注意标志列与名称之间是两个空格，TrimSpace 之后单字符标志行会以 "E " 开头，
// 因此不能拿它当过滤条件，否则会漏掉绝大多数封装格式。
func parseMuxerList(out string) []string {
	var names []string
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		// 至少要有「能力标志 + 名称 + 描述」三段，缺描述的是表头或异常行
		if len(fields) < 3 {
			continue
		}
		flags := fields[0]
		if len(flags) == 0 || len(flags) > 2 || flags[0] != 'E' {
			continue
		}
		name := fields[1]
		if strings.ContainsAny(name, "=,;") || strings.HasPrefix(name, "-") {
			continue
		}
		names = append(names, name)
	}
	return names
}

// runLimited 带超时执行命令并限制输出大小。
func runLimited(ctx context.Context, timeout time.Duration, name string, args ...string) (string, error) {
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(runCtx, name, args...)
	buf := &limitedBuffer{max: 1 << 20}
	cmd.Stdout = buf
	cmd.Stderr = buf
	if err := cmd.Run(); err != nil {
		return buf.String(), err
	}
	return buf.String(), nil
}

func firstLine(s string) string {
	return strings.TrimSpace(strings.SplitN(strings.TrimSpace(s), "\n", 2)[0])
}

func parseFloat(s string) float64 {
	s = strings.TrimSpace(s)
	if s == "" || s == "N/A" {
		return 0
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return f
}

func parseInt64(s string) int64 {
	s = strings.TrimSpace(s)
	if s == "" || s == "N/A" {
		return 0
	}
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		return n
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return int64(f)
	}
	return 0
}

// mimeForInputExt 由输入扩展名推导 MIME（预览用，识别不到时留空由浏览器猜测）。
func mimeForInputExt(ext string) string {
	switch strings.ToLower(ext) {
	case ".jpg", ".jpeg", ".jpe", ".jfif", ".jfi":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".webp":
		return "image/webp"
	case ".gif":
		return "image/gif"
	case ".bmp", ".dib":
		return "image/bmp"
	case ".tif", ".tiff":
		return "image/tiff"
	case ".heic", ".heif":
		return "image/heic"
	case ".avif", ".avifs":
		return "image/avif"
	case ".jxl":
		return "image/jxl"
	case ".svg":
		return "image/svg+xml"
	case ".ico":
		return "image/x-icon"
	case ".qoi":
		return "image/qoi"
	default:
		return ""
	}
}

// limitedBuffer 有限大小的输出缓冲，避免异常输出把内存撑爆。
type limitedBuffer struct {
	buf []byte
	max int
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if len(b.buf) < b.max {
		room := b.max - len(b.buf)
		if len(p) > room {
			p = p[:room]
		}
		b.buf = append(b.buf, p...)
	}
	return len(p), nil
}

func (b *limitedBuffer) String() string { return strings.TrimSpace(string(b.buf)) }
