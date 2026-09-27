package handler

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"math"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/meimolihan/fan-image-tr/internal/config"
	"github.com/meimolihan/fan-image-tr/internal/ffmpeg"
	"github.com/meimolihan/fan-image-tr/internal/logger"
	"github.com/meimolihan/fan-image-tr/internal/service"
)

// ==================== 测试脚手架 ====================

type testEnv struct {
	router *gin.Engine
	media  *service.MediaService
	tasks  *service.TaskService
	cfg    *config.Config
	home   string
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	root := t.TempDir()
	cfg := &config.Config{
		App: config.AppConfig{
			Port:        0,
			Env:         "testing",
			DataDir:     filepath.Join(root, "data"),
			OutputDir:   filepath.Join(root, "data", "output"),
			UploadDir:   filepath.Join(root, "data", "uploads"),
			MediaDir:    filepath.Join(root, "media"),
			Debug:       false,
			AllowUpload: true,
			MaxUploadMB: 8,
			Worker:      2,
		},
		FFmpeg:  config.FFmpegConfig{Accel: "auto", Format: "webp", Quality: 80, ThumbSize: 480},
		Logging: config.LoggingConfig{Level: "error"},
	}
	for _, d := range []string{cfg.App.DataDir, cfg.OutputDir(), cfg.UploadDir(), cfg.HomeDir()} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("创建目录失败 %s: %v", d, err)
		}
	}
	ff, err := ffmpeg.New(ffmpeg.Options{
		FFmpegBin:   cfg.FFmpegBin(),
		FFprobeBin:  cfg.FFprobeBin(),
		Threads:     cfg.FFmpeg.Threads,
		Format:      cfg.FFmpeg.Format,
		Quality:     cfg.FFmpeg.Quality,
		Accel:       cfg.FFmpeg.Accel,
		VAAPIDevice: cfg.FFmpeg.VAAPIDevice,
		HWDecode:    cfg.FFmpeg.HWDecode,
		ThumbSize:   cfg.FFmpeg.ThumbSize,
		ProbeTO:     cfg.FFmpeg.ProbeTimeout,
	})
	if err != nil {
		t.Skipf("本机不可用 ffmpeg: %v", err)
	}
	log, err := logger.New(cfg)
	if err != nil {
		t.Fatalf("创建日志失败: %v", err)
	}
	media := service.NewMediaService(cfg, ff, log)
	tasks := service.NewTaskService(cfg, media, ff, log)
	presets, err := service.NewPresetService(cfg)
	if err != nil {
		t.Fatalf("创建预设服务失败: %v", err)
	}
	tasks.Start()
	t.Cleanup(tasks.Stop)

	gin.SetMode(gin.TestMode)
	env := &testEnv{media: media, tasks: tasks, cfg: cfg, home: cfg.HomeDir()}
	env.router = New(cfg, ff, media, tasks, presets, log).Router()
	return env
}

func (e *testEnv) do(t *testing.T, method, target string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var rdr *bytes.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("序列化请求失败: %v", err)
		}
		rdr = bytes.NewReader(raw)
	} else {
		rdr = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, target, rdr)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	e.router.ServeHTTP(w, req)
	return w
}

func (e *testEnv) json(t *testing.T, w *httptest.ResponseRecorder, dst any) {
	t.Helper()
	if err := json.Unmarshal(w.Body.Bytes(), dst); err != nil {
		t.Fatalf("解析响应失败: %v (body=%s)", err, w.Body.String())
	}
}

// writePNG 生成一张带透明通道的测试图片
func (e *testEnv) writePNG(t *testing.T, name string, w, h int, bg color.RGBA) string {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, bg)
		}
	}
	p := filepath.Join(e.home, name)
	f, err := os.Create(p)
	if err != nil {
		t.Fatalf("创建测试图失败: %v", err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatalf("写入 PNG 失败: %v", err)
	}
	return p
}

func q(path string) string { return "?path=" + url.QueryEscape(path) }

// ==================== 基础接口 ====================

func TestHealthAndVersion(t *testing.T) {
	e := newTestEnv(t)
	w := e.do(t, http.MethodGet, "/api/health", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("health 状态码 = %d", w.Code)
	}
	var health map[string]any
	e.json(t, w, &health)
	if health["status"] != "ok" {
		t.Errorf("health status = %v", health["status"])
	}

	w = e.do(t, http.MethodGet, "/api/version", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("version 状态码 = %d", w.Code)
	}
	var ver struct {
		Version   string `json:"version"`
		GoVersion string `json:"go_version"`
	}
	e.json(t, w, &ver)
	if ver.Version == "" || ver.GoVersion == "" {
		t.Errorf("version 响应缺字段: %+v", ver)
	}
}

func TestOptionsListsFormatsAndDictionaries(t *testing.T) {
	e := newTestEnv(t)
	w := e.do(t, http.MethodGet, "/api/options", nil)
	var opts struct {
		Formats []struct {
			ID        string `json:"id"`
			Available bool   `json:"available"`
			Quality   struct {
				Kind string `json:"kind"`
				Min  int    `json:"min"`
				Max  int    `json:"max"`
			} `json:"quality"`
			DefaultQuality int `json:"default_quality"`
		} `json:"formats"`
		ResizeModes []struct{ ID string } `json:"resize_modes"`
		Adapts      []struct{ ID string } `json:"adapts"`
		ColorModes  []struct{ ID string } `json:"color_modes"`
		Chromas     []struct{ ID string } `json:"chromas"`
		Accels      []struct {
			ID        string   `json:"id"`
			Available bool     `json:"available"`
			Formats   []string `json:"formats"`
		} `json:"accels"`
		DefaultFormat string `json:"default_format"`
		AllowUpload   bool   `json:"allow_upload"`
		MaxUpload     int    `json:"max_upload"`
	}
	e.json(t, w, &opts)

	if len(opts.Formats) < 8 {
		t.Fatalf("格式数量 = %d，期望至少 8", len(opts.Formats))
	}
	if opts.DefaultFormat == "" {
		t.Error("缺少 default_format")
	}
	if !opts.AllowUpload || opts.MaxUpload != 8 {
		t.Errorf("上传配置异常: %v %d", opts.AllowUpload, opts.MaxUpload)
	}
	for _, d := range []struct {
		name string
		got  []struct{ ID string }
		want []string
	}{
		{"resize_modes", opts.ResizeModes, []string{"keep", "long_edge", "short_edge", "width", "height", "percent", "exact"}},
		{"adapts", opts.Adapts, []string{"fit", "fill", "stretch"}},
		{"color_modes", opts.ColorModes, []string{"keep", "rgb", "grayscale"}},
		{"chromas", opts.Chromas, []string{"444", "422", "420"}},
	} {
		if len(d.got) != len(d.want) {
			t.Errorf("%s 数量 = %d，期望 %d", d.name, len(d.got), len(d.want))
			continue
		}
		for i, id := range d.want {
			if d.got[i].ID != id {
				t.Errorf("%s[%d] = %s，期望 %s", d.name, i, d.got[i].ID, id)
			}
		}
	}
	// 硬件加速必须与逐格式自检结果一致：available 时至少有一个格式
	for _, a := range opts.Accels {
		if a.Available && len(a.Formats) == 0 {
			t.Errorf("加速 %s 标记为可用但没有任何实测格式", a.ID)
		}
	}
	// jpg 必须是 yuvj 量纲且滑块范围有意义
	for _, f := range opts.Formats {
		if f.ID != "jpg" {
			continue
		}
		if f.Quality.Kind != "qscale" || f.Quality.Max <= f.Quality.Min {
			t.Errorf("jpg 质量模型异常: %+v", f.Quality)
		}
		if f.DefaultQuality < 1 || f.DefaultQuality > 100 {
			t.Errorf("jpg default_quality = %d，超出 1~100 界面量纲", f.DefaultQuality)
		}
	}
}

// ==================== 媒体接口 ====================

func TestMediaDirList(t *testing.T) {
	e := newTestEnv(t)
	e.writePNG(t, "a.png", 40, 30, color.RGBA{255, 0, 0, 255})
	e.writePNG(t, "b.png", 20, 20, color.RGBA{0, 255, 0, 128})
	if err := os.WriteFile(filepath.Join(e.home, "note.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(e.home, ".hidden.png"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(e.home, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}

	w := e.do(t, http.MethodGet, "/api/media/dir", nil)
	var dir service.List
	e.json(t, w, &dir)

	if dir.Path != "" {
		t.Errorf("根目录 path = %q，期望空串", dir.Path)
	}
	if dir.Parent != "" {
		t.Errorf("根目录 parent = %q，期望空串", dir.Parent)
	}
	if len(dir.Dirs) != 1 || dir.Dirs[0].Name != "sub" || !dir.Dirs[0].IsDir {
		t.Errorf("目录列表异常: %+v", dir.Dirs)
	}
	if len(dir.Files) != 2 {
		t.Fatalf("文件数量 = %d，期望 2（非图片与隐藏文件应被过滤）: %+v", len(dir.Files), dir.Files)
	}
	if dir.Count != len(dir.Files) {
		t.Errorf("count = %d，files = %d", dir.Count, len(dir.Files))
	}
	for _, f := range dir.Files {
		if f.IsDir {
			t.Errorf("文件 %s 的 is_dir 为 true", f.Name)
		}
		if !f.IsImage {
			t.Errorf("文件 %s 的 is_image 为 false", f.Name)
		}
		if f.Ext == "" {
			t.Errorf("文件 %s 缺少 ext", f.Name)
		}
		if f.Size <= 0 {
			t.Errorf("文件 %s 体积 = %d", f.Name, f.Size)
		}
		if f.ModTimeUnix <= 0 {
			t.Errorf("文件 %s 缺少 mod_time", f.Name)
		}
	}
	if dir.TotalSize <= 0 {
		t.Errorf("total_size = %d", dir.TotalSize)
	}

	// 子目录
	w = e.do(t, http.MethodGet, "/api/media/dir"+q("sub"), nil)
	e.json(t, w, &dir)
	if dir.Path != "sub" || len(dir.Files) != 0 || dir.Parent != "" {
		t.Errorf("子目录响应异常: path=%q parent=%q files=%d", dir.Path, dir.Parent, len(dir.Files))
	}
}

func TestMediaDirRejectsTraversal(t *testing.T) {
	e := newTestEnv(t)
	for _, p := range []string{"../etc", "..", "a/../../..", "sub/../../x"} {
		w := e.do(t, http.MethodGet, "/api/media/dir"+q(p), nil)
		if w.Code != http.StatusForbidden {
			t.Errorf("dir(%q) 状态码 = %d，期望 403", p, w.Code)
		}
	}
	// 绝对路径越权
	w := e.do(t, http.MethodGet, "/api/media/dir"+q("/etc/passwd"), nil)
	if w.Code != http.StatusForbidden {
		t.Errorf("dir(/etc/passwd) 状态码 = %d，期望 403", w.Code)
	}
}

func TestMediaInfo(t *testing.T) {
	e := newTestEnv(t)
	e.writePNG(t, "info.png", 120, 60, color.RGBA{10, 20, 30, 255})

	w := e.do(t, http.MethodGet, "/api/media/info"+q("info.png"), nil)
	if w.Code != http.StatusOK {
		t.Fatalf("info 状态码 = %d: %s", w.Code, w.Body.String())
	}
	var info ffmpeg.ImageInfo
	e.json(t, w, &info)
	if info.Width != 120 || info.Height != 60 {
		t.Errorf("尺寸 = %dx%d，期望 120x60", info.Width, info.Height)
	}
	if info.MimeType != "image/png" {
		t.Errorf("mime = %s", info.MimeType)
	}
	if info.Size <= 0 {
		t.Errorf("体积信息异常: %d", info.Size)
	}
	if info.FormatName == "" || info.PixFmt == "" {
		t.Errorf("探测信息缺失: format=%q pix=%q", info.FormatName, info.PixFmt)
	}
	if info.OrientationLabel == "" {
		t.Error("缺少 orientation_label")
	}
	if math.Abs(info.Aspect-2) > 0.01 {
		t.Errorf("宽高比 = %v，期望 2（120x60）", info.Aspect)
	}

	// 不存在的文件
	if w := e.do(t, http.MethodGet, "/api/media/info"+q("nope.png"), nil); w.Code != http.StatusNotFound {
		t.Errorf("info(不存在) 状态码 = %d，期望 404", w.Code)
	}
}

func TestMediaThumbETagAndCache(t *testing.T) {
	e := newTestEnv(t)
	e.writePNG(t, "thumb.png", 300, 200, color.RGBA{200, 30, 60, 255})

	w := e.do(t, http.MethodGet, "/api/media/thumb"+q("thumb.png")+"&size=200", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("thumb 状态码 = %d: %s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "image/") {
		t.Errorf("Content-Type = %s", ct)
	}
	etag := w.Header().Get("ETag")
	if etag == "" {
		t.Fatal("缺少 ETag")
	}
	body := w.Body.Bytes()
	if len(body) == 0 {
		t.Fatal("缩略图内容为空")
	}

	// 条件请求应命中 304
	req := httptest.NewRequest(http.MethodGet, "/api/media/thumb"+q("thumb.png")+"&size=200", nil)
	req.Header.Set("If-None-Match", etag)
	w2 := httptest.NewRecorder()
	e.router.ServeHTTP(w2, req)
	if w2.Code != http.StatusNotModified {
		t.Errorf("条件请求状态码 = %d，期望 304", w2.Code)
	}

	// 非法尺寸应回落到默认
	w3 := e.do(t, http.MethodGet, "/api/media/thumb"+q("thumb.png")+"&size=abc", nil)
	if w3.Code != http.StatusOK {
		t.Errorf("thumb(size=abc) 状态码 = %d", w3.Code)
	}
}

func TestMediaRawAndDownloadOnlyImages(t *testing.T) {
	e := newTestEnv(t)
	e.writePNG(t, "raw.png", 50, 50, color.RGBA{1, 2, 3, 255})
	// 在允许目录内放置非图片文件
	secret := filepath.Join(e.home, "secret.txt")
	if err := os.WriteFile(secret, []byte("top secret"), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{"/api/media/raw", "/api/media/download"} {
		w := e.do(t, http.MethodGet, path+q("raw.png"), nil)
		if w.Code != http.StatusOK {
			t.Fatalf("%s(图片) 状态码 = %d: %s", path, w.Code, w.Body.String())
		}
		if w.Body.Len() == 0 {
			t.Errorf("%s(图片) 内容为空", path)
		}
		// 非图片必须被拒绝，避免退化成任意文件读取
		w = e.do(t, http.MethodGet, path+q("secret.txt"), nil)
		if w.Code == http.StatusOK {
			t.Errorf("%s 竟然允许读取非图片文件 secret.txt", path)
		}
		if strings.Contains(w.Body.String(), "top secret") {
			t.Errorf("%s 泄露了非图片文件内容", path)
		}
		// 路径越权
		if w := e.do(t, http.MethodGet, path+q("../etc/passwd"), nil); w.Code != http.StatusForbidden {
			t.Errorf("%s(../etc/passwd) 状态码 = %d，期望 403", path, w.Code)
		}
	}

	// 下载要带 Content-Disposition
	w := e.do(t, http.MethodGet, "/api/media/download"+q("raw.png"), nil)
	if cd := w.Header().Get("Content-Disposition"); !strings.Contains(cd, "attachment") {
		t.Errorf("Content-Disposition = %q", cd)
	}
}

func TestMediaFileOperations(t *testing.T) {
	e := newTestEnv(t)
	e.writePNG(t, "op.png", 30, 30, color.RGBA{9, 9, 9, 255})

	// 新建目录
	if w := e.do(t, http.MethodPost, "/api/media/mkdir", map[string]string{"path": "", "name": "新建目录"}); w.Code != http.StatusOK {
		t.Fatalf("mkdir 状态码 = %d: %s", w.Code, w.Body.String())
	}
	if st, err := os.Stat(filepath.Join(e.home, "新建目录")); err != nil || !st.IsDir() {
		t.Fatalf("目录未创建: %v", err)
	}

	// 重命名
	if w := e.do(t, http.MethodPost, "/api/media/rename", map[string]string{"path": "新建目录", "new_name": "renamed"}); w.Code != http.StatusOK {
		t.Fatalf("rename 状态码 = %d: %s", w.Code, w.Body.String())
	}
	if _, err := os.Stat(filepath.Join(e.home, "renamed")); err != nil {
		t.Fatalf("重命名失败: %v", err)
	}

	// 名称含分隔符必须拒绝
	for _, bad := range []string{"../evil", "a/b", "..", "/abs"} {
		w := e.do(t, http.MethodPost, "/api/media/rename", map[string]string{"path": "renamed", "new_name": bad})
		if w.Code == http.StatusOK {
			t.Errorf("rename 到 %q 竟然成功", bad)
		}
	}
	// 重命名只改文件名，不允许借 new_name 跨目录搬运
	if w := e.do(t, http.MethodPost, "/api/media/rename", map[string]string{"path": "op.png", "new_name": "renamed/moved.png"}); w.Code == http.StatusOK {
		t.Error("重命名竟然允许跨目录")
	}

	// 删除
	if w := e.do(t, http.MethodPost, "/api/media/delete", map[string]string{"path": "op.png"}); w.Code != http.StatusOK {
		t.Fatalf("delete 状态码 = %d: %s", w.Code, w.Body.String())
	}
	if _, err := os.Stat(filepath.Join(e.home, "op.png")); !os.IsNotExist(err) {
		t.Error("文件未被删除")
	}
	// 删除不存在 / 是目录的路径
	if w := e.do(t, http.MethodPost, "/api/media/delete", map[string]string{"path": "nope.png"}); w.Code == http.StatusOK {
		t.Error("删除不存在的文件竟然成功")
	}
	if w := e.do(t, http.MethodPost, "/api/media/delete", map[string]string{"path": "renamed"}); w.Code == http.StatusOK {
		t.Error("删除目录竟然成功")
	}
	// 越权删除
	if w := e.do(t, http.MethodPost, "/api/media/delete", map[string]string{"path": "../etc/hosts"}); w.Code != http.StatusForbidden {
		t.Errorf("越权 delete 状态码 = %d，期望 403", w.Code)
	}
}

func TestMediaUpload(t *testing.T) {
	e := newTestEnv(t)
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, err := mw.CreateFormFile("files", "上传图.png")
	if err != nil {
		t.Fatal(err)
	}
	img := image.NewNRGBA(image.Rect(0, 0, 24, 24))
	img.Set(5, 5, color.RGBA{255, 255, 255, 255})
	if err := png.Encode(part, img); err != nil {
		t.Fatal(err)
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/media/upload", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	w := httptest.NewRecorder()
	e.router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("upload 状态码 = %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Uploaded int      `json:"uploaded"`
		Failed   []string `json:"failed"`
		Files    []struct {
			Path string `json:"path"`
		} `json:"files"`
	}
	e.json(t, w, &resp)
	if resp.Uploaded != 1 || len(resp.Files) != 1 {
		t.Fatalf("上传结果异常: %+v", resp)
	}
	if !strings.HasSuffix(resp.Files[0].Path, ".png") {
		t.Errorf("上传路径后缀异常: %s", resp.Files[0].Path)
	}
	if _, err := os.Stat(resp.Files[0].Path); err != nil {
		t.Errorf("上传文件不存在: %v", err)
	}

	// 非图片扩展名应被拒绝
	buf.Reset()
	mw = multipart.NewWriter(&buf)
	part, _ = mw.CreateFormFile("files", "evil.txt")
	part.Write([]byte("not an image"))
	mw.Close()
	req = httptest.NewRequest(http.MethodPost, "/api/media/upload", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	w = httptest.NewRecorder()
	e.router.ServeHTTP(w, req)
	if w.Code == http.StatusOK {
		var r resp2
		_ = json.Unmarshal(w.Body.Bytes(), &r)
		if r.Uploaded != 0 {
			t.Errorf("非图片文件竟被接受: %+v", r)
		}
	}
}

type resp2 struct {
	Uploaded int `json:"uploaded"`
	Failed   []string
}

func TestEstimateAndPreview(t *testing.T) {
	e := newTestEnv(t)
	e.writePNG(t, "est.png", 800, 600, color.RGBA{120, 130, 140, 255})

	w := e.do(t, http.MethodPost, "/api/estimate", map[string]any{
		"path":    "est.png",
		"options": map[string]any{"format": "webp", "quality": 80, "resize": "long_edge", "size": 400},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("estimate 状态码 = %d: %s", w.Code, w.Body.String())
	}
	var est struct {
		Width  int    `json:"width"`
		Height int    `json:"height"`
		Bytes  int64  `json:"bytes"`
		Text   string `json:"text"`
	}
	e.json(t, w, &est)
	if est.Width != 400 || est.Height != 300 {
		t.Errorf("预估尺寸 = %dx%d，期望 400x300", est.Width, est.Height)
	}
	if est.Bytes <= 0 || est.Text == "" {
		t.Errorf("预估体积异常: %d %q", est.Bytes, est.Text)
	}

	// 不缩放时应保持原尺寸
	w = e.do(t, http.MethodPost, "/api/estimate", map[string]any{
		"path":    "est.png",
		"options": map[string]any{"format": "png", "quality": 90, "resize": "keep"},
	})
	e.json(t, w, &est)
	if est.Width != 800 || est.Height != 600 {
		t.Errorf("不缩放预估尺寸 = %dx%d", est.Width, est.Height)
	}

	// 命令预览
	w = e.do(t, http.MethodPost, "/api/preview", map[string]any{
		"path":    "est.png",
		"options": map[string]any{"format": "jpg", "quality": 85, "resize": "keep"},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("preview 状态码 = %d: %s", w.Code, w.Body.String())
	}
	var pv struct {
		Command string `json:"command"`
	}
	e.json(t, w, &pv)
	if !strings.HasPrefix(pv.Command, "ffmpeg") {
		t.Errorf("命令未以 ffmpeg 开头: %s", pv.Command)
	}
	if !strings.Contains(pv.Command, "est.png") {
		t.Errorf("命令缺少输入文件: %s", pv.Command)
	}
	// jpg 走 yuvj420p；硬件编码时必须带 -color_range pc
	if !strings.Contains(pv.Command, "yuvj420p") {
		t.Errorf("jpeg 命令缺少 yuvj420p: %s", pv.Command)
	}
	// 输出文件名必须与任务实际命名规则一致，不能是"输出.jpg"这种占位名
	if !strings.Contains(pv.Command, "est.jpg") {
		t.Errorf("命令输出文件名与任务命名规则不一致: %s", pv.Command)
	}

	// 不带 path 时用占位文件名，且仍应返回可读命令
	w = e.do(t, http.MethodPost, "/api/preview", map[string]any{
		"options": map[string]any{"format": "png"},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("无 path 的 preview 状态码 = %d: %s", w.Code, w.Body.String())
	}
	e.json(t, w, &pv)
	if !strings.Contains(pv.Command, "<源文件>") || !strings.Contains(pv.Command, "png") {
		t.Errorf("无 path 的预览命令异常: %s", pv.Command)
	}
	// 空路径会解析到浏览根目录，不能把根目录名当成输出文件名
	if !strings.Contains(pv.Command, "<源文件名>.png") {
		t.Errorf("无 path 时输出文件名异常（不应使用根目录名）: %s", pv.Command)
	}

	// 非法参数应报错
	if w := e.do(t, http.MethodPost, "/api/preview", map[string]any{
		"path":    "est.png",
		"options": map[string]any{"format": "nope"},
	}); w.Code != http.StatusBadRequest {
		t.Errorf("非法格式的 preview 状态码 = %d，期望 400", w.Code)
	}
	if w := e.do(t, http.MethodPost, "/api/preview", map[string]any{
		"path": "../../etc/passwd",
	}); w.Code != http.StatusForbidden {
		t.Errorf("越界路径的 preview 状态码 = %d，期望 403", w.Code)
	}
}

// ==================== 任务接口 ====================

func TestTaskLifecycleViaAPI(t *testing.T) {
	e := newTestEnv(t)
	e.writePNG(t, "t1.png", 200, 100, color.RGBA{200, 30, 60, 255})

	w := e.do(t, http.MethodPost, "/api/tasks", map[string]any{
		"inputs":  []string{"t1.png"},
		"options": map[string]any{"format": "webp", "quality": 80, "resize": "long_edge", "size": 100},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("创建任务状态码 = %d: %s", w.Code, w.Body.String())
	}
	var created struct {
		Batch  string                `json:"batch"`
		Count  int                   `json:"count"`
		Tasks  []struct{ ID string } `json:"tasks"`
		Output string                `json:"output_dir"`
	}
	e.json(t, w, &created)
	if created.Count != 1 || len(created.Tasks) != 1 || created.Batch == "" {
		t.Fatalf("创建响应异常: %+v", created)
	}
	id := created.Tasks[0].ID

	// 轮询直到结束
	var final service.Task
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		w = e.do(t, http.MethodGet, "/api/tasks/"+id, nil)
		if w.Code != http.StatusOK {
			t.Fatalf("查询任务状态码 = %d", w.Code)
		}
		e.json(t, w, &final)
		switch final.Status {
		case service.StatusCompleted, service.StatusFailed, service.StatusCancelled:
			goto done
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("任务超时未结束，状态 = %s", final.Status)
done:
	if final.Status != service.StatusCompleted {
		t.Fatalf("任务状态 = %s，错误 = %s", final.Status, final.Error)
	}
	if final.Progress != 100 {
		t.Errorf("完成时 progress = %v，期望 100", final.Progress)
	}
	if final.DurationSec <= 0 {
		t.Errorf("耗时 = %v", final.DurationSec)
	}
	if !strings.HasPrefix(final.Command, "ffmpeg") {
		t.Errorf("命令记录异常: %q", final.Command)
	}
	if len(final.Outputs) != 1 || !final.Outputs[0].Done {
		t.Fatalf("产物异常: %+v", final.Outputs)
	}
	out := final.Outputs[0]
	if out.Width != 100 || out.Height != 50 {
		t.Errorf("产物尺寸 = %dx%d，期望 100x50", out.Width, out.Height)
	}
	if out.Size <= 0 {
		t.Errorf("产物体积 = %d", out.Size)
	}
	if out.Estimate <= 0 {
		t.Errorf("预估体积 = %d", out.Estimate)
	}
	// 展示用质量应为界面 1~100 量纲
	if final.Quality < 1 || final.Quality > 100 {
		t.Errorf("任务展示质量 = %d，超出 1~100", final.Quality)
	}
	if final.Quality != final.Options.Quality {
		t.Errorf("展示质量 %d 与提交质量 %d 不一致", final.Quality, final.Options.Quality)
	}
	// 产物真实存在
	abs, err := e.media.ResolvePath(out.Path)
	if err != nil {
		t.Fatalf("解析产物路径失败: %v", err)
	}
	if st, err := os.Stat(abs); err != nil || st.Size() == 0 {
		t.Errorf("产物文件异常: %v", err)
	}

	// 下载
	w = e.do(t, http.MethodGet, "/api/media/download"+q(out.Path), nil)
	if w.Code != http.StatusOK || w.Body.Len() == 0 {
		t.Errorf("下载产物失败: %d len=%d", w.Code, w.Body.Len())
	}

	// 统计
	w = e.do(t, http.MethodGet, "/api/tasks/stats", nil)
	var st service.Stats
	e.json(t, w, &st)
	if st.Total < 1 || st.Completed < 1 {
		t.Errorf("统计异常: %+v", st)
	}

	// 已完成任务重试应被拒绝
	w = e.do(t, http.MethodPost, "/api/tasks/"+id+"/retry", nil)
	if w.Code == http.StatusOK {
		t.Error("已完成任务竟然允许重试")
	}
	// 取消已完成任务应被拒绝
	w = e.do(t, http.MethodPost, "/api/tasks/"+id+"/cancel", nil)
	if w.Code == http.StatusOK {
		t.Error("已完成任务竟然允许取消")
	}

	// 删除
	if w := e.do(t, http.MethodDelete, "/api/tasks/"+id, nil); w.Code != http.StatusOK {
		t.Errorf("删除任务状态码 = %d", w.Code)
	}
	if w := e.do(t, http.MethodGet, "/api/tasks/"+id, nil); w.Code != http.StatusNotFound {
		t.Errorf("删除后查询状态码 = %d，期望 404", w.Code)
	}
	// 重复删除已删除的任务 → 404
	if w := e.do(t, http.MethodDelete, "/api/tasks/"+id, nil); w.Code != http.StatusNotFound {
		t.Errorf("重复删除任务状态码 = %d，期望 404", w.Code)
	}
}

func TestTaskVariantsAndOverwrite(t *testing.T) {
	e := newTestEnv(t)
	e.writePNG(t, "v.png", 300, 200, color.RGBA{10, 10, 200, 255})

	w := e.do(t, http.MethodPost, "/api/tasks", map[string]any{
		"inputs":  []string{"v.png"},
		"options": map[string]any{"format": "webp", "quality": 80, "resize": "long_edge", "size": 150},
		"variants": []map[string]any{
			{"name": "小图", "resize": "long_edge", "size": 80},
			{"name": "中图", "format": "jpg", "resize": "long_edge", "size": 120},
		},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("创建变体任务失败: %d %s", w.Code, w.Body.String())
	}
	var created struct {
		Tasks []struct{ ID string } `json:"tasks"`
	}
	e.json(t, w, &created)
	id := created.Tasks[0].ID

	var task service.Task
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		e.json(t, e.do(t, http.MethodGet, "/api/tasks/"+id, nil), &task)
		if task.Status == service.StatusCompleted || task.Status == service.StatusFailed {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if task.Status != service.StatusCompleted {
		t.Fatalf("变体任务失败: %s %s", task.Status, task.Error)
	}
	if len(task.Outputs) != 2 {
		t.Fatalf("产物数量 = %d，期望 2", len(task.Outputs))
	}
	seen := map[string]bool{}
	for _, o := range task.Outputs {
		if !o.Done || o.Size <= 0 {
			t.Errorf("产物未完成: %+v", o)
		}
		if seen[o.Name] {
			t.Errorf("产物重名: %s", o.Name)
		}
		seen[o.Name] = true
		if o.Variant == "" {
			t.Errorf("产物缺少变体名: %+v", o)
		}
	}
	// 变体覆盖了格式与尺寸
	var sawJPG bool
	for _, o := range task.Outputs {
		if strings.HasSuffix(o.Name, ".jpg") {
			sawJPG = true
			if o.Width != 120 {
				t.Errorf("jpg 变体尺寸 = %d，期望 120", o.Width)
			}
		}
	}
	if !sawJPG {
		t.Error("变体未生效：没有 jpg 产物")
	}
}

func TestTaskValidationErrors(t *testing.T) {
	e := newTestEnv(t)
	e.writePNG(t, "ok.png", 40, 40, color.RGBA{1, 1, 1, 255})

	cases := []struct {
		name string
		body any
	}{
		{"空输入", map[string]any{"inputs": []string{}, "options": map[string]any{"format": "webp"}}},
		{"非法格式", map[string]any{"inputs": []string{"ok.png"}, "options": map[string]any{"format": "nope"}}},
		{"非法缩放", map[string]any{"inputs": []string{"ok.png"}, "options": map[string]any{"format": "webp", "resize": "nope"}}},
		{"非法输入", map[string]any{"inputs": []string{"missing.png"}, "options": map[string]any{"format": "webp"}}},
		{"越权输入", map[string]any{"inputs": []string{"../etc/passwd"}, "options": map[string]any{"format": "webp"}}},
	}
	for _, c := range cases {
		w := e.do(t, http.MethodPost, "/api/tasks", c.body)
		if w.Code == http.StatusOK {
			t.Errorf("%s: 状态码 = 200，期望错误", c.name)
		}
	}

	// 变体必须名校验
	w := e.do(t, http.MethodPost, "/api/tasks", map[string]any{
		"inputs":   []string{"ok.png"},
		"options":  map[string]any{"format": "webp"},
		"variants": []map[string]any{{"name": "", "resize": "long_edge", "size": 10}},
	})
	if w.Code == http.StatusOK {
		t.Error("空变体名竟然被接受")
	}
	// 输出目录越权
	w = e.do(t, http.MethodPost, "/api/tasks", map[string]any{
		"inputs":     []string{"ok.png"},
		"options":    map[string]any{"format": "webp"},
		"output_dir": "/tmp/evil-output",
	})
	if w.Code == http.StatusOK {
		t.Error("越权输出目录竟然被接受")
	}
}

func TestTaskClearAndMissing(t *testing.T) {
	e := newTestEnv(t)
	if w := e.do(t, http.MethodGet, "/api/tasks/task_missing", nil); w.Code != http.StatusNotFound {
		t.Errorf("查询不存在任务 = %d", w.Code)
	}
	if w := e.do(t, http.MethodPost, "/api/tasks/task_missing/cancel", nil); w.Code != http.StatusNotFound {
		t.Errorf("取消不存在任务 = %d", w.Code)
	}
	if w := e.do(t, http.MethodPost, "/api/tasks/task_missing/retry", nil); w.Code != http.StatusNotFound {
		t.Errorf("重试不存在任务 = %d", w.Code)
	}
	// 空列表清理
	w := e.do(t, http.MethodDelete, "/api/tasks", nil)
	if w.Code != http.StatusOK && w.Code != http.StatusBadRequest {
		t.Errorf("清理空列表 = %d", w.Code)
	}
}

// ==================== 预设接口 ====================

func TestPresetCRUD(t *testing.T) {
	e := newTestEnv(t)

	// 内置预设
	w := e.do(t, http.MethodGet, "/api/presets", nil)
	var list struct {
		Presets []struct {
			Name     string         `json:"name"`
			BuiltIn  bool           `json:"built_in"`
			Options  map[string]any `json:"options"`
			HasBuilt bool           `json:"-"`
		} `json:"presets"`
	}
	e.json(t, w, &list)
	if len(list.Presets) < 5 {
		t.Fatalf("内置预设数量 = %d", len(list.Presets))
	}
	builtin := ""
	for _, p := range list.Presets {
		if p.BuiltIn {
			builtin = p.Name
			break
		}
	}
	if builtin == "" {
		t.Fatal("没有内置预设")
	}

	// 新建
	w = e.do(t, http.MethodPost, "/api/presets", map[string]any{
		"name":        "我的预设",
		"description": "测试用",
		"options": map[string]any{
			"format": "webp", "quality": 70, "resize": "long_edge", "size": 1200, "color_mode": "keep",
		},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("创建预设 = %d: %s", w.Code, w.Body.String())
	}
	var saved struct {
		Name    string         `json:"name"`
		BuiltIn bool           `json:"built_in"`
		Options map[string]any `json:"options"`
	}
	e.json(t, w, &saved)
	if saved.Name != "我的预设" || saved.BuiltIn {
		t.Errorf("创建响应异常: %+v", saved)
	}
	if int(saved.Options["size"].(float64)) != 1200 {
		t.Errorf("预设参数未正确保存: %+v", saved.Options)
	}

	// 校验：非法格式 / 空名 / 覆盖内置
	for _, c := range []struct {
		name string
		body map[string]any
	}{
		{"空名", map[string]any{"name": "", "options": map[string]any{"format": "webp"}}},
		{"非法格式", map[string]any{"name": "x", "options": map[string]any{"format": "nope"}}},
		{"非法缩放", map[string]any{"name": "x", "options": map[string]any{"format": "webp", "resize": "nope"}}},
		{"覆盖内置", map[string]any{"name": builtin, "options": map[string]any{"format": "webp"}}},
	} {
		if w := e.do(t, http.MethodPost, "/api/presets", c.body); w.Code == http.StatusOK {
			t.Errorf("%s: 竟然成功", c.name)
		}
	}

	// 持久化：profiles.json 应存在
	if _, err := os.Stat(filepath.Join(e.cfg.App.DataDir, "profiles.json")); err != nil {
		t.Errorf("预设未持久化: %v", err)
	}

	// 删除内置应被拒绝
	if w := e.do(t, http.MethodDelete, "/api/presets/"+url.PathEscape(builtin), nil); w.Code == http.StatusOK {
		t.Error("内置预设竟然可删除")
	}
	// 删除自定义
	if w := e.do(t, http.MethodDelete, "/api/presets/"+url.PathEscape("我的预设"), nil); w.Code != http.StatusOK {
		t.Errorf("删除自定义预设 = %d: %s", w.Code, w.Body.String())
	}
	// 再次删除应 404
	if w := e.do(t, http.MethodDelete, "/api/presets/"+url.PathEscape("我的预设"), nil); w.Code != http.StatusNotFound {
		t.Errorf("重复删除 = %d，期望 404", w.Code)
	}
}

// ==================== 静态资源 ====================

func TestStaticAssetsAndSPAFallback(t *testing.T) {
	e := newTestEnv(t)

	for _, p := range []string{"/", "/index.html", "/css/style.css", "/js/app.js", "/favicon.svg"} {
		w := e.do(t, http.MethodGet, p, nil)
		if w.Code != http.StatusOK {
			t.Errorf("GET %s = %d", p, w.Code)
		}
		if w.Body.Len() == 0 {
			t.Errorf("GET %s 内容为空", p)
		}
	}
	// index.html 应是真实页面（不是占位符）
	w := e.do(t, http.MethodGet, "/", nil)
	html := w.Body.String()
	for _, want := range []string{"转换设置", "开始转换", "任务队列", "fan-image-tr"} {
		if !strings.Contains(html, want) {
			t.Errorf("首页缺少 %q", want)
		}
	}
	if strings.Contains(html, "__ASSET_V__") {
		t.Error("首页的资源版本占位符未被替换")
	}
	// 静态资源用 immutable 缓存 + 版本号查询参数，不需要 ETag
	w = e.do(t, http.MethodGet, "/js/app.js", nil)
	if cc := w.Header().Get("Cache-Control"); !strings.Contains(cc, "immutable") {
		t.Errorf("js 资源 Cache-Control = %q，期望 immutable", cc)
	}
	// 首页应带 ETag 并支持 304
	if w := e.do(t, http.MethodGet, "/", nil); w.Header().Get("ETag") == "" {
		t.Error("首页缺少 ETag")
	} else {
		tag := w.Header().Get("ETag")
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("If-None-Match", tag)
		w2 := httptest.NewRecorder()
		e.router.ServeHTTP(w2, req)
		if w2.Code != http.StatusNotModified {
			t.Errorf("首页条件请求 = %d，期望 304", w2.Code)
		}
	}
	// SPA 回退
	for _, p := range []string{"/presets", "/anything/deep"} {
		if w := e.do(t, http.MethodGet, p, nil); w.Code != http.StatusOK {
			t.Errorf("SPA 回退 %s = %d", p, w.Code)
		}
	}
	// 越权静态路径
	if w := e.do(t, http.MethodGet, "/../../../../etc/passwd", nil); w.Code == http.StatusOK &&
		strings.Contains(w.Body.String(), "root:") {
		t.Error("静态资源可读取系统文件")
	}
	// API 404 必须是 JSON
	w = e.do(t, http.MethodGet, "/api/does-not-exist", nil)
	if w.Code != http.StatusNotFound || !strings.Contains(w.Header().Get("Content-Type"), "application/json") {
		t.Errorf("未知 API 路由 = %d %s", w.Code, w.Header().Get("Content-Type"))
	}
}

func TestBadJSONRequests(t *testing.T) {
	e := newTestEnv(t)
	for _, p := range []string{"/api/tasks", "/api/estimate", "/api/preview", "/api/presets"} {
		req := httptest.NewRequest(http.MethodPost, p, strings.NewReader("{not json"))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		e.router.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Errorf("POST %s 非法 JSON = %d，期望 400", p, w.Code)
		}
		if !strings.Contains(w.Header().Get("Content-Type"), "application/json") {
			t.Errorf("POST %s 错误响应不是 JSON: %s", p, w.Header().Get("Content-Type"))
		}
	}
}

// TestRequestLoggerAndErrorResponses 顺带验证带日志中间件的请求链路不会 panic，
// 且错误响应始终是 JSON。
func TestRequestLoggerAndErrorResponses(t *testing.T) {
	e := newTestEnv(t)
	for _, p := range []string{"/api/media/dir" + q("../etc"), "/api/health", "/api/tasks/task_missing"} {
		w := e.do(t, http.MethodGet, p, nil)
		if w.Code >= 500 {
			t.Errorf("GET %s = %d（服务端错误）", p, w.Code)
		}
		if w.Code >= 400 && !strings.Contains(w.Header().Get("Content-Type"), "application/json") {
			t.Errorf("GET %s 错误响应不是 JSON: %s", p, w.Header().Get("Content-Type"))
		}
	}
}
