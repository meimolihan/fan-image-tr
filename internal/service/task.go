package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/meimolihan/fan-image-tr/internal/config"
	"github.com/meimolihan/fan-image-tr/internal/ffmpeg"
)

// Status 转换任务状态。
type Status string

// 任务状态取值。
const (
	StatusQueued    Status = "queued"
	StatusRunning   Status = "running"
	StatusCompleted Status = "completed"
	StatusFailed    Status = "failed"
	StatusCancelled Status = "cancelled"
)

// Terminal 判断状态是否已结束。
func (s Status) Terminal() bool {
	return s == StatusCompleted || s == StatusFailed || s == StatusCancelled
}

// Output 任务的单个输出产物（多尺寸变体时一个任务会有多个产物）。
type Output struct {
	// Variant 变体名（无变体时为空）
	Variant string `json:"variant,omitempty"`
	// Name 输出文件名
	Name string `json:"name"`
	// Path 相对浏览根目录的路径
	Path string `json:"path"`
	// Size 实际体积（字节）
	Size int64 `json:"size"`
	// Estimate 预估体积（字节，任务完成前为预估值）
	Estimate int64 `json:"estimate"`
	// Width / Height 输出尺寸
	Width  int `json:"width"`
	Height int `json:"height"`
	// MimeType 输出 MIME
	MimeType string `json:"mime_type"`
	// Done 产物是否已生成
	Done bool `json:"done"`
}

// Task 转换任务。
type Task struct {
	ID       string  `json:"id"`
	BatchID  string  `json:"batch_id,omitempty"`
	Status   Status  `json:"status"`
	Progress float64 `json:"progress"`
	// Stage 阶段说明（排队中 / 转码中 / 已完成 等）
	Stage string `json:"stage"`

	Input     string `json:"input"`
	InputName string `json:"input_name"`
	InputSize int64  `json:"input_size"`
	// SourceWidth / SourceHeight 源图尺寸
	SourceWidth  int `json:"source_width"`
	SourceHeight int `json:"source_height"`
	// SourceAnimated 源图是否为动图
	SourceAnimated bool `json:"source_animated"`
	// SourceAlpha 源图是否带透明通道
	SourceAlpha bool `json:"source_alpha"`
	// MimeType 源图 MIME
	MimeType string                `json:"mime_type"`
	Format   string                `json:"format"`
	Quality  int                   `json:"quality"`
	Accel    string                `json:"accel"`
	Options  ffmpeg.ConvertOptions `json:"options"`
	// Variants 多尺寸变体定义（保留在任务上，重试 / 恢复快照时可重新推导各产物参数）
	Variants []Variant `json:"variants,omitempty"`
	// OutputDir 产物输出目录（相对浏览根目录）
	OutputDir string    `json:"output_dir"`
	Outputs   []*Output `json:"outputs"`
	// SourceDuration 源动图时长（秒），用于折算进度
	SourceDuration float64 `json:"source_duration,omitempty"`

	// Command 本次执行的 ffmpeg 命令（首个产物）
	Command string `json:"command,omitempty"`
	// Error 失败原因
	Error string `json:"error,omitempty"`

	CreatedAt  time.Time `json:"created_at"`
	StartedAt  time.Time `json:"started_at,omitempty"`
	FinishedAt time.Time `json:"finished_at,omitempty"`
	// DurationSec 实际耗时（秒）
	DurationSec float64 `json:"duration_sec"`
}

// Variant 多尺寸变体：一套参数可产出多种规格（如缩略图大/中/小）。
type Variant struct {
	// Name 变体名（用于文件名后缀与界面展示）
	Name string `json:"name"`
	// Format 覆盖主输出格式（留空则沿用主格式）
	Format string `json:"format,omitempty"`
	// Quality 覆盖质量（<=0 沿用主质量）
	Quality int `json:"quality,omitempty"`
	// Resize / Size / CustomWidth / CustomHeight / Adapt / AllowUpscale 覆盖缩放设置
	Resize        string `json:"resize,omitempty"`
	Size          int    `json:"size,omitempty"`
	CustomWidth   int    `json:"custom_width,omitempty"`
	CustomHeight  int    `json:"custom_height,omitempty"`
	Adapt         string `json:"adapt,omitempty"`
	AllowUpscale  bool   `json:"allow_upscale,omitempty"`
	StripMetadata *bool  `json:"strip_metadata,omitempty"`
}

// TaskSpec 创建任务的请求。
type TaskSpec struct {
	// Inputs 待处理的图片路径列表（相对浏览根目录或允许范围内的绝对路径）
	Inputs []string `json:"inputs"`
	// Options 转换参数
	Options ffmpeg.ConvertOptions `json:"options"`
	// OutputDir 输出目录（相对浏览根目录或允许范围内的绝对路径），留空用配置默认值
	OutputDir string `json:"output_dir,omitempty"`
	// Overwrite 同名文件已存在时是否覆盖，否则自动追加序号
	Overwrite bool `json:"overwrite"`
	// Variants 多尺寸变体。
	// 注意：一旦提供变体，Options 描述的主产物就不再单独输出，
	// 变体列表即为全部产物，其中第一个变体作为任务展示用的主规格。
	Variants []Variant `json:"variants,omitempty"`
	// SkipProbe 跳过尺寸探测（仅用于预估，此时不会真正入队）
	SkipProbe bool `json:"skip_probe,omitempty"`
}

// 任务相关的语义化错误，handler 据此选择合适的 HTTP 状态码。
var (
	// ErrTaskNotFound 任务 ID 不存在。
	ErrTaskNotFound = errors.New("任务不存在")
	// ErrTaskConflict 任务当前状态不允许该操作（如取消已结束的任务）。
	ErrTaskConflict = errors.New("任务状态冲突")
)

// TaskService 转换任务队列。
type TaskService struct {
	cfg    *config.Config
	media  *MediaService
	ff     *ffmpeg.Client
	logger *zap.Logger

	mu      sync.RWMutex
	tasks   map[string]*Task
	order   []string
	cancels map[string]context.CancelFunc

	queue   chan *Task
	started bool
	workers int

	snapPath  string
	snapCh    chan struct{}
	stopCh    chan struct{}
	closeOnce sync.Once
	wg        sync.WaitGroup
}

// NewTaskService 创建任务服务。
func NewTaskService(cfg *config.Config, media *MediaService, ff *ffmpeg.Client, logger *zap.Logger) *TaskService {
	if logger == nil {
		logger = zap.NewNop()
	}
	workers := cfg.App.Worker
	if workers < 1 {
		workers = 1
	}
	return &TaskService{
		cfg:      cfg,
		media:    media,
		ff:       ff,
		logger:   logger,
		tasks:    make(map[string]*Task),
		cancels:  make(map[string]context.CancelFunc),
		queue:    make(chan *Task, 256),
		workers:  workers,
		snapPath: filepath.Join(cfg.App.DataDir, "tasks.json"),
		snapCh:   make(chan struct{}, 1),
		stopCh:   make(chan struct{}),
	}
}

// Start 启动工作协程并恢复上次运行的任务快照。
func (s *TaskService) Start() {
	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return
	}
	s.started = true
	s.mu.Unlock()

	s.loadSnapshot()
	s.wg.Add(1)
	go s.snapshotLoop()
	for i := 0; i < s.workers; i++ {
		s.wg.Add(1)
		go s.worker(i + 1)
	}
	s.logger.Info("转换任务队列已启动", zap.Int("workers", s.workers))
}

// Stop 停止队列（等待在途任务结束），并做最后一次快照落盘。
func (s *TaskService) Stop() {
	s.closeOnce.Do(func() {
		close(s.queue)  // worker 排空队列后退出
		close(s.stopCh) // 通知快照协程退出
	})
	s.wg.Wait()
	s.saveSnapshot()
}

// ==================== 快照持久化 ====================

// snapshotLoop 合并写盘请求，避免频繁落盘。
func (s *TaskService) snapshotLoop() {
	defer s.wg.Done()
	for {
		select {
		case <-s.stopCh:
			return
		case <-s.snapCh:
		}
		// 稍作等待，把连续的状态变更合并成一次写入
		time.Sleep(200 * time.Millisecond)
	drain:
		for {
			select {
			case <-s.snapCh:
				continue drain
			default:
				break drain
			}
		}
		select {
		case <-s.stopCh:
			return
		default:
		}
		s.saveSnapshot()
	}
}

// touch 标记需要写快照（非阻塞）。
func (s *TaskService) touch() {
	select {
	case s.snapCh <- struct{}{}:
	default:
	}
}

// flush 立即同步写盘，用于任务结束等需要立刻落盘的时机。
func (s *TaskService) flush() {
	s.saveSnapshot()
}

// loadSnapshot 读取上次运行的任务列表；上次未完成的任务标记为已中断。
func (s *TaskService) loadSnapshot() {
	data, err := os.ReadFile(s.snapPath)
	if err != nil {
		return
	}
	var list []*Task
	if err := json.Unmarshal(data, &list); err != nil {
		s.logger.Warn("任务快照解析失败，已忽略", zap.Error(err))
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	restored := 0
	for _, t := range list {
		if t == nil || t.ID == "" {
			continue
		}
		if !t.Status.Terminal() {
			t.Status = StatusFailed
			t.Error = "服务重启，任务已中断"
			t.Stage = "已中断"
			t.Progress = -1
		}
		if _, ok := s.tasks[t.ID]; ok {
			continue
		}
		s.tasks[t.ID] = t
		s.order = append(s.order, t.ID)
		restored++
	}
	if restored > 0 {
		s.logger.Info("已恢复历史任务", zap.Int("count", restored))
	}
}

// saveSnapshot 把任务列表写入磁盘。
func (s *TaskService) saveSnapshot() {
	s.mu.RLock()
	list := make([]*Task, 0, len(s.order))
	for _, id := range s.order {
		if t := s.tasks[id]; t != nil {
			list = append(list, t.clone())
		}
	}
	if len(list) > 500 {
		list = list[len(list)-500:]
	}
	// 注意：clone 只是浅拷贝 + 产物深拷贝，元素仍与 map 中的任务共享字段，
	// 因此 Marshal 必须在读锁内完成；写文件则放到锁外。
	data, err := json.Marshal(list)
	s.mu.RUnlock()
	if err != nil {
		return
	}
	tmp := s.snapPath + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		s.logger.Warn("任务快照写入失败", zap.Error(err))
		return
	}
	_ = os.Rename(tmp, s.snapPath)
}

// clone 返回任务的深拷贝，供锁外安全使用（产物与变体都需要独立副本）。
func (t *Task) clone() *Task {
	cp := *t
	cp.Variants = append([]Variant(nil), t.Variants...)
	if t.Outputs != nil {
		cp.Outputs = make([]*Output, len(t.Outputs))
		for i, o := range t.Outputs {
			if o == nil {
				continue
			}
			oc := *o
			cp.Outputs[i] = &oc
		}
	}
	return &cp
}

// ==================== 任务创建 ====================

// Create 校验参数并把任务加入队列，返回创建出的任务列表。
func (s *TaskService) Create(ctx context.Context, spec TaskSpec) ([]*Task, error) {
	if len(spec.Inputs) == 0 {
		return nil, fmt.Errorf("请至少选择一张图片")
	}
	if len(spec.Inputs) > 500 {
		return nil, fmt.Errorf("单次最多提交 500 张图片")
	}

	outputDir, err := s.media.ResolvePath(orDefault(spec.OutputDir, s.cfg.OutputDir()))
	if err != nil {
		return nil, fmt.Errorf("输出目录不可用: %w", err)
	}
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return nil, fmt.Errorf("创建输出目录失败: %w", err)
	}

	caps := s.ff.Detect(ctx)
	variants, err := normalizeVariants(spec.Variants)
	if err != nil {
		return nil, err
	}

	// 先把全部输入校验一遍，避免提交一半才报错
	inputs := make([]string, 0, len(spec.Inputs))
	for _, in := range spec.Inputs {
		abs, err := s.media.ResolvePath(in)
		if err != nil {
			return nil, fmt.Errorf("输入路径不可用（%s）: %w", in, err)
		}
		st, err := os.Stat(abs)
		if err != nil {
			return nil, fmt.Errorf("输入文件不存在: %s", filepath.Base(abs))
		}
		if st.IsDir() {
			return nil, fmt.Errorf("输入是目录而非图片: %s", filepath.Base(abs))
		}
		if !ffmpeg.IsImageFile(abs) {
			return nil, fmt.Errorf("不支持的文件类型: %s", filepath.Base(abs))
		}
		inputs = append(inputs, abs)
	}

	batchID := newID("batch")
	now := time.Now()
	created := make([]*Task, 0, len(inputs))
	pending := make([]*Task, 0, len(inputs))

	for _, abs := range inputs {
		info, err := s.media.ProbePath(ctx, abs)
		if err != nil {
			// 探测失败不阻断提交：交给 FFmpeg 在执行时报错，便于前端看到真实原因
			s.logger.Warn("预探测失败，仍继续提交", zap.String("file", filepath.Base(abs)), zap.Error(err))
			info = &ffmpeg.ImageInfo{}
		}

		task := &Task{
			ID:             newID("task"),
			BatchID:        batchID,
			Status:         StatusQueued,
			Progress:       -1,
			Stage:          "排队中",
			Input:          s.media.RelPath(abs),
			InputName:      filepath.Base(abs),
			InputSize:      fileSize(abs),
			SourceWidth:    info.Width,
			SourceHeight:   info.Height,
			SourceAnimated: info.Animated,
			SourceAlpha:    info.Alpha,
			SourceDuration: info.Duration,
			MimeType:       info.MimeType,
			OutputDir:      s.media.RelPath(outputDir),
			Variants:       variants,
			CreatedAt:      now,
			// 执行时使用的原始参数（run 阶段会按产物套用变体覆盖项）
			Options: spec.Options,
			Accel:   s.cfg.FFmpeg.Accel,
		}

		plans, format, quality, err := s.planOutputs(task, spec, variants, info, caps, outputDir)
		if err != nil {
			return nil, err
		}
		task.Outputs = plans
		task.Format = format
		task.Quality = quality
		// 返回副本：worker 会在后台继续修改 task，直接把活对象交给调用方序列化会与写入竞争
		created = append(created, task.clone())
		pending = append(pending, task)
	}

	s.mu.Lock()
	for _, t := range pending {
		s.tasks[t.ID] = t
		s.order = append(s.order, t.ID)
	}
	s.mu.Unlock()
	s.touch()

	if spec.SkipProbe {
		return created, nil
	}

	accepted := 0
	for _, t := range pending {
		select {
		case <-ctx.Done():
			return created, ctx.Err()
		case s.queue <- t:
			accepted++
		}
	}
	s.logger.Info("已加入转换队列", zap.Int("tasks", accepted), zap.String("batch", batchID))
	return created, nil
}

// planOutputs 为一个输入规划全部输出产物（无变体时为单个主产物，有变体时每个变体一个产物）。
// 同时返回主产物的格式与质量，便于任务列表展示。
func (s *TaskService) planOutputs(task *Task, spec TaskSpec, variants []Variant, info *ffmpeg.ImageInfo, caps *ffmpeg.Capabilities, outputDir string) (outputs []*Output, format string, quality int, err error) {
	base := strings.TrimSuffix(task.InputName, filepath.Ext(task.InputName))
	plans := make([]*Output, 0, len(variants)+1)

	makeOne := func(v Variant, tag string) (*Output, string, int, error) {
		o := applyVariant(spec.Options, v)
		o.SourceWidth, o.SourceHeight, o.SourceAlpha = info.Width, info.Height, info.Alpha
		clamped, err := ffmpeg.Clamp(o, caps)
		if err != nil {
			return nil, "", 0, err
		}
		f, ok := ffmpeg.FormatByID(clamped.Format)
		if !ok {
			return nil, "", 0, fmt.Errorf("不支持的输出格式: %s", clamped.Format)
		}
		name := s.uniqueName(outputDir, base, tag, f.Ext, spec.Overwrite)
		// 对外展示的质量沿用界面上的 1~100 取值；编码器原生档位（qscale/CRF）只用于内部估算与执行
		displayQuality := o.Quality
		if displayQuality <= 0 {
			displayQuality = f.DefaultQuality
		}
		return &Output{
			Variant:  v.Name,
			Name:     name,
			Path:     s.media.RelPath(filepath.Join(outputDir, name)),
			Estimate: ffmpeg.EstimateBytes(f, clamped.Quality, clamped.OutputWidth, clamped.OutputHeight, clamped.ColorMode),
			Width:    clamped.OutputWidth,
			Height:   clamped.OutputHeight,
			MimeType: f.MimeType,
		}, clamped.Format, displayQuality, nil
	}

	// 主产物：无变体时只输出一个；有变体时主产物即第一个变体
	if len(variants) == 0 {
		out, f, q, err := makeOne(Variant{}, "")
		if err != nil {
			return nil, "", 0, err
		}
		return []*Output{out}, f, q, nil
	}
	for _, v := range variants {
		out, f, q, err := makeOne(v, sanitizeTag(v.Name))
		if err != nil {
			return nil, "", 0, err
		}
		if len(plans) == 0 {
			format, quality = f, q
		}
		plans = append(plans, out)
	}
	return plans, format, quality, nil
}

// applyVariant 在主参数基础上套用变体覆盖项，返回未归一化的参数副本。
func applyVariant(base ffmpeg.ConvertOptions, v Variant) ffmpeg.ConvertOptions {
	o := base
	if v.Format != "" {
		o.Format = ffmpeg.DefaultFormatID(v.Format)
	}
	if v.Quality > 0 {
		o.Quality = v.Quality
	}
	if v.Resize != "" {
		o.Resize = v.Resize
	}
	if v.Size > 0 {
		o.Size = v.Size
	}
	if v.CustomWidth > 0 {
		o.CustomWidth = v.CustomWidth
	}
	if v.CustomHeight > 0 {
		o.CustomHeight = v.CustomHeight
	}
	if v.Adapt != "" {
		o.Adapt = v.Adapt
	}
	o.AllowUpscale = v.AllowUpscale
	if v.StripMetadata != nil {
		o.StripMetadata = *v.StripMetadata
	}
	return o
}

// uniqueName 生成不冲突的输出文件名。
// OutputName 按任务命名规则拼出产物文件名（不含"已存在则加序号"的处理）。
// 任务执行与命令预览共用，保证预览里看到的文件名就是真正会写出的那个。
func OutputName(base, tag, ext string) string {
	if tag != "" {
		return base + "_" + tag + ext
	}
	return base + ext
}

func (s *TaskService) uniqueName(dir, base, tag, ext string, overwrite bool) string {
	name := OutputName(base, tag, ext)
	if overwrite {
		return name
	}
	candidate := name
	for i := 1; i < 10000; i++ {
		if _, err := os.Stat(filepath.Join(dir, candidate)); os.IsNotExist(err) {
			return candidate
		}
		stem := strings.TrimSuffix(name, ext)
		candidate = fmt.Sprintf("%s_%d%s", stem, i, ext)
	}
	return name
}

// normalizeVariants 校验并归一化变体列表。
func normalizeVariants(in []Variant) ([]Variant, error) {
	if len(in) == 0 {
		return nil, nil
	}
	if len(in) > 8 {
		return nil, fmt.Errorf("最多支持 8 个输出变体")
	}
	out := make([]Variant, 0, len(in))
	seen := map[string]bool{}
	for _, v := range in {
		v.Name = strings.TrimSpace(v.Name)
		if v.Name == "" {
			return nil, fmt.Errorf("变体名称不能为空")
		}
		if len(v.Name) > 24 {
			return nil, fmt.Errorf("变体名称过长（最多 24 个字符）")
		}
		if seen[v.Name] {
			return nil, fmt.Errorf("变体名称重复: %s", v.Name)
		}
		seen[v.Name] = true
		if v.Resize == "exact" && (v.CustomWidth <= 0 || v.CustomHeight <= 0) {
			return nil, fmt.Errorf("变体「%s」选择了精确尺寸，请填写宽高", v.Name)
		}
		if v.Resize != "" {
			if _, ok := ffmpeg.ResizeModeByID(v.Resize); !ok {
				return nil, fmt.Errorf("变体「%s」的缩放方式无效", v.Name)
			}
		}
		if v.Adapt != "" {
			if _, ok := ffmpeg.AdaptByID(v.Adapt); !ok {
				return nil, fmt.Errorf("变体「%s」的自适应方式无效", v.Name)
			}
		}
		if v.Format != "" {
			if _, ok := ffmpeg.FormatByID(v.Format); !ok {
				return nil, fmt.Errorf("变体「%s」的输出格式无效: %s", v.Name, v.Format)
			}
		}
		out = append(out, v)
	}
	return out, nil
}

// ==================== 队列执行 ====================

// worker 从队列取任务并执行。
func (s *TaskService) worker(id int) {
	defer s.wg.Done()
	for task := range s.queue {
		s.run(task, id)
	}
}

// run 执行单个任务：按产物列表逐个转换，任一产物失败即结束任务。
func (s *TaskService) run(task *Task, workerID int) {
	ctx, cancel := context.WithCancel(context.Background())
	s.mu.Lock()
	s.cancels[task.ID] = cancel
	s.updateTask(task.ID, func(t *Task) {
		t.Status = StatusRunning
		t.Stage = "转码中"
		t.StartedAt = time.Now()
	})
	s.mu.Unlock()
	s.touch()
	defer cancel()

	s.logger.Debug("开始执行任务", zap.String("task", task.ID), zap.Int("worker", workerID))

	caps := s.ff.Detect(ctx)
	// 动图按时长折算进度；静态图没有可折算的时长，传入 0 表示进度不确定
	var expect time.Duration
	if task.SourceAnimated && task.SourceDuration > 0 {
		expect = time.Duration(task.SourceDuration * float64(time.Second))
	}

	input := s.taskInputAbs(task)
	if input == "" {
		s.finish(task, StatusFailed, "输入文件不存在")
		return
	}

	for _, out := range task.Outputs {
		if err := ctx.Err(); err != nil {
			s.finish(task, StatusCancelled, "任务已取消")
			return
		}

		// 按变体还原该产物对应的参数
		raw := applyVariant(task.Options, s.variantOf(task, out))
		raw.SourceWidth, raw.SourceHeight, raw.SourceAlpha = task.SourceWidth, task.SourceHeight, task.SourceAlpha
		raw.Threads = s.cfg.FFmpeg.Threads
		raw.Accel = s.cfg.FFmpeg.Accel
		raw.VAAPIDevice = s.cfg.FFmpeg.VAAPIDevice
		raw.HWDecode = s.cfg.FFmpeg.HWDecode

		opts, err := ffmpeg.Clamp(raw, caps)
		if err != nil {
			s.finish(task, StatusFailed, err.Error())
			return
		}

		dst := s.absOutput(task, out)
		if dst == "" {
			s.finish(task, StatusFailed, "输出路径不可用")
			return
		}

		// 记录首个产物的 ffmpeg 命令，供界面展示
		s.mu.Lock()
		needCmd := task.Command == ""
		total := len(task.Outputs)
		done := s.doneCount(task)
		if needCmd {
			if args, err := s.ff.BuildConvertArgs(*opts, caps, input, dst); err == nil {
				s.updateTask(task.ID, func(t *Task) {
					t.Command = ffmpeg.CommandLine(s.ff.FFmpegBin(), args)
				})
			}
		}
		s.mu.Unlock()

		prefix := ""
		if total > 1 {
			prefix = fmt.Sprintf("[%d/%d] ", done+1, total)
		}
		err = s.ff.RunConvert(ctx, input, dst, *opts, caps, expect, func(p ffmpeg.Progress) {
			s.mu.Lock()
			s.updateTask(task.ID, func(t *Task) {
				if p.Percent >= 0 {
					t.Progress = p.Percent
					t.Stage = fmt.Sprintf("%s转码中 %.0f%%", prefix, p.Percent)
				} else {
					t.Progress = -1
					t.Stage = prefix + "转码中"
				}
			})
			s.mu.Unlock()
		})
		if err != nil {
			if ctx.Err() != nil {
				s.finish(task, StatusCancelled, "任务已取消")
			} else {
				s.finish(task, StatusFailed, err.Error())
			}
			return
		}

		st, statErr := os.Stat(dst)
		if statErr != nil {
			s.finish(task, StatusFailed, fmt.Sprintf("产物校验失败: %v", statErr))
			return
		}
		s.mu.Lock()
		s.updateTask(task.ID, func(t *Task) {
			for _, o := range t.Outputs {
				if o == nil || o.Name != out.Name {
					continue
				}
				o.Done = true
				o.Size = st.Size()
				o.Estimate = st.Size()
			}
		})
		s.mu.Unlock()
	}

	s.finish(task, StatusCompleted, "")
}

// variantOf 取出产物对应的变体定义；无变体的主产物返回空变体。
func (s *TaskService) variantOf(task *Task, out *Output) Variant {
	if out == nil || out.Variant == "" {
		return Variant{}
	}
	for _, v := range task.Variants {
		if v.Name == out.Variant {
			return v
		}
	}
	return Variant{}
}

// doneCount 统计已完成的产物数（多产物任务的进度前缀用），调用方须持有锁。
func (s *TaskService) doneCount(task *Task) int {
	n := 0
	for _, o := range task.Outputs {
		if o != nil && o.Done {
			n++
		}
	}
	return n
}

// taskInputAbs 还原输入文件的绝对路径。
func (s *TaskService) taskInputAbs(task *Task) string {
	if filepath.IsAbs(task.Input) {
		return task.Input
	}
	abs, err := s.media.ResolvePath(task.Input)
	if err != nil {
		return ""
	}
	return abs
}

// absOutput 还原输出文件的绝对路径：输出目录 + 产物文件名。
func (s *TaskService) absOutput(task *Task, out *Output) string {
	if out == nil {
		return ""
	}
	dir, err := s.media.ResolvePath(task.OutputDir)
	if err != nil {
		return ""
	}
	return filepath.Join(dir, out.Name)
}

// finish 收尾：写入最终状态与耗时。
func (s *TaskService) finish(task *Task, status Status, errMsg string) {
	s.mu.Lock()
	s.updateTask(task.ID, func(t *Task) {
		t.Status = status
		t.Stage = stageName(status, errMsg)
		t.Error = errMsg
		t.FinishedAt = time.Now()
		if !t.StartedAt.IsZero() {
			t.DurationSec = t.FinishedAt.Sub(t.StartedAt).Seconds()
		}
		if status == StatusCompleted {
			t.Progress = 100
		} else {
			t.Progress = -1
		}
	})
	delete(s.cancels, task.ID)
	s.mu.Unlock()
	s.flush() // 任务已结束，立刻落盘，保证刷新页面/重启后状态一致

	switch status {
	case StatusCompleted:
		s.logger.Info("任务完成", zap.String("task", task.ID), zap.String("input", task.InputName))
	case StatusCancelled:
		s.logger.Info("任务已取消", zap.String("task", task.ID))
	default:
		s.logger.Error("任务失败", zap.String("task", task.ID), zap.String("input", task.InputName), zap.String("error", errMsg))
	}
}

func stageName(status Status, errMsg string) string {
	switch status {
	case StatusCompleted:
		return "已完成"
	case StatusCancelled:
		return "已取消"
	case StatusFailed:
		if errMsg == "" {
			return "失败"
		}
		return "失败：" + errMsg
	default:
		return "排队中"
	}
}

// updateTask 就地修改任务，调用方须持有写锁。
func (s *TaskService) updateTask(id string, fn func(*Task)) {
	if t, ok := s.tasks[id]; ok && t != nil {
		fn(t)
	}
}

// ==================== 查询与管理 ====================

// Get 查询单个任务。
func (s *TaskService) Get(id string) (*Task, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	t, ok := s.tasks[id]
	if !ok {
		return nil, false
	}
	return t.clone(), true
}

// List 按创建时间倒序列出任务。
func (s *TaskService) List() []*Task {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*Task, 0, len(s.order))
	for i := len(s.order) - 1; i >= 0; i-- {
		if t := s.tasks[s.order[i]]; t != nil {
			out = append(out, t.clone())
		}
	}
	return out
}

// Stats 任务统计（供顶部状态条展示）。
type Stats struct {
	Total     int `json:"total"`
	Queued    int `json:"queued"`
	Running   int `json:"running"`
	Completed int `json:"completed"`
	Failed    int `json:"failed"`
	Cancelled int `json:"cancelled"`
}

// Stats 返回任务计数统计。
func (s *TaskService) Stats() Stats {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var st Stats
	for _, t := range s.tasks {
		st.Total++
		switch t.Status {
		case StatusQueued:
			st.Queued++
		case StatusRunning:
			st.Running++
		case StatusCompleted:
			st.Completed++
		case StatusFailed:
			st.Failed++
		case StatusCancelled:
			st.Cancelled++
		}
	}
	return st
}

// Cancel 取消排队中或执行中的任务。
func (s *TaskService) Cancel(id string) error {
	s.mu.Lock()
	t, ok := s.tasks[id]
	if !ok {
		s.mu.Unlock()
		return fmt.Errorf("%w: %s", ErrTaskNotFound, id)
	}
	if t.Status.Terminal() {
		s.mu.Unlock()
		return fmt.Errorf("%w: 任务已结束，无法取消", ErrTaskConflict)
	}
	cancel := s.cancels[id]
	if t.Status == StatusQueued {
		// 还没进 worker，直接标记取消
		s.updateTask(id, func(tt *Task) {
			tt.Status = StatusCancelled
			tt.Stage = "已取消"
			tt.Progress = -1
			tt.FinishedAt = time.Now()
		})
	}
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	s.touch()
	return nil
}

// Delete 移除任务记录（仅限已结束的任务；产物文件仍保留在磁盘上）。
func (s *TaskService) Delete(id string) error {
	s.mu.Lock()
	t, ok := s.tasks[id]
	if !ok {
		s.mu.Unlock()
		return fmt.Errorf("%w: %s", ErrTaskNotFound, id)
	}
	if !t.Status.Terminal() {
		s.mu.Unlock()
		return fmt.Errorf("%w: 任务进行中，请先取消", ErrTaskConflict)
	}
	delete(s.tasks, id)
	for i, oid := range s.order {
		if oid == id {
			s.order = append(s.order[:i], s.order[i+1:]...)
			break
		}
	}
	s.mu.Unlock()
	s.touch()
	return nil
}

// Clear 清理已结束的任务记录，keepOutputs 为 true 时保留产物文件。
func (s *TaskService) Clear(keepOutputs bool) (int, error) {
	s.mu.Lock()
	kept := make([]string, 0, len(s.order))
	removed := 0
	for _, id := range s.order {
		t := s.tasks[id]
		if t == nil {
			continue
		}
		if !t.Status.Terminal() {
			kept = append(kept, id)
			continue
		}
		if keepOutputs {
			// 保留记录但不再占用列表空间：只保留最近 50 条已完成记录
			if t.Status == StatusCompleted && removed < 50 {
				kept = append(kept, id)
				removed++
				continue
			}
		}
		if !keepOutputs {
			for _, o := range t.Outputs {
				if o == nil {
					continue
				}
				if abs, err := s.media.ResolvePath(o.Path); err == nil {
					_ = os.Remove(abs)
				}
			}
		}
		delete(s.tasks, id)
		removed++
	}
	s.order = kept
	s.mu.Unlock()
	s.touch()
	return removed, nil
}

// Retry 重新入队一个失败或已取消的任务。
// 会按当前任务参数重新规划输出文件名（原产物文件保留在磁盘上）。
func (s *TaskService) Retry(ctx context.Context, id string) (*Task, error) {
	t, ok := s.Get(id)
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrTaskNotFound, id)
	}
	switch t.Status {
	case StatusFailed, StatusCancelled:
		// 可重试
	case StatusCompleted:
		return nil, fmt.Errorf("%w: 任务已成功，无需重试", ErrTaskConflict)
	default:
		return nil, fmt.Errorf("%w: 任务进行中，请先取消", ErrTaskConflict)
	}
	if len(t.Outputs) == 0 {
		return nil, fmt.Errorf("%w: 任务没有产物信息，无法重试", ErrTaskConflict)
	}

	// 探测阶段不持锁：Clamp / 文件名探测都可能较慢
	caps := s.ff.Detect(ctx)
	info := &ffmpeg.ImageInfo{
		Width: t.SourceWidth, Height: t.SourceHeight,
		Alpha: t.SourceAlpha, Animated: t.SourceAnimated, Duration: t.SourceDuration,
	}
	plans, format, quality, err := s.planOutputs(t, TaskSpec{
		Inputs:    []string{t.Input},
		Options:   t.Options,
		OutputDir: t.OutputDir,
	}, nil, info, caps, s.outputDirOf(t))
	if err != nil {
		return nil, err
	}

	// live 是队列里真正要跑的对象（与 tasks 索引里的同一个），
	// 交给调用方的则是副本：worker 会在后台持续改写 live。
	// live 是队列里真正要跑的对象（与 tasks 索引里的同一个）；
	// out 是给调用方的副本，clone 必须在锁内完成，
	// 否则 worker 可能已经开始改写 live。
	var enqueued, out *Task
	s.mu.Lock()
	s.updateTask(id, func(tt *Task) {
		tt.Status = StatusQueued
		tt.Stage = "排队中"
		tt.Progress = -1
		tt.Error = ""
		tt.Command = ""
		tt.Outputs = plans
		tt.Format = format
		tt.Quality = quality
		tt.StartedAt = time.Time{}
		tt.FinishedAt = time.Time{}
		tt.DurationSec = 0
		tt.CreatedAt = time.Now()
		enqueued, out = tt, tt.clone()
	})
	s.mu.Unlock()
	s.touch()

	select {
	case s.queue <- enqueued:
		return out, nil
	default:
		s.mu.Lock()
		s.updateTask(id, func(tt *Task) {
			tt.Status = StatusFailed
			tt.Stage = "失败：队列已满"
		})
		s.mu.Unlock()
		s.touch()
		return nil, fmt.Errorf("任务队列已满，请稍后再试")
	}
}

func (s *TaskService) outputDirOf(t *Task) string {
	if len(t.Outputs) == 0 {
		return s.cfg.OutputDir()
	}
	p := t.Outputs[0].Path
	if abs, err := s.media.ResolvePath(p); err == nil {
		return filepath.Dir(abs)
	}
	return s.cfg.OutputDir()
}

// ==================== 辅助 ====================

// SizeEstimate 单个输出的体积预估结果（供界面实时预览）。
type SizeEstimate struct {
	Width    int    `json:"width"`
	Height   int    `json:"height"`
	Bytes    int64  `json:"bytes"`
	Text     string `json:"text"`
	MimeType string `json:"mime_type"`
}

// Estimate 根据源图尺寸与参数预估输出体积。
func Estimate(opts ffmpeg.ConvertOptions, srcW, srcH int) (*SizeEstimate, error) {
	o := opts
	o.SourceWidth, o.SourceHeight = srcW, srcH
	clamped, err := ffmpeg.Clamp(o, nil)
	if err != nil {
		return nil, err
	}
	f, _ := ffmpeg.FormatByID(clamped.Format)
	bytes := ffmpeg.EstimateBytes(f, clamped.Quality, clamped.OutputWidth, clamped.OutputHeight, clamped.ColorMode)
	return &SizeEstimate{
		Width:    clamped.OutputWidth,
		Height:   clamped.OutputHeight,
		Bytes:    bytes,
		Text:     ffmpeg.FormatBytes(bytes),
		MimeType: f.MimeType,
	}, nil
}

func newID(prefix string) string {
	return fmt.Sprintf("%s_%d%d", prefix, time.Now().UnixNano(), time.Now().Unix()%1000)
}

func orDefault(v, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return v
}

func fileSize(p string) int64 {
	st, err := os.Stat(p)
	if err != nil {
		return 0
	}
	return st.Size()
}

// sanitizeTag 把变体名转换为文件名安全片段。
func sanitizeTag(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_':
			b.WriteRune(r)
		case r == ' ':
			b.WriteRune('-')
		case r > 127:
			// 中文等非 ASCII 字符直接保留
			b.WriteRune(r)
		}
	}
	out := strings.Trim(b.String(), "-_")
	if out == "" {
		out = "v" + strconv.Itoa(int(time.Now().Unix()%1000))
	}
	return out
}
