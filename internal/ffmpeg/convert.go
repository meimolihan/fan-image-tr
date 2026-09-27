package ffmpeg

import (
	"bufio"
	"context"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ==================== 转换参数 ====================

// WatermarkOptions 图片水印参数。
type WatermarkOptions struct {
	// Enabled 是否叠加水印
	Enabled bool `json:"enabled"`
	// Path 水印图片绝对路径
	Path string `json:"path"`
	// Position 位置（见 WatermarkPositions）
	Position string `json:"position"`
	// Margin 边距（像素，按输出尺寸计）
	Margin int `json:"margin"`
	// Width 水印宽度（像素，0 表示保持原始宽度）
	Width int `json:"width"`
	// Opacity 不透明度 1~100
	Opacity int `json:"opacity"`
}

// ConvertOptions 一次图片转换的完整参数。
// 前端提交的就是这个结构，服务端会先经 Clamp 归一化再交给 FFmpeg。
type ConvertOptions struct {
	// Format 输出格式标识
	Format string `json:"format"`
	// Quality 界面统一的 1~100 质量值
	Quality int `json:"quality"`
	// ColorMode keep / rgb / grayscale
	ColorMode string `json:"color_mode"`
	// Chroma 色度抽样 444 / 422 / 420
	Chroma string `json:"chroma"`
	// AutoOrient 按 EXIF 自动摆正
	AutoOrient bool `json:"auto_orient"`
	// Rotate 额外旋转角度 0/90/180/270
	Rotate int `json:"rotate"`
	// FlipH 水平翻转
	FlipH bool `json:"flip_h"`
	// FlipV 垂直翻转
	FlipV bool `json:"flip_v"`
	// Resize 缩放方式（见 ResizeModes）
	Resize string `json:"resize"`
	// Size 缩放数值（边长 / 百分比）
	Size int `json:"size"`
	// CustomWidth / CustomHeight 精确尺寸模式的目标宽高
	CustomWidth  int `json:"custom_width"`
	CustomHeight int `json:"custom_height"`
	// Adapt 精确尺寸下的自适应策略（见 Adapts）
	Adapt string `json:"adapt"`
	// AllowUpscale 允许放大到超过原始尺寸
	AllowUpscale bool `json:"allow_upscale"`
	// Brightness / Contrast / Saturation 色调调整，范围 -100~100
	Brightness int `json:"brightness"`
	Contrast   int `json:"contrast"`
	Saturation int `json:"saturation"`
	// Sharpen 锐化强度 0~100
	Sharpen int `json:"sharpen"`
	// Blur 高斯模糊强度 0~100
	Blur int `json:"blur"`
	// Background 透明图合成到不透明格式时的背景色（默认白）
	Background string `json:"background"`
	// Animated 保留多帧（动图）
	Animated bool `json:"animated"`
	// StripMetadata 移除 EXIF 等元数据
	StripMetadata bool `json:"strip_metadata"`
	// Watermark 水印设置
	Watermark WatermarkOptions `json:"watermark"`

	// ---- 以下字段由服务端填充，前端无需提交 ----
	// SourceWidth / SourceHeight 源图尺寸（0 表示未知）
	SourceWidth  int `json:"-"`
	SourceHeight int `json:"-"`
	// SourceAlpha 源图是否带透明通道
	SourceAlpha bool `json:"-"`
	// ScaleWidth / ScaleHeight 缩放滤镜目标尺寸
	ScaleWidth  int `json:"-"`
	ScaleHeight int `json:"-"`
	// OutputWidth / OutputHeight 最终输出尺寸（裁剪后）
	OutputWidth  int `json:"-"`
	OutputHeight int `json:"-"`
	// Threads FFmpeg 线程数（0 表示自动）
	Threads int `json:"-"`
	// Accel 硬件加速策略
	Accel string `json:"-"`
	// VAAPIDevice VAAPI 设备节点
	VAAPIDevice string `json:"-"`
	// HWDecode 尝试硬件解码
	HWDecode bool `json:"-"`
}

// DefaultOptions 返回一组推荐的默认转换参数。
func DefaultOptions(format string) ConvertOptions {
	return ConvertOptions{
		Format:     DefaultFormatID(format),
		Quality:    -1,
		ColorMode:  "keep",
		Chroma:     "420",
		AutoOrient: true,
		Resize:     "keep",
		Adapt:      "fit",
		Background: "white",
		Threads:    0,
		Accel:      AccelAuto,
		Watermark:  WatermarkOptions{Position: "bottom_right", Margin: 24, Opacity: 60},
	}
}

// ClampOptions 归一化转换参数（不依赖 FFmpeg，可供表单预校验复用）。
func ClampOptions(o ConvertOptions) ConvertOptions {
	clampOptions(&o)
	return o
}

// Clamp 校验并归一化转换参数，同时依据源图尺寸算出各阶段的目标尺寸。
// caps 为 nil 时跳过"格式是否可用"的检查（能力尚未探测时使用）。
func Clamp(o ConvertOptions, caps *Capabilities) (*ConvertOptions, error) {
	// clampOptions 会把非法取值静默收敛为默认值，但输出格式与缩放方式属于用户明确表达的意图：
	// 走 API 时拼错应当立刻报错，而不是悄悄产出另一种规格的文件。空值仍表示"用默认值"。
	if raw := strings.ToLower(strings.TrimSpace(o.Format)); raw != "" {
		if _, ok := FormatByID(raw); !ok {
			return nil, fmt.Errorf("不支持的输出格式：%s", o.Format)
		}
	}
	if raw := strings.ToLower(strings.TrimSpace(o.Resize)); raw != "" {
		if _, ok := ResizeModeByID(raw); !ok {
			return nil, fmt.Errorf("不支持的缩放方式：%s", o.Resize)
		}
	}

	clampOptions(&o)

	f, ok := FormatByID(o.Format)
	if !ok {
		return nil, fmt.Errorf("不支持的输出格式：%s", o.Format)
	}
	if caps != nil {
		if sup := caps.FormatSupportFor(f.ID); sup != nil && !sup.Available {
			reason := sup.Reason
			if reason == "" {
				reason = "当前 FFmpeg 不支持该格式"
			}
			return nil, fmt.Errorf("当前环境不支持 %s 输出：%s", f.Name, reason)
		}
	}
	o.Format = f.ID

	if o.Animated && !f.Animated {
		return nil, fmt.Errorf("%s 不支持动图输出，请关闭「保留动画」或更换输出格式", f.Name)
	}
	if o.Animated && caps != nil {
		if sup := caps.FormatSupportFor(f.ID); sup != nil && !sup.AnimatedAvailable {
			return nil, fmt.Errorf("当前环境不支持 %s 动图输出", f.Name)
		}
	}
	if o.Watermark.Enabled {
		if o.Watermark.Path == "" {
			return nil, fmt.Errorf("已开启水印但未选择水印图片")
		}
	}

	// 目标尺寸：源图尺寸未知时留给 FFmpeg 表达式推导
	sw, sh := o.SourceWidth, o.SourceHeight
	if sw > 0 && sh > 0 {
		scaleW, scaleH := scaleDims(&o, sw, sh)
		outW, outH := scaleW, scaleH
		if o.Resize == "exact" && o.Adapt == "fill" {
			outW, outH = o.CustomWidth, o.CustomHeight
		}
		o.ScaleWidth, o.ScaleHeight = evenDim(scaleW), evenDim(scaleH)
		o.OutputWidth, o.OutputHeight = evenDim(outW), evenDim(outH)
		// 水印宽度不超过输出宽度
		if o.Watermark.Width > o.OutputWidth {
			o.Watermark.Width = o.OutputWidth
		}
		// 边距不超过输出短边的一半
		if maxMargin := minInt(o.OutputWidth, o.OutputHeight) / 2; o.Watermark.Margin > maxMargin {
			o.Watermark.Margin = maxMargin
		}
	}
	return &o, nil
}

// clampOptions 就地收敛所有数值字段到合法区间。
func clampOptions(o *ConvertOptions) {
	o.Format = strings.ToLower(strings.TrimSpace(o.Format))
	if _, ok := FormatByID(o.Format); !ok {
		o.Format = "webp"
	}
	// Quality 未指定（0）与负值都表示"用格式默认值"。
	// 若漏判 0，clampInt 会把它收敛成 1，等于默认输出全片最差画质。
	if o.Quality <= 0 {
		if f, ok := FormatByID(o.Format); ok {
			o.Quality = f.DefaultQuality
		} else {
			o.Quality = 82
		}
	}
	o.Quality = clampInt(o.Quality, 1, 100)

	o.ColorMode = ColorModeByID(o.ColorMode).ID
	o.Chroma = ChromaByID(o.Chroma).ID

	o.Rotate = NormalizeRotation(o.Rotate)

	if _, ok := ResizeModeByID(o.Resize); !ok {
		o.Resize = "keep"
	}
	o.Size = clampInt(o.Size, 1, 40000)
	if o.Resize == "percent" {
		o.Size = clampInt(o.Size, 1, 1000)
	}
	o.CustomWidth = clampInt(o.CustomWidth, 1, 40000)
	o.CustomHeight = clampInt(o.CustomHeight, 1, 40000)
	if _, ok := AdaptByID(o.Adapt); !ok {
		o.Adapt = "fit"
	}
	// 不允许放大时，百分比不能超过 100
	if o.Resize == "percent" && !o.AllowUpscale && o.Size > 100 {
		o.Size = 100
	}

	o.Brightness = clampInt(o.Brightness, -100, 100)
	o.Contrast = clampInt(o.Contrast, -100, 100)
	o.Saturation = clampInt(o.Saturation, -100, 100)
	o.Sharpen = clampInt(o.Sharpen, 0, 100)
	o.Blur = clampInt(o.Blur, 0, 100)

	if strings.TrimSpace(o.Background) == "" {
		o.Background = "white"
	}
	o.Background = sanitizeColor(o.Background)

	if o.Watermark.Position == "" {
		o.Watermark.Position = "bottom_right"
	}
	if p, ok := WatermarkPositionByID(o.Watermark.Position); ok {
		o.Watermark.Position = p.ID
	} else {
		o.Watermark.Position = "bottom_right"
	}
	o.Watermark.Margin = clampInt(o.Watermark.Margin, 0, 10000)
	o.Watermark.Width = clampInt(o.Watermark.Width, 0, 40000)
	if o.Watermark.Opacity <= 0 {
		o.Watermark.Opacity = 100
	}
	o.Watermark.Opacity = clampInt(o.Watermark.Opacity, 1, 100)
	o.Watermark.Path = strings.TrimSpace(o.Watermark.Path)
	if o.Watermark.Path == "" {
		o.Watermark.Enabled = false
	}

	accel := strings.ToLower(strings.TrimSpace(o.Accel))
	switch accel {
	case AccelNone, AccelNVENC, AccelQSV, AccelVAAPI:
		o.Accel = accel
	default:
		o.Accel = AccelAuto
	}
}

// sanitizeColor 校验背景色，仅接受 ffmpeg color 过滤器支持的安全写法，
// 避免把任意命令片段拼进滤镜链。
func sanitizeColor(c string) string {
	c = strings.TrimSpace(c)
	if c == "" {
		return "white"
	}
	for _, r := range c {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '#', r == '@', r == '.', r == '-', r == ' ', r == '/', r == '(', r == ')':
		default:
			return "white"
		}
	}
	return c
}

// scaleDims 依据缩放设置计算 scale 滤镜的目标尺寸。
// 语义与前端说明一一对应：长边 / 短边 / 指定宽 / 指定高 / 百分比 / 精确尺寸。
func scaleDims(o *ConvertOptions, srcW, srcH int) (int, int) {
	if srcW <= 0 || srcH <= 0 {
		return 0, 0
	}
	size := o.Size
	switch o.Resize {
	case "width":
		if size <= 0 {
			break
		}
		if !o.AllowUpscale && srcW <= size {
			break
		}
		return size, maxInt(1, srcH*size/srcW)

	case "height":
		if size <= 0 {
			break
		}
		if !o.AllowUpscale && srcH <= size {
			break
		}
		return maxInt(1, srcW*size/srcH), size

	case "long_edge":
		if size <= 0 {
			break
		}
		if !o.AllowUpscale && maxInt(srcW, srcH) <= size {
			break
		}
		if srcW >= srcH {
			return size, maxInt(1, srcH*size/srcW)
		}
		return maxInt(1, srcW*size/srcH), size

	case "short_edge":
		if size <= 0 {
			break
		}
		// 语义是"把最短边限制为 size"，因此最短边本来就够小时无需处理
		if !o.AllowUpscale && minInt(srcW, srcH) <= size {
			break
		}
		if srcW >= srcH {
			return maxInt(1, srcW*size/srcH), size
		}
		return size, maxInt(1, srcH*size/srcW)

	case "percent":
		if size <= 0 {
			break
		}
		return srcW * size / 100, srcH * size / 100

	case "exact":
		tw, th := o.CustomWidth, o.CustomHeight
		if tw <= 0 || th <= 0 {
			break
		}
		switch o.Adapt {
		case "stretch":
			return tw, th
		case "fill":
			// 先按覆盖比例放大，再由 crop 裁到目标尺寸
			ratio := math.Max(float64(tw)/float64(srcW), float64(th)/float64(srcH))
			sw := int(math.Round(float64(srcW) * ratio))
			sh := int(math.Round(float64(srcH) * ratio))
			// 偶数对齐后若反而小于目标，补回目标值，保证 crop 区域有效
			sw = maxInt(evenDim(sw), tw)
			sh = maxInt(evenDim(sh), th)
			return sw, sh
		default: // fit：完整显示，不允许放大时不超过原始尺寸
			ratio := math.Min(float64(tw)/float64(srcW), float64(th)/float64(srcH))
			if !o.AllowUpscale && ratio > 1 {
				ratio = 1
			}
			return int(math.Round(float64(srcW) * ratio)), int(math.Round(float64(srcH) * ratio))
		}
	}
	return srcW, srcH
}

// evenDim 折算到偶数并保证至少 2 像素：4:2:0 / 4:2:2 等抽样格式要求宽高为偶数。
func evenDim(v int) int {
	if v < 2 {
		return 2
	}
	if v%2 == 0 {
		return v
	}
	return v - 1
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// ==================== 命令构建 ====================

// BuildConvertArgs 构建 ffmpeg 转换命令（完整参数，含输出文件）。
func (c *Client) BuildConvertArgs(o ConvertOptions, caps *Capabilities, in, out string) ([]string, error) {
	f, ok := FormatByID(o.Format)
	if !ok {
		return nil, fmt.Errorf("不支持的输出格式：%s", o.Format)
	}
	clamped, err := Clamp(o, caps)
	if err != nil {
		return nil, err
	}
	o = *clamped

	accel := c.resolveAccel(f, &o, caps)
	encoder := selectEncoder(f, o, accel, caps)

	args := []string{"-hide_banner", "-nostdin", "-loglevel", "error"}
	if !o.AutoOrient {
		args = append(args, "-noautorotate")
	}
	args = append(args, "-i", in)

	// 透明图输出到不支持透明的格式时，先合成到纯色背景。
	// overlay 的主图在前、被叠加图在后：这里要"把图片叠到背景上"，
	// 因此背景是主图、图片是叠加图，写反只会得到纯背景色。
	composite := o.SourceAlpha && !f.Alpha && o.OutputWidth > 0 && o.OutputHeight > 0
	whiteIdx, wmIdx := -1, -1
	inputs := 1 // 0 号输入恒为主图
	if composite {
		args = append(args, "-f", "lavfi", "-i",
			fmt.Sprintf("color=c=%s:s=%dx%d:d=1", o.Background, o.OutputWidth, o.OutputHeight))
		whiteIdx = inputs
		inputs++
	}
	if o.Watermark.Enabled {
		args = append(args, "-i", o.Watermark.Path)
		wmIdx = inputs
		inputs++
	}

	chain := buildChain(&o, f)
	graph, outLabel := buildGraph(&o, f, chain, composite, whiteIdx, wmIdx)
	if graph != "" {
		args = append(args, "-filter_complex", graph, "-map", "["+outLabel+"]")
	} else if len(chain) > 0 {
		args = append(args, "-vf", strings.Join(chain, ","))
	}

	args = append(args, c.encoderArgs(encoder, f, &o)...)
	args = append(args, pixFmtArgs(f, &o, encoder)...)

	if o.StripMetadata {
		args = append(args, "-map_metadata", "-1")
	}
	if o.Threads > 0 {
		args = append(args, "-threads", strconv.Itoa(o.Threads))
	}

	args = append(args, "-an", "-sn", "-dn")
	if !o.Animated {
		args = append(args, "-frames:v", "1")
	}
	if o.Animated && f.ID == "gif" {
		args = append(args, "-loop", "0")
	}
	args = append(args, "-f", muxerFor(f, o.Animated, caps))
	args = append(args, "-y", out)
	return args, nil
}

// muxerFor 返回本次输出实际使用的封装名。
func muxerFor(f Format, animated bool, caps *Capabilities) string {
	if caps != nil {
		return caps.ResolveMuxer(f, animated)
	}
	return f.OutputMuxer(animated)
}

// buildChain 生成串行滤镜链：旋转翻转 → 缩放裁剪 → 色彩 → 锐化模糊。
func buildChain(o *ConvertOptions, f Format) []string {
	var chain []string

	if t := transformFilter(o); t != "" {
		chain = append(chain, t)
	}

	needScale := o.ScaleWidth > 0 && o.ScaleHeight > 0 &&
		(o.ScaleWidth != o.SourceWidth || o.ScaleHeight != o.SourceHeight)
	switch {
	case needScale:
		chain = append(chain, fmt.Sprintf("scale=%d:%d:flags=lanczos", o.ScaleWidth, o.ScaleHeight))
	case o.ScaleWidth == 0:
		// 源图尺寸未知（例如探测失败）时不能把缩放悄悄丢掉，
		// 改用表达式让 FFmpeg 依据实际输入尺寸推导。
		if e := scaleExpr(o); e != "" {
			chain = append(chain, e)
		}
	}
	if o.OutputWidth > 0 && o.OutputHeight > 0 &&
		(o.OutputWidth != o.ScaleWidth || o.OutputHeight != o.ScaleHeight) {
		x := (o.ScaleWidth - o.OutputWidth) / 2
		y := (o.ScaleHeight - o.OutputHeight) / 2
		if x < 0 {
			x = 0
		}
		if y < 0 {
			y = 0
		}
		chain = append(chain, fmt.Sprintf("crop=%d:%d:%d:%d", o.OutputWidth, o.OutputHeight, x, y))
	}

	if o.ColorMode == "grayscale" {
		chain = append(chain, "format=gray")
	}

	if o.Brightness != 0 || o.Contrast != 0 || o.Saturation != 0 {
		// eq 的取值域：brightness -1~1、contrast 0~1000、saturation 0~3
		b := float64(o.Brightness) / 100 * 0.7
		c := 1 + float64(o.Contrast)/100*1.5
		s := 1 + float64(o.Saturation)/100*1.5
		chain = append(chain, fmt.Sprintf("eq=brightness=%.3f:contrast=%.3f:saturation=%.3f", b, c, s))
	}

	if o.Sharpen > 0 {
		// 亮度通道做锐化，色度通道不动，避免出现彩色噪点
		amount := float64(o.Sharpen) / 100 * 1.5
		chain = append(chain, fmt.Sprintf("unsharp=5:5:%.2f:5:5:0.00", amount))
	}
	if o.Blur > 0 {
		sigma := float64(o.Blur) / 100 * 6
		chain = append(chain, fmt.Sprintf("gblur=sigma=%.2f:steps=1", sigma))
	}
	return chain
}

// scaleExpr 在源图尺寸未知时，用表达式描述缩放规则。
// 表达式中的逗号是滤镜选项分隔符，必须转义成 "\,"。
func scaleExpr(o *ConvertOptions) string {
	size := o.Size
	// 不允许放大时用 min(原边, 目标边) 限制
	limit := func(s string) string {
		if o.AllowUpscale {
			return fmt.Sprint(size)
		}
		return fmt.Sprintf("min(%s\\,%d)", s, size)
	}
	switch o.Resize {
	case "width":
		return fmt.Sprintf("scale=w=%s:h=-2:flags=lanczos", limit("iw"))
	case "height":
		return fmt.Sprintf("scale=w=-2:h=%s:flags=lanczos", limit("ih"))
	case "long_edge":
		w := limit("iw")
		h := limit("ih")
		if !o.AllowUpscale {
			w = fmt.Sprintf("if(gte(iw\\,ih)\\,min(iw\\,%d)\\,-2)", size)
			h = fmt.Sprintf("if(gte(iw\\,ih)\\,-2\\,min(ih\\,%d))", size)
		} else {
			w = fmt.Sprintf("if(gte(iw\\,ih)\\,%d\\,-2)", size)
			h = fmt.Sprintf("if(gte(iw\\,ih)\\,-2\\,%d)", size)
		}
		return fmt.Sprintf("scale=w=%s:h=%s:flags=lanczos", w, h)
	case "short_edge":
		w := fmt.Sprint(size)
		h := fmt.Sprint(size)
		if !o.AllowUpscale {
			w = fmt.Sprintf("if(gte(iw\\,ih)\\,-2\\,min(iw\\,%d))", size)
			h = fmt.Sprintf("if(gte(iw\\,ih)\\,min(ih\\,%d)\\,-2)", size)
		} else {
			w = fmt.Sprintf("if(gte(iw\\,ih)\\,-2\\,%d)", size)
			h = fmt.Sprintf("if(gte(iw\\,ih)\\,%d\\,-2)", size)
		}
		return fmt.Sprintf("scale=w=%s:h=%s:flags=lanczos", w, h)
	case "percent":
		return fmt.Sprintf("scale=w=trunc(iw*%d/100/2)*2:h=trunc(ih*%d/100/2)*2:flags=lanczos", size, size)
	case "exact":
		tw, th := o.CustomWidth, o.CustomHeight
		switch o.Adapt {
		case "stretch":
			return fmt.Sprintf("scale=w=%d:h=%d:flags=lanczos", tw, th)
		case "fill":
			// increase 会放大到刚好填满，再居中裁掉溢出部分
			return fmt.Sprintf("scale=w=%d:h=%d:force_original_aspect_ratio=increase:flags=lanczos,crop=%d:%d:(in_w-out_w)/2:(in_h-out_h)/2",
				tw, th, tw, th)
		default:
			// decrease 天然不会放大
			return fmt.Sprintf("scale=w=%d:h=%d:force_original_aspect_ratio=decrease:flags=lanczos", tw, th)
		}
	}
	return ""
}

// transformFilter 由旋转 / 翻转参数生成 transpose 滤镜。
func transformFilter(o *ConvertOptions) string {
	var fs []string
	switch o.Rotate {
	case 90:
		fs = append(fs, "transpose=1")
	case 180:
		fs = append(fs, "transpose=1", "transpose=1")
	case 270:
		fs = append(fs, "transpose=2")
	}
	if o.FlipH {
		fs = append(fs, "hflip")
	}
	if o.FlipV {
		fs = append(fs, "vflip")
	}
	return strings.Join(fs, ",")
}

// buildGraph 组装需要多输入的滤镜图（背景合成 / 水印叠加 / GIF 调色板）。
// 返回空串表示只需串行滤镜链。
func buildGraph(o *ConvertOptions, f Format, chain []string, composite bool, whiteIdx, wmIdx int) (string, string) {
	palette := f.ID == "gif"
	if !composite && !o.Watermark.Enabled && !palette {
		return "", ""
	}

	var parts []string
	cur := "0:v"
	if len(chain) > 0 {
		parts = append(parts, fmt.Sprintf("[0:v]%s[v0]", strings.Join(chain, ",")))
		cur = "v0"
	}
	seq := 0
	label := func(p string) string {
		seq++
		return fmt.Sprintf("%s%d", p, seq)
	}

	if composite && whiteIdx >= 0 {
		out := label("bg")
		// 背景在前、图片在后：图片被合成到背景色上
		parts = append(parts, fmt.Sprintf("[%d:v][%s]overlay=x=0:y=0[%s]", whiteIdx, cur, out))
		cur = out
	}

	if o.Watermark.Enabled && wmIdx >= 0 {
		var wm []string
		wm = append(wm, "format=rgba")
		if o.Watermark.Width > 0 {
			wm = append(wm, fmt.Sprintf("scale=%d:-1", o.Watermark.Width))
		}
		if o.Watermark.Opacity < 100 {
			wm = append(wm, fmt.Sprintf("colorchannelmixer=aa=%.3f", float64(o.Watermark.Opacity)/100))
		}
		w := label("wmk")
		parts = append(parts, fmt.Sprintf("[%d:v]%s[%s]", wmIdx, strings.Join(wm, ","), w))
		out := label("wm")
		parts = append(parts, fmt.Sprintf("[%s][%s]overlay=%s[%s]",
			cur, w, overlayExpr(o.Watermark.Position, o.Watermark.Margin), out))
		cur = out
	}

	if palette {
		colors := f.Quality.EncoderValue(o.Quality)
		if colors < 2 {
			colors = 2
		}
		if colors > 256 {
			colors = 256
		}
		a, b := label("s"), label("s")
		pal := label("pal")
		out := label("gif")
		parts = append(parts, fmt.Sprintf("[%s]split=2[%s][%s]", cur, a, b))
		parts = append(parts, fmt.Sprintf("[%s]palettegen=max_colors=%d:stats_mode=diff[%s]", a, colors, pal))
		parts = append(parts, fmt.Sprintf("[%s][%s]paletteuse=dither=bayer:bayer_scale=3[%s]", b, pal, out))
		cur = out
	}

	return strings.Join(parts, ";"), cur
}

// overlayExpr 生成水印位置表达式。overlay 滤镜中 W/H 为主图尺寸，w/h 为水印尺寸。
func overlayExpr(pos string, margin int) string {
	m := margin
	switch pos {
	case "top_left":
		return fmt.Sprintf("x=%d:y=%d", m, m)
	case "top":
		return fmt.Sprintf("x=(W-w)/2:y=%d", m)
	case "top_right":
		return fmt.Sprintf("x=W-w-%d:y=%d", m, m)
	case "left":
		return fmt.Sprintf("x=%d:y=(H-h)/2", m)
	case "center":
		return "x=(W-w)/2:y=(H-h)/2"
	case "right":
		return fmt.Sprintf("x=W-w-%d:y=(H-h)/2", m)
	case "bottom_left":
		return fmt.Sprintf("x=%d:y=H-h-%d", m, m)
	case "bottom":
		return fmt.Sprintf("x=(W-w)/2:y=H-h-%d", m)
	default: // bottom_right
		return fmt.Sprintf("x=W-w-%d:y=H-h-%d", m, m)
	}
}

// resolveAccel 决定本次转换实际使用的加速策略。
// 只有经真实编码自检的（格式 → 编码器）组合才会被选中。
func (c *Client) resolveAccel(f Format, o *ConvertOptions, caps *Capabilities) string {
	accel := o.Accel
	if accel == "" {
		accel = c.accel
	}
	hw, hasHW := caps.HWFormats[f.ID]
	if caps == nil {
		hasHW = false
	}
	if accel != AccelAuto {
		// 指定了加速，但该格式并没有通过自检 → 退回软件
		if hasHW && hw.Accel == accel {
			return accel
		}
		return AccelNone
	}
	if hasHW {
		o.Accel = hw.Accel
		return hw.Accel
	}
	o.Accel = AccelNone
	return AccelNone
}

// selectEncoder 按优先级挑选编码器：自检通过的硬件编码器 → 动图编码器 → 软件列表。
func selectEncoder(f Format, o ConvertOptions, accel string, caps *Capabilities) string {
	if accel != AccelNone && caps != nil {
		if hw, ok := caps.HWFormats[f.ID]; ok && hw.Accel == accel {
			return hw.Encoder
		}
	}
	if o.Animated && f.AnimatedEncoder != "" {
		if accel == AccelNone && encoderAvailable(caps, f.AnimatedEncoder) {
			return f.AnimatedEncoder
		}
	}
	for _, enc := range f.SoftwareEncoders {
		if encoderAvailable(caps, enc) {
			return enc
		}
	}
	// 探测结果不可用时退回首选，交由 ffmpeg 给出可读报错
	if len(f.SoftwareEncoders) > 0 {
		return f.SoftwareEncoders[0]
	}
	return ""
}

func encoderAvailable(caps *Capabilities, name string) bool {
	if caps == nil || name == "" {
		return true
	}
	for _, e := range caps.Encoders {
		if e == name {
			return true
		}
	}
	return false
}

// encoderArgs 生成编码器参数。
func (c *Client) encoderArgs(enc string, f Format, o *ConvertOptions) []string {
	args := []string{"-c:v", enc}
	switch {
	case enc == "mjpeg" || enc == "jpeg2000":
		args = append(args, "-q:v", strconv.Itoa(f.Quality.EncoderValue(o.Quality)))
	case enc == "libwebp" || enc == "libwebp_anim":
		q := f.Quality.EncoderValue(o.Quality)
		args = append(args, "-quality", strconv.Itoa(q))
		if q >= 100 {
			args = append(args, "-lossless", "1")
		}
	case enc == "png" || enc == "apng" || enc == "tiff" || enc == "libtiff":
		args = append(args, "-compression_level", strconv.Itoa(f.Quality.EncoderValue(o.Quality)))
	case enc == "libaom-av1", enc == "libsvtav1", enc == "av1_nvenc", enc == "av1_qsv", enc == "av1_vaapi", enc == "libjxl", enc == "jxl":
		args = append(args, "-crf", strconv.Itoa(f.Quality.EncoderValue(o.Quality)))
	case strings.HasPrefix(enc, "hevc_"), enc == "libx265":
		args = append(args, "-crf", strconv.Itoa(f.Quality.EncoderValue(o.Quality)))
		if enc == "libx265" {
			// x265 默认会刷屏，任务日志只保留必要内容
			args = append(args, "-x265-params", "log-level=error")
		}
	}
	// AVIF 单帧编码：libaom 支持 still-picture（显著减小体积与耗时），
	// 但部分编译版本没有该选项，硬写会让整条命令失败，因此先探测。
	if enc == "libaom-av1" && c.encoderHasOption(enc, "still-picture") {
		args = append(args, "-still-picture", "1")
		if c.encoderHasOption(enc, "cpu-used") {
			args = append(args, "-cpu-used", strconv.Itoa(avifCPUUsed(o.Quality)))
		}
	}
	if enc == "libsvtav1" {
		args = append(args, "-preset", strconv.Itoa(avifPreset(o.Quality)))
	}
	return args
}

// avifCPUUsed 把界面质量值映射为 libaom 的 cpu-used（0 最慢最清晰）。
func avifCPUUsed(quality int) int {
	switch {
	case quality >= 90:
		return 0
	case quality >= 75:
		return 1
	case quality >= 55:
		return 2
	default:
		return 4
	}
}

// avifPreset 把界面质量值映射为 libsvtav1 的 preset（0 最慢最清晰，13 最快）。
func avifPreset(quality int) int {
	switch {
	case quality >= 90:
		return 4
	case quality >= 70:
		return 6
	case quality >= 50:
		return 8
	default:
		return 10
	}
}

// pixFmtArgs 生成像素格式参数。灰度与色度抽样在这里落地。
func pixFmtArgs(f Format, o *ConvertOptions, enc string) []string {
	if o.ColorMode == "grayscale" {
		return []string{"-pix_fmt", "gray"}
	}
	switch f.Quality.Kind {
	case QualityQScale, QualityCRF, QualityPercent:
	default:
		// PNG / TIFF / GIF / BMP / QOI 原生按 RGB(A) 或索引存储，不做色度转换
		return nil
	}
	args := pixFmtForFormat(f, o)
	// 部分硬件编码器（如 mjpeg_qsv）不理会输入色彩范围，始终按 limited range
	// 写出的 YUV 值，标记为全范围的 yuvj* 像素格式时白底会变成 235。
	// 显式声明全范围，编码器才会写出正确的 0~255 取值。
	if isHWEncoder(enc) && isFullRangePixFmt(args) {
		args = append([]string{"-color_range", "pc"}, args...)
	}
	return args
}

// pixFmtForFormat 按格式与色度抽样选择输出像素格式。
func pixFmtForFormat(f Format, o *ConvertOptions) []string {
	switch f.ID {
	case "jpg":
		switch o.Chroma {
		case "444":
			return []string{"-pix_fmt", "yuvj444p"}
		case "422":
			return []string{"-pix_fmt", "yuvj422p"}
		default:
			return []string{"-pix_fmt", "yuvj420p"}
		}
	case "heic", "heif":
		switch o.Chroma {
		case "444":
			return []string{"-pix_fmt", "yuv444p"}
		case "422":
			return []string{"-pix_fmt", "yuv422p"}
		default:
			return []string{"-pix_fmt", "yuv420p"}
		}
	case "avif":
		if o.SourceAlpha && f.Alpha {
			switch o.Chroma {
			case "444":
				return []string{"-pix_fmt", "yuva444p"}
			case "422":
				return []string{"-pix_fmt", "yuva422p"}
			default:
				return []string{"-pix_fmt", "yuva420p"}
			}
		}
		switch o.Chroma {
		case "444":
			return []string{"-pix_fmt", "yuv444p"}
		case "422":
			return []string{"-pix_fmt", "yuv422p"}
		default:
			return []string{"-pix_fmt", "yuv420p"}
		}
	case "webp":
		switch o.Chroma {
		case "444":
			return []string{"-pix_fmt", "yuv444p"}
		case "422":
			return []string{"-pix_fmt", "yuv422p"}
		}
	}
	return nil
}

// isHWEncoder 判断是否为硬件编码器（编码器名以 _qsv / _nvenc / _vaapi 等结尾）。
func isHWEncoder(enc string) bool {
	e := strings.ToLower(enc)
	for _, suffix := range []string{"_qsv", "_nvenc", "_vaapi", "_videotoolbox", "_amf", "_mf", "_v4l2m2m"} {
		if strings.HasSuffix(e, suffix) {
			return true
		}
	}
	return false
}

// isFullRangePixFmt 判断像素格式参数是否为全范围（yuvj* 是 JPEG 惯例的全范围标记）。
func isFullRangePixFmt(args []string) bool {
	if len(args) < 2 || args[0] != "-pix_fmt" {
		return false
	}
	return strings.HasPrefix(args[1], "yuvj")
}

// PreviewCommand 生成"预览用"命令，供前端在参数面板展示实际执行的命令。
// 输出落到系统临时目录，仅用于展示与人工排查。
func (c *Client) PreviewCommand(o ConvertOptions, caps *Capabilities, in string) ([]string, error) {
	f, ok := FormatByID(o.Format)
	if !ok {
		f, _ = FormatByID("webp")
	}
	o.Format = f.ID
	out := filepath.Join(os.TempDir(), fmt.Sprintf("fan-image-preview-%d%s", os.Getpid(), f.Ext))
	return c.BuildConvertArgs(o, caps, in, out)
}

// ==================== 执行转换 ====================

// Progress 转换进度。
type Progress struct {
	// Frame 已输出帧数
	Frame int `json:"frame"`
	// OutTimeUS 已编码时长（微秒）
	OutTimeUS int64 `json:"out_time_us"`
	// TotalSize 当前输出体积（字节，-1 表示未知）
	TotalSize int64 `json:"total_size"`
	// Speed 编码速度
	Speed string `json:"speed"`
	// Percent 完成百分比（0~100，源时长未知时为 -1）
	Percent float64 `json:"percent"`
	// Done 是否已结束
	Done bool `json:"done"`
}

// RunConvert 执行一次图片转换，并通过 onProgress 回调报告进度。
// expect 为源图时长（动图有效，静态图传 0），用于换算百分比。
//
// 硬件编码失败时会自动退回软件编码重试一次：容器设备映射、驱动版本等
// 问题在能力探测阶段未必暴露，届时静默失败对用户毫无意义。
func (c *Client) RunConvert(ctx context.Context, in, out string, o ConvertOptions, caps *Capabilities, expect time.Duration, onProgress func(Progress)) error {
	args, err := c.BuildConvertArgs(o, caps, in, out)
	if err != nil {
		return err
	}
	runErr := c.runOnce(ctx, args, out, expect, onProgress)
	if runErr == nil || !usesHardwareEncoder(args) {
		return runErr
	}

	// 硬件路径失败：强制软件编码重试
	o.Accel = AccelNone
	retryArgs, err := c.BuildConvertArgs(o, caps, in, out)
	if err != nil {
		return runErr
	}
	if retryErr := c.runOnce(ctx, retryArgs, out, expect, onProgress); retryErr != nil {
		return fmt.Errorf("%v（已自动回退到软件编码，仍失败：%w）", runErr, retryErr)
	}
	return nil
}

// usesHardwareEncoder 判断命令里是否用了硬件编码器。
func usesHardwareEncoder(args []string) bool {
	for i := 0; i < len(args)-1; i++ {
		if args[i] != "-c:v" {
			continue
		}
		enc := args[i+1]
		for _, f := range formats() {
			for _, sw := range f.SoftwareEncoders {
				if sw == enc {
					return false
				}
			}
			if f.AnimatedEncoder == enc {
				return false
			}
		}
		return true
	}
	return false
}

// runOnce 执行一次已构建好的 ffmpeg 命令。
func (c *Client) runOnce(ctx context.Context, args []string, out string, expect time.Duration, onProgress func(Progress)) error {
	args = append(args[:len(args)-1], "-progress", "pipe:1", "-nostats", args[len(args)-1])

	// 输出文件先移除，避免编码失败时残留上一版结果被误认为成功
	if err := os.Remove(out); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("清理输出文件失败：%w", err)
	}

	cmd := exec.CommandContext(ctx, c.ffmpegBin, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr := &limitedBuffer{max: 64 << 10}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("启动 FFmpeg 失败：%w", err)
	}

	report := func(p Progress) {
		if onProgress == nil {
			return
		}
		if expect > 0 {
			p.Percent = float64(p.OutTimeUS) / float64(expect.Microseconds()) * 100
			if p.Percent > 99.9 {
				p.Percent = 99.9
			}
		} else {
			p.Percent = -1
		}
		onProgress(p)
	}

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 256*1024)
	last := Progress{TotalSize: -1}
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		switch key {
		case "frame":
			last.Frame, _ = strconv.Atoi(val)
		case "out_time_us":
			if v, err := strconv.ParseInt(val, 10, 64); err == nil {
				last.OutTimeUS = v
			}
		case "total_size":
			if v, err := strconv.ParseInt(val, 10, 64); err == nil {
				last.TotalSize = v
			}
		case "speed":
			last.Speed = val
		case "progress":
			done := val == "end"
			report(last)
			if done {
				last = Progress{Frame: last.Frame, OutTimeUS: last.OutTimeUS, TotalSize: last.TotalSize, Speed: last.Speed}
			}
		}
	}
	waitErr := cmd.Wait()
	if waitErr != nil {
		msg := summarizeErr(stderr.String())
		if msg == "" {
			msg = waitErr.Error()
		}
		return fmt.Errorf("FFmpeg 转换失败：%s", msg)
	}

	st, err := os.Stat(out)
	if err != nil {
		return fmt.Errorf("转换未生成输出文件：%w", err)
	}
	if st.Size() == 0 {
		return fmt.Errorf("转换生成的输出文件为空")
	}
	p := last
	p.Done = true
	report(p)
	return nil
}

// ThumbnailBytes 生成缩略图并返回 JPEG 字节（用于文件浏览器网格视图）。
func (c *Client) ThumbnailBytes(ctx context.Context, path string, size int) ([]byte, error) {
	if size <= 0 {
		size = c.thumbSize
	}
	dir, err := os.MkdirTemp("", "fan-image-thumb-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	out := filepath.Join(dir, "thumb.jpg")

	args := []string{
		"-hide_banner", "-nostdin", "-loglevel", "error",
		"-i", path,
		"-vf", fmt.Sprintf("scale=%d:%d:force_original_aspect_ratio=decrease:flags=fast_bilinear", size, size),
		"-frames:v", "1",
		"-q:v", "6",
		"-f", "image2", "-y", out,
	}
	if o, err := runLimited(ctx, 60*time.Second, c.ffmpegBin, args...); err != nil {
		if msg := summarizeErr(o); msg != "" {
			return nil, fmt.Errorf("生成缩略图失败：%s", msg)
		}
		return nil, fmt.Errorf("生成缩略图失败：%w", err)
	}
	return os.ReadFile(out)
}

// summarizeErr 从 FFmpeg 的输出里提取可读的核心错误（过滤掉版本横幅与库日志）。
func summarizeErr(s string) string {
	lines := strings.Split(s, "\n")
	var picked []string
	for i := len(lines) - 1; i >= 0 && len(picked) < 4; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		// 丢弃编码器库的调试日志（如 Svt[info]、libx265 log）
		if strings.HasPrefix(line, "Svt[") || strings.HasPrefix(line, "x265 [") ||
			strings.HasPrefix(line, "libaom") {
			continue
		}
		picked = append(picked, line)
	}
	// 反转回原始顺序
	for i, j := 0, len(picked)-1; i < j; i, j = i+1, j-1 {
		picked[i], picked[j] = picked[j], picked[i]
	}
	return strings.Join(picked, "; ")
}

// FormatSupportFor 查找某格式的能力描述。
func (c *Capabilities) FormatSupportFor(id string) *FormatSupport {
	for i := range c.Formats {
		if c.Formats[i].Format.ID == id {
			return &c.Formats[i]
		}
	}
	return nil
}

// encoderOptionCache 缓存"某编码器是否支持某私有选项"，避免重复执行 ffmpeg -h。
var encoderOptionCache sync.Map

// encoderHasOption 查询编码器是否支持某个私有选项（如 libaom 的 still-picture）。
// 不同 FFmpeg 编译版本差异很大，硬编码私有选项会直接让命令失败，故先探测。
func (c *Client) encoderHasOption(enc, opt string) bool {
	key := enc + "\x00" + opt
	if v, ok := encoderOptionCache.Load(key); ok {
		return v.(bool)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	out, err := runLimited(ctx, 15*time.Second, c.ffmpegBin, "-hide_banner", "-h", "encoder="+enc)
	has := err == nil && strings.Contains(out, opt)
	encoderOptionCache.Store(key, has)
	return has
}

// CommandLine 拼出可复制的命令行文本（供界面查看命令）。
func CommandLine(bin string, args []string) string {
	parts := make([]string, 0, len(args)+1)
	parts = append(parts, bin)
	for _, a := range args {
		if a == "" {
			continue
		}
		if strings.ContainsAny(a, " \t\"'") {
			parts = append(parts, strconv.Quote(a))
			continue
		}
		parts = append(parts, a)
	}
	return strings.Join(parts, " ")
}

// FormatBytes 把字节数格式化为易读文本，供任务日志与体积预估展示。
func FormatBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGT"[exp])
}
