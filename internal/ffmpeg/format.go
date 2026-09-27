package ffmpeg

import (
	"fmt"
	"math"
	"path/filepath"
	"strconv"
	"strings"
)

// ==================== 输出格式 ====================

// QualityKind 质量参数的量纲类型。
const (
	// QualityNone 格式无质量概念（如 BMP / QOI）
	QualityNone = "none"
	// QualityQScale FFmpeg q:v 量纲（数值越小越清晰，如 mjpeg 2~31）
	QualityQScale = "qscale"
	// QualityPercent 百分比量纲（数值越大越清晰，如 libwebp 1~100）
	QualityPercent = "percent"
	// QualityCRF 恒定质量量纲（数值越小越清晰，如 AVIF 0~63）
	QualityCRF = "crf"
	// QualityCompression 压缩强度量纲（数值越大压得越狠，如 PNG 0~9）
	QualityCompression = "compression"
	// QualityColors 调色板颜色数量量纲（数值越大色彩越丰富，如 GIF 2~256）
	QualityColors = "colors"
)

// QualityModel 描述某种输出格式的质量参数如何映射到编码器取值。
// 前端统一使用 1~100 的滑块，由本结构换算为各编码器自己的量纲。
type QualityModel struct {
	// Kind 量纲类型
	Kind string `json:"kind"`
	// Label 滑块标签（PNG 之类的格式需要换成"压缩强度"等更贴切的措辞）
	Label string `json:"label"`
	// Hint 滑块说明文案
	Hint string `json:"hint"`
	// Min / Max 编码器取值范围
	Min int `json:"min"`
	Max int `json:"max"`
	// Default 该格式的推荐值
	Default int `json:"default"`
	// Invert 为 true 表示"界面数值越大 → 编码器数值越小"
	Invert bool `json:"invert"`
}

// EncoderValue 把 1~100 的界面质量值换算为编码器取值。
func (m QualityModel) EncoderValue(quality int) int {
	if m.Kind == QualityNone || m.Max <= m.Min {
		return 0
	}
	q := clampInt(quality, 1, 100)
	span := m.Max - m.Min
	offset := (q - 1) * span / 99
	if m.Invert {
		return m.Max - offset
	}
	return m.Min + offset
}

// QualityModel 的 EncoderValue 把界面质量换算为编码器取值，
// qualityT 则换算成"随画质单调递增"的 0~1 系数，用于体积预估。
func (m QualityModel) qualityT(quality int) float64 {
	if m.Kind == QualityNone || m.Max <= m.Min {
		return 0
	}
	v := m.EncoderValue(quality)
	if m.Invert {
		// 编码器取值越小画质越好，取反后才是"越清晰越大"
		return float64(m.Max-v) / float64(m.Max-m.Min)
	}
	return float64(v-m.Min) / float64(m.Max-m.Min)
}

// Format 描述一种输出图片格式。
type Format struct {
	// ID 格式标识（jpg / png / webp / avif …）
	ID string `json:"id"`
	// Name 展示名
	Name string `json:"name"`
	// Ext 输出文件扩展名（含点）
	Ext string `json:"ext"`
	// MimeType 浏览器与下载用 MIME
	MimeType string `json:"mime_type"`
	// SoftwareEncoders 软件编码器候选（按优先级）
	SoftwareEncoders []string
	// HWEncoders 硬件编码器映射：加速标识 → 编码器名
	HWEncoders map[string]string
	// AnimatedEncoder 动图编码器（可选，缺省用 SoftwareEncoders 第一个）
	AnimatedEncoder string
	// Quality 质量量纲映射
	Quality QualityModel
	// Muxer 显式指定的封装格式（留空则按扩展名自动选择）
	Muxer string
	// MuxerFallback 首选封装不可用时的备选
	MuxerFallback string
	// AnimatedMuxer 输出多帧时必须使用的封装（如 PNG 动图需 apng 封装）
	AnimatedMuxer string
	// Alpha 是否支持透明通道
	Alpha bool `json:"alpha"`
	// Lossless 是否无损格式
	Lossless bool `json:"lossless"`
	// Animated 是否支持多帧（动图）
	Animated bool `json:"animated"`
	// Note 展示用说明
	Note string `json:"note"`
	// DefaultQuality 该格式的推荐质量（1~100）
	DefaultQuality int `json:"default_quality"`
}

// formats 返回全部输出格式定义（顺序即前端展示顺序）。
func formats() []Format {
	return []Format{
		{
			ID: "jpg", Name: "JPEG", Ext: ".jpg", MimeType: "image/jpeg",
			SoftwareEncoders: []string{"mjpeg"},
			HWEncoders: map[string]string{
				AccelQSV:   "mjpeg_qsv",
				AccelVAAPI: "mjpeg_vaapi",
			},
			Quality: QualityModel{
				Kind: QualityQScale, Label: "画质", Hint: "数值越高细节越多、体积越大",
				Min: 2, Max: 31, Default: 23, Invert: true,
			},
			Note: "兼容性最好，几乎所有设备与软件都支持", DefaultQuality: 82,
		},
		{
			ID: "png", Name: "PNG", Ext: ".png", MimeType: "image/png",
			SoftwareEncoders: []string{"png"},
			AnimatedEncoder:  "apng",
			AnimatedMuxer:    "apng",
			Quality: QualityModel{
				Kind: QualityCompression, Label: "压缩强度", Hint: "PNG 无损，数值越大压得越狠（体积更小、速度更慢）",
				Min: 0, Max: 9, Default: 9,
			},
			Alpha: true, Lossless: true, Animated: true,
			Note: "无损、支持透明与动画（APNG）", DefaultQuality: 90,
		},
		{
			ID: "webp", Name: "WebP", Ext: ".webp", MimeType: "image/webp",
			SoftwareEncoders: []string{"libwebp"},
			AnimatedEncoder:  "libwebp_anim",
			// FFmpeg 没有 WebP 硬件编码器（libwebp 是纯软件实现），故不设 HWEncoders
			Quality: QualityModel{
				Kind: QualityPercent, Label: "画质", Hint: "100 为接近无损",
				Min: 1, Max: 100, Default: 80,
			},
			Alpha: true, Lossless: false, Animated: true,
			Note: "同画质体积约为 JPEG 的 60%~80%，网页首选", DefaultQuality: 80,
		},
		{
			ID: "avif", Name: "AVIF", Ext: ".avif", MimeType: "image/avif",
			SoftwareEncoders: []string{"libaom-av1", "libsvtav1"},
			HWEncoders: map[string]string{
				AccelNVENC: "av1_nvenc",
				AccelQSV:   "av1_qsv",
				AccelVAAPI: "av1_vaapi",
			},
			Quality: QualityModel{
				Kind: QualityCRF, Label: "画质", Hint: "AVIF 为 CRF 量纲，界面数值越大越清晰",
				Min: 0, Max: 63, Default: 30, Invert: true,
			},
			Muxer: "avif",
			Alpha: true,
			Note:  "压缩率最高，编码较慢，适合长期归档", DefaultQuality: 60,
		},
		{
			ID: "gif", Name: "GIF", Ext: ".gif", MimeType: "image/gif",
			SoftwareEncoders: []string{"gif"},
			Quality: QualityModel{
				Kind: QualityColors, Label: "颜色数量", Hint: "调色板颜色越多越细腻、体积越大",
				Min: 2, Max: 256, Default: 128,
			},
			Alpha: true, Animated: true,
			Note: "256 色调色板，支持动图", DefaultQuality: 70,
		},
		{
			ID: "heic", Name: "HEIC", Ext: ".heic", MimeType: "image/heic",
			SoftwareEncoders: []string{"libx265"},
			HWEncoders: map[string]string{
				AccelNVENC: "hevc_nvenc",
				AccelQSV:   "hevc_qsv",
				AccelVAAPI: "hevc_vaapi",
			},
			Quality: QualityModel{
				Kind: QualityCRF, Label: "画质", Hint: "HEVC 量纲，界面数值越大越清晰",
				Min: 0, Max: 51, Default: 24, Invert: true,
			},
			Muxer: "heic", MuxerFallback: "hevc",
			Note: "苹果设备原生支持，不支持透明通道", DefaultQuality: 70,
		},
		{
			ID: "jxl", Name: "JPEG XL", Ext: ".jxl", MimeType: "image/jxl",
			SoftwareEncoders: []string{"libjxl"},
			Quality: QualityModel{
				Kind: QualityPercent, Label: "画质", Hint: "接近无损，压缩率高于 WebP",
				Min: 1, Max: 100, Default: 85,
			},
			Alpha: true, Lossless: true,
			Note: "新一代格式，压缩率与画质俱佳（依赖 FFmpeg 编译 libjxl）", DefaultQuality: 85,
		},
		{
			ID: "jpeg2000", Name: "JPEG 2000", Ext: ".jp2", MimeType: "image/jp2",
			SoftwareEncoders: []string{"jpeg2000"},
			Quality: QualityModel{
				Kind: QualityQScale, Label: "压缩强度", Hint: "数值越大压得越狠",
				Min: 1, Max: 31, Default: 20, Invert: true,
			},
			Note: "医疗 / 印刷 / 遥感领域常用，支持 16 位与无损模式", DefaultQuality: 75,
		},
		{
			ID: "tiff", Name: "TIFF", Ext: ".tiff", MimeType: "image/tiff",
			SoftwareEncoders: []string{"libtiff", "tiff"},
			Quality: QualityModel{
				Kind: QualityCompression, Label: "压缩强度", Hint: "数值越大压得越狠",
				Min: 0, Max: 9, Default: 6,
			},
			Alpha: true, Lossless: true,
			Note: "印刷 / 归档常用，支持 16 位与 CMYK", DefaultQuality: 80,
		},
		{
			ID: "bmp", Name: "BMP", Ext: ".bmp", MimeType: "image/bmp",
			SoftwareEncoders: []string{"bmp"},
			Quality:          QualityModel{Kind: QualityNone, Label: "画质", Hint: "BMP 无损压缩，无质量参数"},
			Note:             "未压缩位图，体积大", DefaultQuality: 100,
		},
		{
			ID: "qoi", Name: "QOI", Ext: ".qoi", MimeType: "image/qoi",
			SoftwareEncoders: []string{"qoi"},
			Quality:          QualityModel{Kind: QualityNone, Label: "画质", Hint: "QOI 无损，无质量参数"},
			Alpha:            true, Lossless: true,
			Note: "极简无损格式，编码极快", DefaultQuality: 100,
		},
	}
}

// Formats 返回全部输出格式定义。
func Formats() []Format { return formats() }

// FormatByID 按标识查找输出格式。
func FormatByID(id string) (Format, bool) {
	id = strings.ToLower(strings.TrimSpace(id))
	for _, f := range formats() {
		if f.ID == id {
			return f, true
		}
	}
	return Format{}, false
}

// DefaultFormatID 返回配置指定的默认输出格式，非法时回退到 WebP。
func DefaultFormatID(id string) string {
	if _, ok := FormatByID(id); ok {
		return strings.ToLower(strings.TrimSpace(id))
	}
	return "webp"
}

// OutputMuxer 返回实际使用的封装格式名。
// 留空表示 image2（FFmpeg 默认的图片封装，能按扩展名写单个文件）；
// 输出多帧时改用 AnimatedMuxer（如 PNG 动图必须走 apng 封装才能写进单个文件）。
func (f Format) OutputMuxer(animated bool) string {
	if animated && f.AnimatedMuxer != "" {
		return f.AnimatedMuxer
	}
	if f.Muxer != "" {
		return f.Muxer
	}
	return "image2"
}

// MimeTypeForExt 由输出扩展名推导 MIME（预览与下载用）。
func MimeTypeForExt(ext string) string {
	ext = strings.ToLower(ext)
	for _, f := range formats() {
		if f.Ext == ext {
			return f.MimeType
		}
	}
	return ""
}

// MimeTypeForFormatID 由格式标识推导 MIME。
func MimeTypeForFormatID(id string) string {
	if f, ok := FormatByID(id); ok {
		return f.MimeType
	}
	return "application/octet-stream"
}

// ==================== 输入格式 ====================

// imageInputExts 可浏览 / 可转换的图片扩展名（小写，含点）。
// 这里刻意放宽：能否解码交给 FFmpeg 判断，界面只负责把候选文件列出来。
var imageInputExts = map[string]string{
	".jpg": "jpg", ".jpeg": "jpg", ".jpe": "jpg", ".jfif": "jpg", ".jfi": "jpg", ".jpg_large": "jpg",
	".png": "png", ".webp": "webp", ".gif": "gif",
	".bmp": "bmp", ".dib": "bmp", ".tiff": "tiff", ".tif": "tiff",
	".heic": "heic", ".heif": "heic", ".avif": "avif", ".avifs": "avif",
	".jxl": "jxl", ".ico": "bmp", ".cur": "bmp",
	".tga": "bmp", ".pcx": "bmp", ".ppm": "bmp", ".pgm": "bmp", ".pbm": "bmp", ".pnm": "bmp",
	".exr": "bmp", ".hdr": "bmp", ".dds": "bmp", ".sgi": "bmp", ".rgb": "bmp", ".rgba": "bmp",
	".jp2": "bmp", ".j2k": "bmp", ".jpf": "bmp", ".jpx": "bmp",
}

// IsImageFile 判断文件名是否属于可转换的图片扩展名。
func IsImageFile(name string) bool {
	_, ok := imageInputExts[strings.ToLower(filepath.Ext(name))]
	return ok
}

// FormatIDForInput 依据输入文件扩展名推测建议的输出格式（仅用于界面默认值提示）。
func FormatIDForInput(inputPath string) string {
	ext := strings.ToLower(filepath.Ext(inputPath))
	switch ext {
	case ".png", ".gif", ".tga", ".ico", ".cur":
		return "webp"
	case ".webp":
		return "jpg"
	case ".heic", ".heif", ".avif", ".jxl":
		return "jpg"
	case ".tif", ".tiff":
		return "png"
	default:
		return "webp"
	}
}

// ==================== 缩放 ====================

// ResizeMode 缩放方式。
type ResizeMode struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Label string `json:"label"`
}

// ResizeModes 返回可选的缩放方式。
func ResizeModes() []ResizeMode {
	return []ResizeMode{
		{ID: "keep", Name: "保持原始", Label: "不缩放"},
		{ID: "long_edge", Name: "长边", Label: "限制最长边"},
		{ID: "short_edge", Name: "短边", Label: "限制最短边"},
		{ID: "width", Name: "指定宽度", Label: "按宽度等比"},
		{ID: "height", Name: "指定高度", Label: "按高度等比"},
		{ID: "percent", Name: "百分比", Label: "按原始尺寸缩放"},
		{ID: "exact", Name: "精确尺寸", Label: "自定义 宽x高"},
	}
}

// ResizeModeByID 按标识查找缩放方式。
func ResizeModeByID(id string) (ResizeMode, bool) {
	id = strings.ToLower(strings.TrimSpace(id))
	for _, m := range ResizeModes() {
		if m.ID == id {
			return m, true
		}
	}
	return ResizeMode{}, false
}

// Adapt 目标尺寸自适应策略。
type Adapt struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Label string `json:"label"`
}

// Adapts 返回精确尺寸下的自适应策略。
func Adapts() []Adapt {
	return []Adapt{
		{ID: "fit", Name: "完整显示", Label: "保持比例，不足处留白"},
		{ID: "fill", Name: "填满裁剪", Label: "保持比例，居中裁掉溢出部分"},
		{ID: "stretch", Name: "强制拉伸", Label: "忽略比例，直接拉伸"},
	}
}

// AdaptByID 按标识查找自适应策略。
func AdaptByID(id string) (Adapt, bool) {
	id = strings.ToLower(strings.TrimSpace(id))
	for _, a := range Adapts() {
		if a.ID == id {
			return a, true
		}
	}
	return Adapt{}, false
}

// ParseCustomSize 解析 "1920x1080" 形式的精确尺寸。
func ParseCustomSize(s string) (int, int, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return 0, 0, fmt.Errorf("自定义尺寸不能为空")
	}
	sep := strings.IndexAny(s, "x*×")
	if sep <= 0 {
		return 0, 0, fmt.Errorf("自定义尺寸格式应为 宽x高，如 1920x1080")
	}
	w, err := strconv.Atoi(strings.TrimSpace(s[:sep]))
	if err != nil {
		return 0, 0, fmt.Errorf("宽度不是有效数字")
	}
	h, err := strconv.Atoi(strings.TrimSpace(s[sep+1:]))
	if err != nil {
		return 0, 0, fmt.Errorf("高度不是有效数字")
	}
	if w < 1 || h < 1 {
		return 0, 0, fmt.Errorf("宽高必须大于 0")
	}
	if w > 40000 || h > 40000 {
		return 0, 0, fmt.Errorf("宽高超出支持范围（最大 40000）")
	}
	return w, h, nil
}

// ==================== 旋转 / 翻转 ====================

// Rotation 旋转角度。
type Rotation struct {
	Value int    `json:"value"`
	Name  string `json:"name"`
}

// Rotations 返回可选的旋转角度。
func Rotations() []Rotation {
	return []Rotation{
		{Value: 0, Name: "不旋转"},
		{Value: 90, Name: "顺时针 90°"},
		{Value: 180, Name: "旋转 180°"},
		{Value: 270, Name: "顺时针 270°"},
	}
}

// NormalizeRotation 把任意角度折算到 0/90/180/270。
func NormalizeRotation(deg int) int {
	deg = ((deg % 360) + 360) % 360
	return (deg / 90) * 90
}

// ==================== 水印位置 ====================

// WatermarkPosition 水印位置。
type WatermarkPosition struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// WatermarkPositions 返回全部水印位置（展示顺序与九宫格一致）。
func WatermarkPositions() []WatermarkPosition {
	return []WatermarkPosition{
		{ID: "top_left", Name: "左上"}, {ID: "top", Name: "正上"}, {ID: "top_right", Name: "右上"},
		{ID: "left", Name: "正左"}, {ID: "center", Name: "正中"}, {ID: "right", Name: "正右"},
		{ID: "bottom_left", Name: "左下"}, {ID: "bottom", Name: "正下"}, {ID: "bottom_right", Name: "右下"},
	}
}

// WatermarkPositionByID 按标识查找位置，默认右下角。
func WatermarkPositionByID(id string) (WatermarkPosition, bool) {
	id = strings.ToLower(strings.TrimSpace(id))
	for _, p := range WatermarkPositions() {
		if p.ID == id {
			return p, true
		}
	}
	return WatermarkPositionByID("bottom_right")
}

// ==================== 颜色模式 ====================

// ColorMode 颜色模式。
type ColorMode struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Label string `json:"label"`
}

// ColorModes 返回可选的颜色模式。
func ColorModes() []ColorMode {
	return []ColorMode{
		{ID: "keep", Name: "保持原始", Label: "彩色 / 灰度 / 索引色原样保留"},
		{ID: "rgb", Name: "真彩色", Label: "24 位 RGB，不透明"},
		{ID: "grayscale", Name: "灰度", Label: "8 位灰度，体积显著变小"},
	}
}

// ColorModeByID 按标识查找颜色模式；未识别时回退"保持原始"。
func ColorModeByID(id string) ColorMode {
	id = strings.ToLower(strings.TrimSpace(id))
	for _, c := range ColorModes() {
		if c.ID == id {
			return c
		}
	}
	return ColorModes()[0]
}

// ChromaSubsampling 色度抽样方式。
type Chroma struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Label string `json:"label"`
}

// Chromas 返回可选的色度抽样方式。
func Chromas() []Chroma {
	return []Chroma{
		{ID: "444", Name: "4:4:4", Label: "不损失色彩细节，体积最大"},
		{ID: "422", Name: "4:2:2", Label: "折中，画质与体积兼顾"},
		{ID: "420", Name: "4:2:0", Label: "兼容性最好，体积最小"},
	}
}

// ChromaByID 按标识查找色度抽样方式；未识别时回退 4:2:0。
func ChromaByID(id string) Chroma {
	id = strings.TrimSpace(id)
	for _, c := range Chromas() {
		if c.ID == id {
			return c
		}
	}
	return Chromas()[2]
}

// ==================== 体积预估 ====================

// bitsPerPixel 估算指定格式 / 质量下每像素占用的比特数。
// 经验模型：用于给界面一个量级参考，不追求与真实编码器完全一致。
func bitsPerPixel(f Format, quality int, colorMode string) float64 {
	hasAlpha := f.Alpha && colorMode != "grayscale"
	// t 随画质单调递增（0=最小体积，1=最大体积），保证预估方向与手感一致
	t := f.Quality.qualityT(quality)
	var bpp float64
	switch f.Quality.Kind {
	case QualityQScale:
		// mjpeg：qscale 2(近乎无损)~31(极糊)
		bpp = 0.02 + 2.0*math.Pow(t, 1.7)
	case QualityPercent:
		bpp = 0.01 + 2.2*math.Pow(t, 1.5)
	case QualityCRF:
		bpp = 0.005 + 2.0*math.Pow(t, 1.6)
	case QualityCompression:
		lvl := float64(f.Quality.EncoderValue(quality))
		// 无损格式：压缩强度越高体积越小
		bpp = 2.9 - 0.19*lvl
	case QualityColors:
		colors := float64(f.Quality.EncoderValue(quality))
		// GIF：颜色数越多索引越大
		bpp = 0.35 + 3.2*math.Log2(math.Max(colors, 2))/8
	case QualityNone:
		// BMP / QOI 等
		if f.Lossless {
			bpp = 3.0
		} else {
			bpp = 3.0
		}
	}
	// 灰度图每像素信息量大幅下降
	if colorMode == "grayscale" {
		bpp *= 0.35
	}
	if hasAlpha {
		bpp += 0.35
	}
	return math.Max(bpp, 0.004)
}

// EstimateBytes 预估输出文件体积（字节）。w/h 为输出尺寸，0 表示未知。
func EstimateBytes(f Format, quality int, w, h int, colorMode string) int64 {
	if w <= 0 || h <= 0 {
		return 0
	}
	bpp := bitsPerPixel(f, quality, colorMode)
	// 极小图有固定的文件头开销
	size := float64(w*h) * bpp / 8
	minSize := 800.0
	if hasAlpha(f) {
		minSize = 1200
	}
	if size < minSize {
		size = minSize
	}
	const maxSize = 512.0 * 1024 * 1024
	if size > maxSize {
		size = maxSize
	}
	return int64(size)
}

// ==================== 工具函数 ====================

// hasAlpha 报告该格式是否可能带透明通道。
func hasAlpha(f Format) bool { return f.Alpha }

// clampInt 把数值收敛到 [min, max] 区间。
func clampInt(v, min, max int) int {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}
