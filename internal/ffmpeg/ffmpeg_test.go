package ffmpeg

import (
	"bytes"
	"context"
	"image"
	"image/jpeg"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// newTestClient 创建测试用客户端；本机没有 ffmpeg 时跳过测试。
func newTestClient(t *testing.T) *Client {
	t.Helper()
	c, err := New(Options{ThumbSize: 128})
	if err != nil {
		t.Skipf("跳过测试：本机没有可用的 ffmpeg（%v）", err)
	}
	return c
}

// makeFixture 用 ffmpeg 现场生成测试图片，避免在仓库里放二进制素材。
func makeFixture(t *testing.T, c *Client, name string, filter string, size string) string {
	t.Helper()
	dir := t.TempDir()
	out := filepath.Join(dir, name)
	src := "testsrc2=s=" + size + ":d=1"
	if filter != "" {
		src = filter
	}
	args := []string{"-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", src, "-frames:v", "1", "-y", out}
	if _, err := runLimited(context.Background(), 60*time.Second, c.ffmpegBin, args...); err != nil {
		t.Fatalf("生成测试素材 %s 失败: %v", out, err)
	}
	return out
}

// ==================== 纯逻辑测试（无需 ffmpeg） ====================

func TestScaleDims(t *testing.T) {
	cases := []struct {
		name    string
		opts    ConvertOptions
		srcW    int
		srcH    int
		wantW   int
		wantH   int
		wantOut int // 0 表示与 scale 尺寸一致
	}{
		{
			name: "长边缩放-横图", opts: ConvertOptions{Resize: "long_edge", Size: 1920},
			srcW: 4000, srcH: 3000, wantW: 1920, wantH: 1440,
		},
		{
			name: "长边缩放-竖图", opts: ConvertOptions{Resize: "long_edge", Size: 1920},
			srcW: 3000, srcH: 4000, wantW: 1440, wantH: 1920,
		},
		{
			name: "短边缩放", opts: ConvertOptions{Resize: "short_edge", Size: 800},
			srcW: 4000, srcH: 3000, wantW: 1066, wantH: 800,
		},
		{
			name: "短边已小于目标不处理", opts: ConvertOptions{Resize: "short_edge", Size: 800},
			srcW: 900, srcH: 600, wantW: 900, wantH: 600,
		},
		{
			name: "指定宽度", opts: ConvertOptions{Resize: "width", Size: 1920},
			srcW: 4000, srcH: 3000, wantW: 1920, wantH: 1440,
		},
		{
			name: "指定高度", opts: ConvertOptions{Resize: "height", Size: 1080},
			srcW: 4000, srcH: 3000, wantW: 1440, wantH: 1080,
		},
		{
			name: "百分比", opts: ConvertOptions{Resize: "percent", Size: 50},
			srcW: 4000, srcH: 3000, wantW: 2000, wantH: 1500,
		},
		{
			name: "不放大-长边已达标", opts: ConvertOptions{Resize: "long_edge", Size: 8000},
			srcW: 4000, srcH: 3000, wantW: 4000, wantH: 3000,
		},
		{
			name: "允许放大", opts: ConvertOptions{Resize: "long_edge", Size: 8000, AllowUpscale: true},
			srcW: 4000, srcH: 3000, wantW: 8000, wantH: 6000,
		},
		{
			name: "精确尺寸-完整显示", opts: ConvertOptions{Resize: "exact", CustomWidth: 1920, CustomHeight: 1080, Adapt: "fit"},
			srcW: 4000, srcH: 3000, wantW: 1440, wantH: 1080,
		},
		{
			name: "精确尺寸-填满裁剪", opts: ConvertOptions{Resize: "exact", CustomWidth: 1920, CustomHeight: 1080, Adapt: "fill"},
			srcW: 4000, srcH: 3000, wantW: 1920, wantH: 1440, wantOut: 1080,
		},
		{
			name: "精确尺寸-强制拉伸", opts: ConvertOptions{Resize: "exact", CustomWidth: 1920, CustomHeight: 1080, Adapt: "stretch"},
			srcW: 4000, srcH: 3000, wantW: 1920, wantH: 1080,
		},
		{
			name: "精确尺寸-完整显示且不放大", opts: ConvertOptions{Resize: "exact", CustomWidth: 1920, CustomHeight: 1080, Adapt: "fit"},
			srcW: 800, srcH: 600, wantW: 800, wantH: 600,
		},
		{
			name: "奇数尺寸向下取偶", opts: ConvertOptions{Resize: "width", Size: 1001},
			srcW: 4000, srcH: 3000, wantW: 1000, wantH: 750,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			o := tc.opts
			clampOptions(&o)
			sw, sh := scaleDims(&o, tc.srcW, tc.srcH)
			if gotW, gotH := evenDim(sw), evenDim(sh); gotW != tc.wantW || gotH != tc.wantH {
				t.Fatalf("scale 尺寸 = %dx%d, 期望 %dx%d", gotW, gotH, tc.wantW, tc.wantH)
			}
			if tc.wantOut > 0 {
				// 需要裁剪的场景再走一遍完整归一化，校验最终输出尺寸
				o.SourceWidth, o.SourceHeight = tc.srcW, tc.srcH
				clamped, err := Clamp(o, nil)
				if err != nil {
					t.Fatalf("参数归一化失败: %v", err)
				}
				if clamped.OutputWidth != tc.wantW || clamped.OutputHeight != tc.wantOut {
					t.Fatalf("输出尺寸 = %dx%d, 期望 %dx%d",
						clamped.OutputWidth, clamped.OutputHeight, tc.wantW, tc.wantOut)
				}
			}
		})
	}
}

func TestClampOptionsDefaults(t *testing.T) {
	o := DefaultOptions("png")
	clampOptions(&o)
	if o.Format != "png" {
		t.Fatalf("格式 = %s, 期望 png", o.Format)
	}
	if o.Quality != 90 {
		t.Fatalf("质量默认值 = %d, 期望 90", o.Quality)
	}

	// 未指定质量（0）应取格式默认值，而不是被收敛成最低画质 1
	for _, id := range []string{"jpg", "png", "webp", "avif", "gif"} {
		o = ConvertOptions{Format: id}
		clampOptions(&o)
		f, _ := FormatByID(id)
		if o.Quality != f.DefaultQuality {
			t.Errorf("格式 %s 未指定质量 = %d，期望默认值 %d", id, o.Quality, f.DefaultQuality)
		}
	}

	// 越界与非法值都应被收敛
	o = ConvertOptions{
		Format:       "不存在的格式",
		Quality:      9999,
		Rotate:       100,
		Resize:       "乱写",
		Chroma:       "999",
		ColorMode:    "乱写",
		Size:         -5,
		Accel:        "乱写",
		Background:   "white; rm -rf /",
		AllowUpscale: false,
	}
	clampOptions(&o)
	if o.Format != "webp" {
		t.Errorf("非法格式应回退 webp, 实际 %s", o.Format)
	}
	if o.Quality != 100 {
		t.Errorf("质量应被收敛到 100, 实际 %d", o.Quality)
	}
	if o.Rotate != 90 {
		t.Errorf("旋转应折算到 90, 实际 %d", o.Rotate)
	}
	if o.Resize != "keep" {
		t.Errorf("非法缩放方式应回退 keep, 实际 %s", o.Resize)
	}
	if o.Chroma != "420" {
		t.Errorf("非法色度应回退 420, 实际 %s", o.Chroma)
	}
	if o.ColorMode != "keep" {
		t.Errorf("非法颜色模式应回退 keep, 实际 %s", o.ColorMode)
	}
	if o.Accel != AccelAuto {
		t.Errorf("非法加速应回退 auto, 实际 %s", o.Accel)
	}
	if o.Background != "white" {
		t.Errorf("背景色应过滤为 white, 实际 %q", o.Background)
	}

	// 不允许放大时百分比不能超过 100
	o = ConvertOptions{Resize: "percent", Size: 400}
	clampOptions(&o)
	if o.Size != 100 {
		t.Errorf("不允许放大时百分比应被收敛到 100, 实际 %d", o.Size)
	}
}

func TestWatermarkSanitize(t *testing.T) {
	o := ConvertOptions{Resize: "keep", Watermark: WatermarkOptions{Opacity: 50}}
	o.Background = "0x00ff00"
	clampOptions(&o)
	if o.Background != "0x00ff00" {
		t.Errorf("合法背景色应被保留, 实际 %q", o.Background)
	}
	o.Background = "red,format=rgba"
	clampOptions(&o)
	if o.Background != "white" {
		t.Errorf("含分隔符的背景色应被过滤, 实际 %q", o.Background)
	}
}

func TestEstimateBytes(t *testing.T) {
	f, _ := FormatByID("webp")
	w, h := 1920, 1080
	high := EstimateBytes(f, 95, w, h, "keep")
	low := EstimateBytes(f, 20, w, h, "keep")
	if high <= low {
		t.Fatalf("有损格式质量越高预估体积应越大: %d vs %d", high, low)
	}
	// 有损格式：预估体积应随质量单调不减
	for _, id := range []string{"jpg", "webp", "avif", "heic", "gif", "jpeg2000"} {
		f, ok := FormatByID(id)
		if !ok {
			t.Fatalf("格式 %s 不存在", id)
		}
		prev := int64(0)
		for q := 10; q <= 100; q += 10 {
			cur := EstimateBytes(f, q, w, h, "keep")
			if cur < prev {
				t.Errorf("%s 质量 %d 的预估体积 %d 小于更低质量的 %d", id, q, cur, prev)
			}
			prev = cur
		}
	}
	// 无损格式的滑块是"压缩强度"，数值越大压得越狠、体积越小
	for _, id := range []string{"png", "tiff"} {
		f, _ := FormatByID(id)
		prev := int64(1 << 40)
		for q := 10; q <= 100; q += 10 {
			cur := EstimateBytes(f, q, w, h, "keep")
			if cur > prev {
				t.Errorf("%s 压缩强度 %d 的预估体积 %d 大于更低强度的 %d", id, q, cur, prev)
			}
			prev = cur
		}
	}
	if EstimateBytes(f, 80, 0, 0, "keep") != 0 {
		t.Error("尺寸未知时体积预估应为 0")
	}
	gray := EstimateBytes(f, 80, w, h, "grayscale")
	color := EstimateBytes(f, 80, w, h, "keep")
	if gray >= color {
		t.Errorf("灰度图预估体积应小于彩色图: %d vs %d", gray, color)
	}
}

func TestFormatLookup(t *testing.T) {
	if _, ok := FormatByID("JPG"); !ok {
		t.Error("格式查找应忽略大小写")
	}
	if _, ok := FormatByID("nope"); ok {
		t.Error("不存在的格式应查找失败")
	}
	if DefaultFormatID("nope") != "webp" {
		t.Error("非法默认格式应回退 webp")
	}
	if f, _ := FormatByID("png"); f.OutputMuxer(false) != "image2" || f.OutputMuxer(true) != "apng" {
		t.Error("PNG 静态走 image2、动图走 apng 封装")
	}
	if f, _ := FormatByID("heic"); f.OutputMuxer(false) != "heic" {
		t.Error("HEIC 默认封装应为 heic")
	}
}

// ==================== 依赖 ffmpeg 的集成测试 ====================

func TestDetect(t *testing.T) {
	c := newTestClient(t)
	caps := c.Detect(context.Background())
	if caps.FFmpeg == "" {
		t.Fatal("未探测到 ffmpeg 版本")
	}
	if len(caps.Encoders) == 0 {
		t.Fatal("未探测到任何编码器")
	}
	// 基础格式在任意常规 FFmpeg 上都应可用
	for _, id := range []string{"jpg", "png"} {
		sup := caps.FormatSupportFor(id)
		if sup == nil {
			t.Fatalf("能力列表缺少格式 %s", id)
		}
		if !sup.Available {
			t.Errorf("格式 %s 应默认可用，实际不可用：%s", id, sup.Reason)
		}
	}
	// 不可用格式必须带上原因，方便界面提示
	for _, sup := range caps.Formats {
		if !sup.Available && sup.Reason == "" {
			t.Errorf("格式 %s 不可用但没有给出原因", sup.Format.ID)
		}
	}
	// 能力结果应被缓存
	if again := c.Detect(context.Background()); again != caps {
		t.Error("能力探测结果应当缓存复用")
	}
}

func TestProbe(t *testing.T) {
	c := newTestClient(t)
	src := makeFixture(t, c, "probe.png", "testsrc2=s=640x480:d=1", "")

	info, err := c.Probe(context.Background(), src)
	if err != nil {
		t.Fatalf("探测失败: %v", err)
	}
	if info.Width != 640 || info.Height != 480 {
		t.Errorf("尺寸 = %dx%d, 期望 640x480", info.Width, info.Height)
	}
	if info.MimeType != "image/png" {
		t.Errorf("MIME = %s, 期望 image/png", info.MimeType)
	}
	if info.OrientationLabel != "横图" {
		t.Errorf("方向描述 = %s, 期望 横图", info.OrientationLabel)
	}
	if info.Animated {
		t.Error("静态图不应被识别为动图")
	}
	if info.Size <= 0 {
		t.Error("文件体积应大于 0")
	}
	if info.Aspect < 1.33 || info.Aspect > 1.34 {
		t.Errorf("宽高比 = %.3f, 期望约 1.333", info.Aspect)
	}
}

func TestProbeAlphaDetection(t *testing.T) {
	c := newTestClient(t)
	// color=c=red@0.5 带 alpha 通道
	src := makeFixture(t, c, "alpha.png", "color=c=red@0.5:s=200x100:d=1,format=rgba", "")
	info, err := c.Probe(context.Background(), src)
	if err != nil {
		t.Fatalf("探测失败: %v", err)
	}
	if !info.Alpha {
		t.Errorf("rgba 素材应识别出透明通道，实际 pix_fmt=%s", info.PixFmt)
	}
}

// TestConvertFormats 遍历本机所有可用格式做一次真实转换，确保命令构建对每种编码器都成立。
func TestConvertFormats(t *testing.T) {
	c := newTestClient(t)
	caps := c.Detect(context.Background())
	src := makeFixture(t, c, "src.png", "testsrc2=s=320x240:d=1", "")
	ctx := context.Background()

	for _, sup := range caps.Formats {
		if !sup.Available {
			t.Logf("跳过 %s：%s", sup.Format.Name, sup.Reason)
			continue
		}
		sup := sup
		t.Run(sup.Format.ID, func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "out"+sup.Format.Ext)
			o := DefaultOptions(sup.Format.ID)
			clamped, err := Clamp(o, caps)
			if err != nil {
				t.Fatalf("参数归一化失败: %v", err)
			}
			if err := c.RunConvert(ctx, src, out, *clamped, caps, 0, nil); err != nil {
				t.Fatalf("转换为 %s 失败: %v", sup.Format.Name, err)
			}
			st, err := os.Stat(out)
			if err != nil || st.Size() == 0 {
				t.Fatalf("输出文件无效: %v", err)
			}
			// 输出应能被 ffprobe 重新读出，且尺寸与源图一致
			info, err := c.Probe(ctx, out)
			if err != nil {
				t.Logf("输出 %s 无法被 ffprobe 解析（ffmpeg 能力限制）: %v", sup.Format.Name, err)
				return
			}
			if info.Width != 320 || info.Height != 240 {
				t.Errorf("输出尺寸 = %dx%d, 期望 320x240", info.Width, info.Height)
			}
		})
	}
}

func TestConvertResizeKeepsAspect(t *testing.T) {
	c := newTestClient(t)
	caps := c.Detect(context.Background())
	if sup := caps.FormatSupportFor("jpg"); sup == nil || !sup.Available {
		t.Skip("本机不支持 JPEG 输出")
	}
	src := makeFixture(t, c, "src.png", "testsrc2=s=1600x1200:d=1", "")
	out := filepath.Join(t.TempDir(), "out.jpg")

	o := DefaultOptions("jpg")
	o.Resize, o.Size = "long_edge", 800
	clamped, err := Clamp(o, caps)
	if err != nil {
		t.Fatalf("参数归一化失败: %v", err)
	}
	if err := c.RunConvert(context.Background(), src, out, *clamped, caps, 0, nil); err != nil {
		t.Fatalf("转换失败: %v", err)
	}
	info, err := c.Probe(context.Background(), out)
	if err != nil {
		t.Fatalf("探测输出失败: %v", err)
	}
	if info.Width != 800 || info.Height != 600 {
		t.Fatalf("输出尺寸 = %dx%d, 期望 800x600", info.Width, info.Height)
	}
}

func TestConvertRotate(t *testing.T) {
	c := newTestClient(t)
	caps := c.Detect(context.Background())
	src := makeFixture(t, c, "src.png", "testsrc2=s=1600x1200:d=1", "")
	out := filepath.Join(t.TempDir(), "out.png")

	o := DefaultOptions("png")
	o.Rotate = 90
	clamped, _ := Clamp(o, caps)
	if err := c.RunConvert(context.Background(), src, out, *clamped, caps, 0, nil); err != nil {
		t.Fatalf("转换失败: %v", err)
	}
	info, err := c.Probe(context.Background(), out)
	if err != nil {
		t.Fatalf("探测输出失败: %v", err)
	}
	if info.Width != 1200 || info.Height != 1600 {
		t.Fatalf("旋转 90 度后尺寸 = %dx%d, 期望 1200x1600", info.Width, info.Height)
	}
}

func TestConvertExactFillCrops(t *testing.T) {
	c := newTestClient(t)
	caps := c.Detect(context.Background())
	src := makeFixture(t, c, "src.png", "testsrc2=s=1600x1200:d=1", "")
	out := filepath.Join(t.TempDir(), "out.jpg")

	o := DefaultOptions("jpg")
	o.Resize, o.CustomWidth, o.CustomHeight, o.Adapt = "exact", 800, 800, "fill"
	clamped, _ := Clamp(o, caps)
	if err := c.RunConvert(context.Background(), src, out, *clamped, caps, 0, nil); err != nil {
		t.Fatalf("转换失败: %v", err)
	}
	info, err := c.Probe(context.Background(), out)
	if err != nil {
		t.Fatalf("探测输出失败: %v", err)
	}
	if info.Width != 800 || info.Height != 800 {
		t.Fatalf("填满裁剪后尺寸 = %dx%d, 期望 800x800", info.Width, info.Height)
	}
}

// TestConvertAlphaCompositedOntoBackground 验证透明图转 JPEG 时确实合成到了白底，
// 而不是直接丢掉 alpha 通道露出黑色。
func TestConvertAlphaCompositedOntoBackground(t *testing.T) {
	c := newTestClient(t)
	caps := c.Detect(context.Background())
	if sup := caps.FormatSupportFor("jpg"); sup == nil || !sup.Available {
		t.Skip("本机不支持 JPEG 输出")
	}
	// 50% 透明度的纯红
	src := makeFixture(t, c, "alpha.png", "color=c=red@0.5:s=200x100:d=1,format=rgba", "")
	out := filepath.Join(t.TempDir(), "out.jpg")

	o := DefaultOptions("jpg")
	o.Background = "white"
	clamped, err := Clamp(o, caps)
	if err != nil {
		t.Fatalf("参数归一化失败: %v", err)
	}
	clamped.SourceWidth, clamped.SourceHeight = 200, 100
	clamped.SourceAlpha = true
	clamped, err = Clamp(*clamped, caps)
	if err != nil {
		t.Fatalf("参数归一化失败: %v", err)
	}
	if err := c.RunConvert(context.Background(), src, out, *clamped, caps, 0, nil); err != nil {
		t.Fatalf("转换失败: %v", err)
	}

	img, err := decodeJPEG(out)
	if err != nil {
		t.Fatalf("解码输出失败: %v", err)
	}
	r, g, b, _ := img.At(100, 50).RGBA()
	// 白底 + 50% 红 ≈ (255, 128, 128)；若只是丢掉 alpha 会得到 (255, 0, 0)。
	// JPEG 有损，R 通道允许偏差，但 G/B 必须落在"半红"区间而非接近 0。
	if g>>8 < 90 || g>>8 > 170 || b>>8 < 90 || b>>8 > 170 {
		t.Fatalf("中心像素 = (%d,%d,%d)，期望白底混合后的浅红（G/B 约 128）", r>>8, g>>8, b>>8)
	}
	if r>>8 < 200 || r>>8 <= g>>8+40 {
		t.Fatalf("中心像素 = (%d,%d,%d)，R 通道应明显高于 G/B", r>>8, g>>8, b>>8)
	}
}

func TestConvertGrayscale(t *testing.T) {
	c := newTestClient(t)
	caps := c.Detect(context.Background())
	src := makeFixture(t, c, "src.png", "testsrc2=s=200x100:d=1", "")
	out := filepath.Join(t.TempDir(), "out.jpg")

	o := DefaultOptions("jpg")
	o.ColorMode = "grayscale"
	clamped, _ := Clamp(o, caps)
	if err := c.RunConvert(context.Background(), src, out, *clamped, caps, 0, nil); err != nil {
		t.Fatalf("转换失败: %v", err)
	}
	img, err := decodeJPEG(out)
	if err != nil {
		t.Fatalf("解码输出失败: %v", err)
	}
	r, g, b, _ := img.At(100, 50).RGBA()
	if diff(r, g) > 12 || diff(g, b) > 12 {
		t.Fatalf("灰度图像素 = (%d,%d,%d)，三通道应接近", r>>8, g>>8, b>>8)
	}
}

func TestConvertWatermark(t *testing.T) {
	c := newTestClient(t)
	caps := c.Detect(context.Background())
	src := makeFixture(t, c, "src.png", "color=c=black:s=600x400:d=1", "")
	wm := makeFixture(t, c, "wm.png", "color=c=white:s=200x50:d=1", "")
	out := filepath.Join(t.TempDir(), "out.jpg")

	o := DefaultOptions("jpg")
	o.Watermark = WatermarkOptions{
		Enabled:  true,
		Path:     wm,
		Position: "bottom_right",
		Margin:   50,
		Width:    200,
		Opacity:  100,
	}
	clamped, _ := Clamp(o, caps)
	if err := c.RunConvert(context.Background(), src, out, *clamped, caps, 0, nil); err != nil {
		t.Fatalf("转换失败: %v", err)
	}
	img, err := decodeJPEG(out)
	if err != nil {
		t.Fatalf("解码输出失败: %v", err)
	}
	// 水印矩形区域：x ∈ [600-50-200, 600-50) = [350, 550)，y ∈ [300, 350)
	center := img.At(450, 325)
	_, g, _, _ := center.RGBA()
	if g>>8 < 200 {
		t.Errorf("水印区域应接近白色，实际灰度 %d", g>>8)
	}
	corner, _, _, _ := img.At(10, 10).RGBA()
	if corner>>8 > 60 {
		t.Errorf("未覆盖区域应保持黑色，实际 %d", corner>>8)
	}
}

func TestConvertAnimated(t *testing.T) {
	c := newTestClient(t)
	caps := c.Detect(context.Background())
	sup := caps.FormatSupportFor("gif")
	if sup == nil || !sup.AnimatedAvailable {
		t.Skip("本机不支持 GIF 动图输出")
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "anim.png")
	args := []string{"-hide_banner", "-loglevel", "error", "-f", "lavfi",
		"-i", "testsrc2=s=160x120:d=1:r=10", "-c:v", "apng", "-frames:v", "6", "-f", "apng", "-y", src}
	if _, err := runLimited(context.Background(), 60*time.Second, c.ffmpegBin, args...); err != nil {
		t.Skipf("无法生成 APNG 素材: %v", err)
	}
	out := filepath.Join(dir, "out.gif")

	o := DefaultOptions("gif")
	o.Animated = true
	clamped, err := Clamp(o, caps)
	if err != nil {
		t.Fatalf("参数归一化失败: %v", err)
	}
	if err := c.RunConvert(context.Background(), src, out, *clamped, caps, time.Second, nil); err != nil {
		t.Fatalf("动图转换失败: %v", err)
	}
	info, err := c.Probe(context.Background(), out)
	if err != nil {
		t.Fatalf("探测输出失败: %v", err)
	}
	if !info.Animated {
		t.Errorf("输出应为多帧 GIF，实际帧数 %d", info.Frames)
	}
	if info.Width != 160 || info.Height != 120 {
		t.Errorf("输出尺寸 = %dx%d, 期望 160x120", info.Width, info.Height)
	}
}

func TestClampRejectsUnavailableFormat(t *testing.T) {
	caps := &Capabilities{Formats: []FormatSupport{{
		Format:    Format{ID: "jxl", Name: "JPEG XL"},
		Available: false,
		Reason:    "当前 FFmpeg 未编译 libjxl",
	}}}
	o := DefaultOptions("jxl")
	if _, err := Clamp(o, caps); err == nil {
		t.Fatal("不可用格式应被拒绝")
	}
	// 动图开关配合不支持动图的格式也应报错
	o = DefaultOptions("bmp")
	o.Animated = true
	if _, err := Clamp(o, nil); err == nil {
		t.Fatal("BMP 不应允许动图输出")
	}
}

func TestBuildConvertArgsShape(t *testing.T) {
	c := newTestClient(t)
	caps := c.Detect(context.Background())

	// 源图尺寸未知时，缩放改用表达式，保证不会把缩放悄悄丢掉
	o := DefaultOptions("jpg")
	o.Resize, o.Size = "long_edge", 1600
	o.Sharpen, o.Brightness = 30, 10
	clamped, _ := Clamp(o, caps)
	args, err := c.BuildConvertArgs(*clamped, caps, "/data/in.png", "/data/out.jpg")
	if err != nil {
		t.Fatalf("构建命令失败: %v", err)
	}
	cmd := CommandLine("ffmpeg", args)
	for _, want := range []string{"-i /data/in.png", "gte(iw", "unsharp=", "eq=", "/data/out.jpg"} {
		if !bytes.Contains([]byte(cmd), []byte(want)) {
			t.Errorf("命令缺少 %q：%s", want, cmd)
		}
	}
	// 不允许放大时不应插入 -noautorotate（默认开启自动摆正）
	if bytes.Contains([]byte(cmd), []byte("-noautorotate")) {
		t.Errorf("默认应开启 EXIF 自动摆正：%s", cmd)
	}
	o.AutoOrient = false
	args, _ = c.BuildConvertArgs(o, caps, "/data/in.png", "/data/out.jpg")
	if !bytes.Contains([]byte(CommandLine("ffmpeg", args)), []byte("-noautorotate")) {
		t.Error("关闭自动摆正时应加上 -noautorotate")
	}

	// 源图尺寸已知时使用确定的数值尺寸，便于预估与展示
	o = DefaultOptions("jpg")
	o.Resize, o.Size = "long_edge", 1600
	clamped, _ = Clamp(o, caps)
	clamped.SourceWidth, clamped.SourceHeight = 3200, 2400
	clamped, _ = Clamp(*clamped, caps)
	args, err = c.BuildConvertArgs(*clamped, caps, "/data/in.png", "/data/out.jpg")
	if err != nil {
		t.Fatalf("构建命令失败: %v", err)
	}
	cmd = CommandLine("ffmpeg", args)
	if !bytes.Contains([]byte(cmd), []byte("scale=1600:1200")) {
		t.Errorf("已知尺寸时应生成确定的 scale 滤镜：%s", cmd)
	}
}

func TestThumbnail(t *testing.T) {
	c := newTestClient(t)
	src := makeFixture(t, c, "src.png", "testsrc2=s=1200x800:d=1", "")
	data, err := c.ThumbnailBytes(context.Background(), src, 128)
	if err != nil {
		t.Fatalf("生成缩略图失败: %v", err)
	}
	img, err := jpeg.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("缩略图无法解码: %v", err)
	}
	if img.Bounds().Dx() > 128 || img.Bounds().Dy() > 128 {
		t.Errorf("缩略图尺寸 = %v, 不应超过 128", img.Bounds().Size())
	}
	if img.Bounds().Dx() == 0 {
		t.Error("缩略图宽度为 0")
	}
}

func TestProbeFallbackErrors(t *testing.T) {
	c := newTestClient(t)
	bogus := filepath.Join(t.TempDir(), "not-an-image.png")
	if err := os.WriteFile(bogus, []byte("this is not an image"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Probe(context.Background(), bogus); err == nil {
		t.Error("非图片文件探测应失败")
	}
}

func TestRunConvertReportsMissingSource(t *testing.T) {
	c := newTestClient(t)
	caps := c.Detect(context.Background())
	out := filepath.Join(t.TempDir(), "out.jpg")
	o := DefaultOptions("jpg")
	clamped, _ := Clamp(o, caps)
	err := c.RunConvert(context.Background(), filepath.Join(t.TempDir(), "missing.png"), out, *clamped, caps, 0, nil)
	if err == nil {
		t.Fatal("源文件缺失时应报错")
	}
	// 失败时不应留下空的输出文件
	if _, statErr := os.Stat(out); statErr == nil {
		t.Error("转换失败后不应残留输出文件")
	}
}

// ==================== 辅助 ====================

func decodeJPEG(path string) (image.Image, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return jpeg.Decode(bytes.NewReader(data))
}

func diff(a, b uint32) int {
	if a > b {
		return int(a - b)
	}
	return int(b - a)
}

// TestPixFmtColorRangeForHardwareEncoder 回归测试：
// 部分硬件编码器（实测 mjpeg_qsv）不理会输入色彩范围，写出的 yuvj* 像素
// 始终是 limited range 取值（白底 255 → 235），必须显式声明 -color_range pc。
func TestPixFmtColorRangeForHardwareEncoder(t *testing.T) {
	jpg, _ := FormatByID("jpg")
	o := DefaultOptions("jpg")

	// 硬件编码器 + yuvj 像素格式 → 必须带上 -color_range pc
	args := pixFmtArgs(jpg, &o, "mjpeg_qsv")
	if !containsPair(args, "-color_range", "pc") {
		t.Errorf("mjpeg_qsv 缺少 -color_range pc: %v", args)
	}
	// 显式像素格式仍需保留
	if !containsPair(args, "-pix_fmt", "yuvj420p") {
		t.Errorf("缺少 -pix_fmt yuvj420p: %v", args)
	}
	for _, enc := range []string{"mjpeg_nvenc", "mjpeg_vaapi", "hevc_qsv"} {
		if enc == "hevc_qsv" {
			continue // yuv420p 是 limited range，不应加 pc
		}
		if args := pixFmtArgs(jpg, &o, enc); !containsPair(args, "-color_range", "pc") {
			t.Errorf("%s 缺少 -color_range pc: %v", enc, args)
		}
	}

	// 软件编码器不需要（软件路径本来就能正确转换范围）
	if args := pixFmtArgs(jpg, &o, "mjpeg"); containsPair(args, "-color_range", "pc") {
		t.Errorf("软件编码器不应带 -color_range pc: %v", args)
	}

	// limited range 的像素格式（yuv420p）不应加 pc，否则会整体偏亮
	heic, _ := FormatByID("heic")
	ho := DefaultOptions("heic")
	if args := pixFmtArgs(heic, &ho, "hevc_qsv"); containsPair(args, "-color_range", "pc") {
		t.Errorf("yuv420p 不应带 -color_range pc: %v", args)
	}

	// 灰度与原生 RGB 格式不受影响
	og := DefaultOptions("jpg")
	og.ColorMode = "grayscale"
	if args := pixFmtArgs(jpg, &og, "mjpeg_qsv"); containsPair(args, "-color_range", "pc") {
		t.Errorf("灰度输出不应带 -color_range pc: %v", args)
	}
	png, _ := FormatByID("png")
	op := DefaultOptions("png")
	if args := pixFmtArgs(png, &op, "libwebp_qsv"); len(args) != 0 {
		t.Errorf("PNG 输出不应带像素格式参数: %v", args)
	}
}

// TestConvertAlphaCompositedPureWhite 校验完全透明的图合成到白底后是纯白（255），
// 而不是 limited range 的 235。
func TestConvertAlphaCompositedPureWhite(t *testing.T) {
	c := newTestClient(t)
	caps := c.Detect(context.Background())
	sup := caps.FormatSupportFor("jpg")
	if sup == nil || !sup.Available {
		t.Skip("本机不支持 JPEG 输出")
	}

	for _, accel := range []string{AccelNone, AccelQSV} {
		if accel != AccelNone {
			if _, ok := caps.HWFormats["jpg"]; !ok {
				t.Logf("跳过 %s：本机 JPEG 无实测可用的硬件编码器", accel)
				continue
			}
		}
		// 完全透明的 120x80 图
		src := makeFixture(t, c, "clear.png", "color=c=black@0.0:s=120x80:d=1,format=rgba", "")
		out := filepath.Join(t.TempDir(), "white.jpg")

		o := DefaultOptions("jpg")
		o.Accel = accel
		o.Background = "white"
		clamped, err := Clamp(o, caps)
		if err != nil {
			t.Fatalf("参数归一化失败: %v", err)
		}
		clamped.SourceWidth, clamped.SourceHeight, clamped.SourceAlpha = 120, 80, true
		clamped, err = Clamp(*clamped, caps)
		if err != nil {
			t.Fatalf("参数归一化失败: %v", err)
		}
		if err := c.RunConvert(context.Background(), src, out, *clamped, caps, 0, nil); err != nil {
			t.Fatalf("[%s] 转换失败: %v", accel, err)
		}
		img, err := decodeJPEG(out)
		if err != nil {
			t.Fatalf("[%s] 解码输出失败: %v", accel, err)
		}
		r, g, b, _ := img.At(60, 40).RGBA()
		for _, v := range []uint32{r, g, b} {
			if v>>8 < 250 {
				t.Errorf("[%s] 透明图合成白底后中心像素 = (%d,%d,%d)，期望接近纯白 255"+
					"（235 说明硬件编码器写成了 limited range）", accel, r>>8, g>>8, b>>8)
				break
			}
		}
	}
}

func containsPair(args []string, key, val string) bool {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == key && args[i+1] == val {
			return true
		}
	}
	return false
}
