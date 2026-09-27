package service

import (
	"context"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/meimolihan/fan-image-tr/internal/config"
	"github.com/meimolihan/fan-image-tr/internal/ffmpeg"
)

// testConfig 构造指向临时目录的配置。数据目录与浏览目录分开，
// 避免 data/ 出现在目录浏览结果里干扰断言。
func testConfig(t *testing.T) *config.Config {
	t.Helper()
	dir := t.TempDir()
	cfg := &config.Config{}
	cfg.App.DataDir = filepath.Join(t.TempDir(), "data")
	cfg.App.MediaDir = dir
	cfg.App.OutputDir = "output"
	cfg.App.UploadDir = "uploads"
	cfg.App.Worker = 2
	cfg.FFmpeg.Path = "ffmpeg"
	cfg.FFmpeg.FFprobePath = "ffprobe"
	cfg.FFmpeg.Accel = "none"
	cfg.FFmpeg.DetectAccel = false
	if err := os.MkdirAll(cfg.App.DataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{cfg.OutputDir(), cfg.UploadDir()} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("创建目录失败: %v", err)
		}
	}
	return cfg
}

func newTestMedia(t *testing.T, cfg *config.Config) *MediaService {
	t.Helper()
	ff, err := ffmpeg.New(ffmpeg.Options{FFmpegBin: "ffmpeg", FFprobeBin: "ffprobe", Accel: "none"})
	if err != nil {
		t.Skipf("跳过：未找到 ffmpeg（%v）", err)
	}
	return NewMediaService(cfg, ff, zap.NewNop())
}

// writeTestPNG 写一张指定尺寸的测试图片。
func writeTestPNG(t *testing.T, path string, w, h int) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x % 256), G: uint8(y % 256), B: 128, A: 255})
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
}

func TestResolvePathSecurity(t *testing.T) {
	cfg := testConfig(t)
	media := newTestMedia(t, cfg)
	home := cfg.HomeDir()

	if got, err := media.ResolvePath(""); err != nil || got != home {
		t.Fatalf("空路径应解析为根目录: got=%q err=%v", got, err)
	}
	sub := filepath.Join(home, "a", "b")
	if got, err := media.ResolvePath("a/b"); err != nil || got != sub {
		t.Fatalf("相对路径解析错误: got=%q want=%q err=%v", got, sub, err)
	}
	if got, err := media.ResolvePath(sub); err != nil || got != sub {
		t.Fatalf("根目录内的绝对路径应可访问: got=%q err=%v", got, err)
	}

	// 目录穿越必须被拒绝
	// 注意："~" 是合法的相对文件名（<根目录>/~），不算穿越
	for _, bad := range []string{"../etc/passwd", "a/../../..", "/etc/passwd", "a/b/../../../..", "sub/../../outside"} {
		if _, err := media.ResolvePath(bad); err == nil {
			t.Errorf("路径穿越未被拒绝: %q", bad)
		}
	}

	// 上传目录与输出目录在允许范围内（即使它们位于浏览根目录之外）
	outFile := filepath.Join(cfg.OutputDir(), "x.webp")
	if err := os.WriteFile(outFile, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := media.ResolvePath(outFile); err != nil || got != outFile {
		t.Fatalf("输出目录内的文件应可访问: got=%q err=%v", got, err)
	}
}

func TestRelPath(t *testing.T) {
	cfg := testConfig(t)
	media := newTestMedia(t, cfg)
	home := cfg.HomeDir()

	if got := media.RelPath(home); got != "" {
		t.Errorf("根目录应返回空串: %q", got)
	}
	want := "sub/dir/a.png"
	if got := media.RelPath(filepath.Join(home, "sub", "dir", "a.png")); got != want {
		t.Errorf("相对路径错误: got=%q want=%q", got, want)
	}
	// 根目录之外返回绝对路径
	if got := media.RelPath("/tmp/somewhere.png"); got != "/tmp/somewhere.png" {
		t.Errorf("根目录外应返回绝对路径: %q", got)
	}
}

func TestMediaList(t *testing.T) {
	cfg := testConfig(t)
	media := newTestMedia(t, cfg)
	home := cfg.HomeDir()

	writeTestPNG(t, filepath.Join(home, "a.png"), 8, 8)
	writeTestPNG(t, filepath.Join(home, "b.JPG"), 8, 8)
	writeTestPNG(t, filepath.Join(home, "sub", "c.png"), 8, 8)
	if err := os.WriteFile(filepath.Join(home, "notes.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".hidden.png"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	list, err := media.List("")
	if err != nil {
		t.Fatalf("列目录失败: %v", err)
	}
	if list.Count != 2 {
		t.Errorf("应过滤出 2 张图片（.txt/.hidden 排除，大小写不敏感）: got=%d %+v", list.Count, list.Files)
	}
	if len(list.Dirs) != 1 || list.Dirs[0].Name != "sub" {
		t.Errorf("子目录列表错误: %+v", list.Dirs)
	}
	if list.Parent != "" {
		t.Errorf("根目录的 Parent 应为空: %q", list.Parent)
	}
	// 大写扩展名也应识别
	for _, f := range list.Files {
		if f.Name == "b.JPG" && f.Ext != ".jpg" {
			t.Errorf("扩展名应小写: %q", f.Ext)
		}
	}

	// 子目录的 Parent 应指向根
	sub, err := media.List("sub")
	if err != nil {
		t.Fatal(err)
	}
	if sub.Parent != "" || sub.Path != "sub" {
		t.Errorf("子目录信息错误: path=%q parent=%q", sub.Path, sub.Parent)
	}
}

func TestUniqueName(t *testing.T) {
	cfg := testConfig(t)
	media := newTestMedia(t, cfg)
	svc := NewTaskService(cfg, media, nil, zap.NewNop())
	dir := t.TempDir()

	if got := svc.uniqueName(dir, "photo", "", ".webp", false); got != "photo.webp" {
		t.Errorf("首次应使用原名: %q", got)
	}
	if err := os.WriteFile(filepath.Join(dir, "photo.webp"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := svc.uniqueName(dir, "photo", "", ".webp", false); got != "photo_1.webp" {
		t.Errorf("重名应追加序号: %q", got)
	}
	if got := svc.uniqueName(dir, "photo", "small", ".webp", false); got != "photo_small.webp" {
		t.Errorf("变体文件名错误: %q", got)
	}
	if got := svc.uniqueName(dir, "photo", "", ".webp", true); got != "photo.webp" {
		t.Errorf("覆盖模式应保持原名: %q", got)
	}
}

func TestNormalizeVariants(t *testing.T) {
	if got, err := normalizeVariants(nil); err != nil || got != nil {
		t.Errorf("空变体应返回 nil: %v %v", got, err)
	}

	ok := []Variant{{Name: "小图", Resize: "long_edge", Size: 320}}
	if got, err := normalizeVariants(ok); err != nil || len(got) != 1 || got[0].Name != "小图" {
		t.Errorf("合法变体应通过: %+v %v", got, err)
	}

	bad := [][]Variant{
		{{Name: ""}},
		{{Name: "a"}, {Name: "a"}},
		{{Name: "精确", Resize: "exact"}},
		{{Name: "非法", Resize: "nope"}},
		{{Name: "非法", Adapt: "nope"}},
		{{Name: "非法", Format: "nope"}},
		{{Name: string(make([]byte, 30))}},
	}
	for i, v := range bad {
		if _, err := normalizeVariants(v); err == nil {
			t.Errorf("用例 %d 应校验失败: %+v", i, v)
		}
	}

	var many []Variant
	for i := 0; i < 9; i++ {
		many = append(many, Variant{Name: string(rune('a' + i))})
	}
	if _, err := normalizeVariants(many); err == nil {
		t.Error("超过 8 个变体应报错")
	}
}

func TestSanitizeTag(t *testing.T) {
	cases := map[string]string{
		"小图":         "小图",
		"Small":      "Small",
		"my variant": "my-variant",
		"a/b":        "ab",
		"a\\b":       "ab",
		"..":         "v", // 全部非法字符时使用兜底
	}
	for in, want := range cases {
		got := sanitizeTag(in)
		if in == ".." {
			if got == "" {
				t.Errorf("非法名称应使用兜底值: %q", got)
			}
			continue
		}
		if got != want {
			t.Errorf("sanitizeTag(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPresetService(t *testing.T) {
	cfg := testConfig(t)
	svc, err := NewPresetService(cfg)
	if err != nil {
		t.Fatalf("创建预设服务失败: %v", err)
	}

	list := svc.List()
	if len(list) < 5 {
		t.Fatalf("内置预设数量异常: %d", len(list))
	}
	if !list[0].BuiltIn {
		t.Error("内置预设应排在最前")
	}

	// 内置预设不可覆盖 / 删除
	if err := svc.Save(&Preset{Name: "网页通用 WebP", Options: ffmpeg.ConvertOptions{Format: "webp", Quality: 50, Resize: "keep"}}); err == nil {
		t.Error("不应允许覆盖内置预设")
	}
	if err := svc.Delete("网页通用 WebP"); err == nil {
		t.Error("不应允许删除内置预设")
	}

	// 新增用户预设
	np := &Preset{
		Name:     "我的预设",
		Options:  ffmpeg.ConvertOptions{Format: "jpg", Quality: 88, Resize: "long_edge", Size: 1600, ColorMode: "keep"},
		Variants: []Variant{{Name: "小图", Resize: "long_edge", Size: 400}},
	}
	if err := svc.Save(np); err != nil {
		t.Fatalf("保存预设失败: %v", err)
	}
	got, ok := svc.Get("我的预设")
	if !ok || !got.Options.AutoOrient == false {
		t.Errorf("读取预设失败: %+v", got)
	}
	if len(got.Variants) != 1 {
		t.Errorf("变体未保存: %+v", got.Variants)
	}

	// 落盘后重新加载应保留
	svc2, err := NewPresetService(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := svc2.Get("我的预设"); !ok {
		t.Error("重启后预设丢失")
	}
	if len(svc2.List()) <= len(list) {
		t.Error("重新加载后预设数量异常")
	}

	// 非法预设应被拒绝
	badPresets := []*Preset{
		{Name: "x1", Options: ffmpeg.ConvertOptions{Format: "nope", Resize: "keep"}},
		{Name: "x2", Options: ffmpeg.ConvertOptions{Format: "jpg", Quality: 0, Resize: "keep"}},
		{Name: "x3", Options: ffmpeg.ConvertOptions{Format: "jpg", Quality: 50, Resize: "nope"}},
		{Name: "x4", Options: ffmpeg.ConvertOptions{Format: "jpg", Quality: 50, Resize: "exact"}},
		{Name: "x5", Options: ffmpeg.ConvertOptions{Format: "jpg", Quality: 50, Resize: "long_edge"}},
		{Name: "a/b", Options: ffmpeg.ConvertOptions{Format: "jpg", Quality: 50, Resize: "keep"}},
		{Name: "  ", Options: ffmpeg.ConvertOptions{Format: "jpg", Quality: 50, Resize: "keep"}},
	}
	for _, bp := range badPresets {
		if err := svc.Save(bp); err == nil {
			t.Errorf("非法预设应被拒绝: %+v", bp)
		}
	}

	// 删除
	if err := svc.Delete("我的预设"); err != nil {
		t.Errorf("删除预设失败: %v", err)
	}
	if _, ok := svc.Get("我的预设"); ok {
		t.Error("删除后仍能查到")
	}
	if err := svc.Delete("我的预设"); err == nil {
		t.Error("重复删除应报错")
	}
}

func TestSizeEstimate(t *testing.T) {
	est, err := Estimate(ffmpeg.ConvertOptions{Format: "jpg", Quality: 80, Resize: "long_edge", Size: 1000}, 4000, 3000)
	if err != nil {
		t.Fatalf("预估失败: %v", err)
	}
	if est.Width != 1000 || est.Height != 750 {
		t.Errorf("预估尺寸错误: %dx%d", est.Width, est.Height)
	}
	if est.Bytes <= 0 || est.Text == "" {
		t.Errorf("预估体积无效: %+v", est)
	}
	if est.MimeType != "image/jpeg" {
		t.Errorf("MIME 错误: %s", est.MimeType)
	}
}

func TestTaskCreateValidation(t *testing.T) {
	cfg := testConfig(t)
	media := newTestMedia(t, cfg)
	svc := NewTaskService(cfg, media, media.ff, zap.NewNop())
	ctx := context.Background()

	if _, err := svc.Create(ctx, TaskSpec{}); err == nil {
		t.Error("空输入应报错")
	}
	if _, err := svc.Create(ctx, TaskSpec{Inputs: []string{"nope.png"}}); err == nil {
		t.Error("不存在的文件应报错")
	}
	if _, err := svc.Create(ctx, TaskSpec{Inputs: []string{".."}}); err == nil {
		t.Error("目录穿越应被拒绝")
	}
	// 目录而非图片
	if err := os.MkdirAll(filepath.Join(cfg.HomeDir(), "d"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Create(ctx, TaskSpec{Inputs: []string{"d"}}); err == nil {
		t.Error("目录应被拒绝")
	}
	// 非图片文件
	if err := os.WriteFile(filepath.Join(cfg.HomeDir(), "a.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Create(ctx, TaskSpec{Inputs: []string{"a.txt"}}); err == nil {
		t.Error("非图片应被拒绝")
	}
	// 超过 500 张
	var many []string
	for i := 0; i < 501; i++ {
		many = append(many, "x.png")
	}
	if _, err := svc.Create(ctx, TaskSpec{Inputs: many}); err == nil {
		t.Error("超过数量上限应报错")
	}
}

func TestTaskLifecycle(t *testing.T) {
	cfg := testConfig(t)
	media := newTestMedia(t, cfg)
	svc := NewTaskService(cfg, media, media.ff, zap.NewNop())
	svc.Start()
	defer svc.Stop()

	ctx := context.Background()
	writeTestPNG(t, filepath.Join(cfg.HomeDir(), "src.png"), 600, 400)

	tasks, err := svc.Create(ctx, TaskSpec{
		Inputs:  []string{"src.png"},
		Options: ffmpeg.ConvertOptions{Format: "webp", Quality: 80, Resize: "long_edge", Size: 300, ColorMode: "keep"},
	})
	if err != nil {
		t.Fatalf("创建任务失败: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("应创建 1 个任务: %d", len(tasks))
	}
	task := tasks[0]
	if task.Status != StatusQueued {
		t.Errorf("初始状态应为 queued: %s", task.Status)
	}
	if task.SourceWidth != 600 || task.SourceHeight != 400 {
		t.Errorf("源图尺寸未探测: %dx%d", task.SourceWidth, task.SourceHeight)
	}
	if len(task.Outputs) != 1 {
		t.Fatalf("应规划 1 个产物: %+v", task.Outputs)
	}
	out := task.Outputs[0]
	if out.Name != "src.webp" {
		t.Errorf("产物文件名错误: %s", out.Name)
	}
	if out.Width != 300 || out.Height != 200 {
		t.Errorf("产物尺寸错误: %dx%d", out.Width, out.Height)
	}
	if out.Estimate <= 0 {
		t.Error("应给出体积预估")
	}

	// 等待任务完成
	final := waitTask(t, svc, task.ID, 60*time.Second)
	if final.Status != StatusCompleted {
		t.Fatalf("任务应完成，实际 %s: %s", final.Status, final.Error)
	}
	if final.Progress != 100 {
		t.Errorf("完成后进度应为 100: %v", final.Progress)
	}
	if final.Command == "" {
		t.Error("应记录 ffmpeg 命令")
	}
	if !final.Outputs[0].Done || final.Outputs[0].Size <= 0 {
		t.Errorf("产物信息错误: %+v", final.Outputs[0])
	}
	abs, err := media.ResolvePath(final.Outputs[0].Path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(abs); err != nil {
		t.Fatalf("产物文件不存在: %v", err)
	}

	// 真实探测产物尺寸，确认缩放生效
	info, err := media.Info(ctx, final.Outputs[0].Path)
	if err != nil {
		t.Fatalf("探测产物失败: %v", err)
	}
	if info.Width != 300 || info.Height != 200 {
		t.Errorf("产物实际尺寸错误: %dx%d", info.Width, info.Height)
	}

	// 统计 / 查询
	if st := svc.Stats(); st.Completed != 1 {
		t.Errorf("统计错误: %+v", st)
	}
	if _, ok := svc.Get(task.ID); !ok {
		t.Error("Get 应能查到任务")
	}
	if _, ok := svc.Get("nope"); ok {
		t.Error("Get 不应查到不存在的任务")
	}
	if len(svc.List()) != 1 {
		t.Errorf("List 应返回 1 条: %d", len(svc.List()))
	}

	// 快照文件应已生成
	snap := filepath.Join(cfg.App.DataDir, "tasks.json")
	if _, err := os.Stat(snap); err != nil {
		t.Errorf("任务快照未落盘: %v", err)
	}

	// 重试（已完成的任务不允许重试）
	if _, err := svc.Retry(ctx, task.ID); err == nil {
		t.Error("已完成任务不应允许重试")
	}

	// 删除记录
	if err := svc.Delete(task.ID); err != nil {
		t.Errorf("删除任务记录失败: %v", err)
	}
	if _, ok := svc.Get(task.ID); ok {
		t.Error("删除后仍能查到")
	}
	if err := svc.Delete(task.ID); err == nil {
		t.Error("重复删除应报错")
	}
}

func TestTaskVariants(t *testing.T) {
	cfg := testConfig(t)
	media := newTestMedia(t, cfg)
	svc := NewTaskService(cfg, media, media.ff, zap.NewNop())
	svc.Start()
	defer svc.Stop()

	ctx := context.Background()
	writeTestPNG(t, filepath.Join(cfg.HomeDir(), "v.png"), 1000, 800)

	tasks, err := svc.Create(ctx, TaskSpec{
		Inputs:  []string{"v.png"},
		Options: ffmpeg.ConvertOptions{Format: "webp", Quality: 80, Resize: "keep", ColorMode: "keep"},
		Variants: []Variant{
			{Name: "小图", Resize: "long_edge", Size: 160},
			{Name: "中图", Resize: "long_edge", Size: 400},
			{Name: "方图", Resize: "exact", CustomWidth: 200, CustomHeight: 200, Adapt: "fill", Format: "jpg", Quality: 90},
		},
	})
	if err != nil {
		t.Fatalf("创建变体任务失败: %v", err)
	}
	if len(tasks[0].Outputs) != 3 {
		t.Fatalf("应规划 3 个产物: %+v", tasks[0].Outputs)
	}
	wantNames := map[string]int{"v_小图.webp": 160, "v_中图.webp": 400, "v_方图.jpg": 200}
	for _, o := range tasks[0].Outputs {
		w, ok := wantNames[o.Name]
		if !ok {
			t.Errorf("产物文件名异常: %s", o.Name)
			continue
		}
		if o.Width != w {
			t.Errorf("%s 宽度应为 %d: %d", o.Name, w, o.Width)
		}
	}

	final := waitTask(t, svc, tasks[0].ID, 90*time.Second)
	if final.Status != StatusCompleted {
		t.Fatalf("任务应完成: %s %s", final.Status, final.Error)
	}
	for _, o := range final.Outputs {
		if !o.Done || o.Size <= 0 {
			t.Errorf("产物未生成: %+v", o)
			continue
		}
		abs, err := media.ResolvePath(o.Path)
		if err != nil {
			t.Errorf("产物路径无法解析: %s", o.Path)
			continue
		}
		if _, err := os.Stat(abs); err != nil {
			t.Errorf("产物缺失: %s", o.Name)
		}
	}
	// 精确裁剪产物应为正方形
	for _, o := range final.Outputs {
		if o.Variant != "方图" {
			continue
		}
		info, err := media.Info(ctx, o.Path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Width != 200 || info.Height != 200 {
			t.Errorf("精确裁剪尺寸错误: %dx%d", info.Width, info.Height)
		}
	}
}

func TestTaskNamingCollision(t *testing.T) {
	cfg := testConfig(t)
	media := newTestMedia(t, cfg)
	svc := NewTaskService(cfg, media, media.ff, zap.NewNop())
	svc.Start()
	defer svc.Stop()

	ctx := context.Background()
	writeTestPNG(t, filepath.Join(cfg.HomeDir(), "n.png"), 40, 30)
	// 预先占用目标文件名
	if err := os.WriteFile(filepath.Join(cfg.OutputDir(), "n.webp"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	tasks, err := svc.Create(ctx, TaskSpec{
		Inputs:  []string{"n.png"},
		Options: ffmpeg.ConvertOptions{Format: "webp", Quality: 80, Resize: "keep", ColorMode: "keep"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if tasks[0].Outputs[0].Name != "n_1.webp" {
		t.Errorf("重名应自动追加序号: %s", tasks[0].Outputs[0].Name)
	}
	final := waitTask(t, svc, tasks[0].ID, 60*time.Second)
	if final.Status != StatusCompleted {
		t.Fatalf("任务失败: %s", final.Error)
	}
	// 原有文件不应被覆盖
	data, err := os.ReadFile(filepath.Join(cfg.OutputDir(), "n.webp"))
	if err != nil || len(data) != 1 {
		t.Errorf("已有文件被覆盖: %v", err)
	}
}

func TestTaskSkipProbe(t *testing.T) {
	cfg := testConfig(t)
	media := newTestMedia(t, cfg)
	svc := NewTaskService(cfg, media, media.ff, zap.NewNop())
	svc.Start()
	defer svc.Stop()

	writeTestPNG(t, filepath.Join(cfg.HomeDir(), "s.png"), 50, 50)
	tasks, err := svc.Create(context.Background(), TaskSpec{
		Inputs:    []string{"s.png"},
		Options:   ffmpeg.ConvertOptions{Format: "webp", Quality: 80, Resize: "keep", ColorMode: "keep"},
		SkipProbe: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 {
		t.Fatalf("应返回 1 个任务: %d", len(tasks))
	}
	time.Sleep(300 * time.Millisecond)
	if st := svc.Stats(); st.Queued != 1 {
		t.Errorf("SkipProbe 模式不应入队: %+v", st)
	}
}

func TestTaskCancel(t *testing.T) {
	cfg := testConfig(t)
	media := newTestMedia(t, cfg)
	cfg.App.Worker = 1
	svc := NewTaskService(cfg, media, media.ff, zap.NewNop())
	svc.Start()
	defer svc.Stop()

	// 塞满队列后取消一个排队任务
	for i := 0; i < 20; i++ {
		writeTestPNG(t, filepath.Join(cfg.HomeDir(), "c", string(rune('a'+i))+".png"), 30, 30)
	}
	tasks, err := svc.Create(context.Background(), TaskSpec{
		Inputs:  []string{"c/a.png", "c/b.png", "c/c.png", "c/d.png", "c/e.png", "c/f.png", "c/g.png", "c/h.png"},
		Options: ffmpeg.ConvertOptions{Format: "avif", Quality: 60, Resize: "keep", ColorMode: "keep"},
	})
	if err != nil {
		t.Fatal(err)
	}
	// 取消最后一个（大概率仍在排队）
	last := tasks[len(tasks)-1]
	if err := svc.Cancel(last.ID); err != nil {
		if last.Status.Terminal() {
			t.Skipf("任务已完成太快，跳过取消测试: %s", last.Status)
		}
		t.Fatalf("取消失败: %v", err)
	}
	final := waitTask(t, svc, last.ID, 90*time.Second)
	if final.Status != StatusCancelled {
		t.Errorf("状态应为 cancelled: %s", final.Status)
	}
	if err := svc.Cancel(last.ID); err == nil {
		t.Error("已结束任务不应允许取消")
	}
	// 已取消任务可重试
	if _, err := svc.Retry(context.Background(), last.ID); err != nil {
		t.Errorf("重试失败: %v", err)
	}
	svc.Stop()
}

func TestTaskSnapshotRecovery(t *testing.T) {
	cfg := testConfig(t)
	media := newTestMedia(t, cfg)
	svc := NewTaskService(cfg, media, media.ff, zap.NewNop())
	svc.Start()

	writeTestPNG(t, filepath.Join(cfg.HomeDir(), "r.png"), 30, 30)
	tasks, err := svc.Create(context.Background(), TaskSpec{
		Inputs:  []string{"r.png"},
		Options: ffmpeg.ConvertOptions{Format: "webp", Quality: 80, Resize: "keep", ColorMode: "keep"},
	})
	if err != nil {
		t.Fatal(err)
	}
	waitTask(t, svc, tasks[0].ID, 60*time.Second)
	svc.Stop()

	// 重新启动：历史任务应恢复
	svc2 := NewTaskService(cfg, media, media.ff, zap.NewNop())
	svc2.Start()
	defer svc2.Stop()
	got, ok := svc2.Get(tasks[0].ID)
	if !ok {
		t.Fatal("重启后应恢复历史任务")
	}
	if got.Status != StatusCompleted {
		t.Errorf("已完成任务状态应保留: %s", got.Status)
	}
	if len(got.Outputs) != 1 || !got.Outputs[0].Done {
		t.Errorf("产物信息丢失: %+v", got.Outputs)
	}
}

func TestTaskClear(t *testing.T) {
	cfg := testConfig(t)
	media := newTestMedia(t, cfg)
	svc := NewTaskService(cfg, media, media.ff, zap.NewNop())
	svc.Start()
	defer svc.Stop()

	writeTestPNG(t, filepath.Join(cfg.HomeDir(), "cl.png"), 30, 30)
	tasks, err := svc.Create(context.Background(), TaskSpec{
		Inputs:  []string{"cl.png"},
		Options: ffmpeg.ConvertOptions{Format: "webp", Quality: 80, Resize: "keep", ColorMode: "keep"},
	})
	if err != nil {
		t.Fatal(err)
	}
	waitTask(t, svc, tasks[0].ID, 60*time.Second)

	// keepOutputs=true：保留产物文件
	outPath := filepath.Join(cfg.OutputDir(), tasks[0].Outputs[0].Name)
	if _, err := os.Stat(outPath); err != nil {
		t.Fatal(err)
	}
	if n, err := svc.Clear(true); err != nil || n == 0 {
		t.Fatalf("清理失败: n=%d err=%v", n, err)
	}
	if _, err := os.Stat(outPath); err != nil {
		t.Errorf("keepOutputs=true 时产物不应被删除: %v", err)
	}

	// keepOutputs=false：删除产物文件
	if n, err := svc.Clear(false); err != nil || n == 0 {
		t.Fatalf("清理失败: n=%d err=%v", n, err)
	}
	if _, err := os.Stat(outPath); !os.IsNotExist(err) {
		t.Errorf("keepOutputs=false 时产物应被删除: %v", err)
	}
}

func TestMediaInfoAndThumbnail(t *testing.T) {
	cfg := testConfig(t)
	media := newTestMedia(t, cfg)
	ctx := context.Background()
	writeTestPNG(t, filepath.Join(cfg.HomeDir(), "t.png"), 800, 600)

	info, err := media.Info(ctx, "t.png")
	if err != nil {
		t.Fatalf("探测失败: %v", err)
	}
	if info.Width != 800 || info.Height != 600 {
		t.Errorf("尺寸错误: %dx%d", info.Width, info.Height)
	}

	// 二次调用应命中缓存
	info2, err := media.Info(ctx, "t.png")
	if err != nil || info2 != info {
		t.Errorf("缓存未命中: %v", err)
	}

	data, err := media.Thumbnail(ctx, "t.png", 200)
	if err != nil {
		t.Fatalf("缩略图失败: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("缩略图为空")
	}
	// 缓存：size 变化后应重新生成，且旧条目被淘汰
	if _, err := media.Thumbnail(ctx, "t.png", 100); err != nil {
		t.Fatal(err)
	}
	media.mu.RLock()
	n := len(media.thumbs)
	media.mu.RUnlock()
	if n != 2 {
		t.Errorf("缩略图缓存条目数异常: %d", n)
	}

	// 文件更新后缓存应失效
	time.Sleep(10 * time.Millisecond)
	writeTestPNG(t, filepath.Join(cfg.HomeDir(), "t.png"), 400, 300)
	info3, err := media.Info(ctx, "t.png")
	if err != nil {
		t.Fatal(err)
	}
	if info3.Width != 400 {
		t.Errorf("文件变化后应重新探测: %dx%d", info3.Width, info3.Height)
	}

	// 错误路径
	if _, err := media.Info(ctx, "nope.png"); err == nil {
		t.Error("不存在的文件应报错")
	}
	if _, err := media.Thumbnail(ctx, "nope.png", 100); err == nil {
		t.Error("不存在的文件应报错")
	}
	if _, err := media.Info(ctx, "../etc/passwd"); err == nil {
		t.Error("目录穿越应被拒绝")
	}
}

func TestMediaFileOps(t *testing.T) {
	cfg := testConfig(t)
	media := newTestMedia(t, cfg)
	writeTestPNG(t, filepath.Join(cfg.HomeDir(), "del.png"), 10, 10)

	if err := media.Rename("del.png", "renamed.png"); err != nil {
		t.Fatalf("重命名失败: %v", err)
	}
	if _, err := os.Stat(filepath.Join(cfg.HomeDir(), "renamed.png")); err != nil {
		t.Errorf("重命名后文件缺失: %v", err)
	}
	if err := media.Rename("renamed.png", "a/b.png"); err == nil {
		t.Error("重命名不应允许路径分隔符")
	}
	if err := media.Rename("renamed.png", ".hidden"); err == nil {
		t.Error("重命名不应允许点开头")
	}
	if err := media.Rename("nope.png", "x.png"); err == nil {
		t.Error("重命名不存在的文件应报错")
	}

	if err := media.CreateFolder("", "newdir"); err != nil {
		t.Fatalf("创建目录失败: %v", err)
	}
	if st, err := os.Stat(filepath.Join(cfg.HomeDir(), "newdir")); err != nil || !st.IsDir() {
		t.Errorf("目录未创建: %v", err)
	}
	if err := media.CreateFolder("", "a/b"); err == nil {
		t.Error("创建目录不应允许路径分隔符")
	}
	if err := media.CreateFolder("", ""); err == nil {
		t.Error("空目录名应报错")
	}

	if err := media.Delete("renamed.png"); err != nil {
		t.Fatalf("删除失败: %v", err)
	}
	if _, err := os.Stat(filepath.Join(cfg.HomeDir(), "renamed.png")); !os.IsNotExist(err) {
		t.Error("文件未被删除")
	}
	if err := media.Delete(""); err == nil {
		t.Error("不应允许删除根目录")
	}
	if err := media.Delete("nope.png"); err == nil {
		t.Error("删除不存在的文件应报错")
	}
	// 非空目录不能直接删除
	writeTestPNG(t, filepath.Join(cfg.HomeDir(), "newdir", "x.png"), 10, 10)
	if err := media.Delete("newdir"); err == nil {
		t.Error("删除非空目录应报错")
	}
}

// waitTask 轮询等待任务结束。
func waitTask(t *testing.T, svc *TaskService, id string, timeout time.Duration) *Task {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		task, ok := svc.Get(id)
		if ok && task.Status.Terminal() {
			return task
		}
		time.Sleep(50 * time.Millisecond)
	}
	if task, ok := svc.Get(id); ok {
		t.Fatalf("等待任务结束超时，最后状态: %s", task.Status)
	}
	t.Fatalf("任务不存在: %s", id)
	return nil
}
