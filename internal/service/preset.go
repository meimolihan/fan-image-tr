package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/meimolihan/fan-image-tr/internal/config"
	"github.com/meimolihan/fan-image-tr/internal/ffmpeg"
)

// Preset 转换预设：一组可直接套用的转换参数。
type Preset struct {
	// Name 预设名称（唯一）
	Name string `json:"name"`
	// Description 备注说明
	Description string `json:"description,omitempty"`
	// Options 转换参数
	Options ffmpeg.ConvertOptions `json:"options"`
	// Variants 多尺寸变体
	Variants []Variant `json:"variants,omitempty"`
	// Overwrite 同名产物是否覆盖
	Overwrite bool `json:"overwrite"`
	// BuiltIn 是否为内置预设（内置预设不可修改与删除）
	BuiltIn bool `json:"built_in"`
	// CreatedAt / UpdatedAt
	CreatedAt time.Time `json:"created_at,omitempty"`
	UpdatedAt time.Time `json:"updated_at,omitempty"`
}

// clone 返回预设的深拷贝。
func (p *Preset) clone() *Preset {
	cp := *p
	cp.Variants = append([]Variant(nil), p.Variants...)
	return &cp
}

// ErrPresetNotFound 预设名称不存在。
var ErrPresetNotFound = errors.New("预设不存在")

// PresetService 负责转换预设的加载、保存与删除（落盘到 data/profiles.json）。
type PresetService struct {
	mu      sync.RWMutex
	path    string
	presets map[string]*Preset
}

// NewPresetService 创建预设服务并加载磁盘上的用户预设。
func NewPresetService(cfg *config.Config) (*PresetService, error) {
	s := &PresetService{
		path:    cfg.ProfilesPath(),
		presets: make(map[string]*Preset),
	}
	for _, p := range builtinPresets() {
		s.presets[p.Name] = p
	}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

// load 读取用户预设；文件不存在视为首次使用。
func (s *PresetService) load() error {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("读取预设文件失败: %w", err)
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return nil
	}
	var list []*Preset
	if err := json.Unmarshal(data, &list); err != nil {
		return fmt.Errorf("解析预设文件失败: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, p := range list {
		if p == nil {
			continue
		}
		p.Name = strings.TrimSpace(p.Name)
		if p.Name == "" {
			continue
		}
		// 内置预设名称被占用时自动改名，避免用户覆盖内置预设
		if _, exists := s.presets[p.Name]; exists {
			p.Name = p.Name + " (自定义)"
			if _, dup := s.presets[p.Name]; dup {
				continue
			}
		}
		p.BuiltIn = false
		s.presets[p.Name] = p
	}
	return nil
}

// save 持久化全部用户预设，调用方须持有写锁。
func (s *PresetService) save() error {
	list := make([]*Preset, 0, len(s.presets))
	for _, p := range s.presets {
		if p.BuiltIn {
			continue
		}
		list = append(list, p)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0755); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return fmt.Errorf("写入预设失败: %w", err)
	}
	return os.Rename(tmp, s.path)
}

// List 返回全部预设（内置在前，用户预设按名称排序）。
func (s *PresetService) List() []*Preset {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*Preset, 0, len(s.presets))
	for _, p := range s.presets {
		out = append(out, p.clone())
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].BuiltIn != out[j].BuiltIn {
			return out[i].BuiltIn
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// Get 按名称查询预设。
func (s *PresetService) Get(name string) (*Preset, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.presets[name]
	if !ok {
		return nil, false
	}
	return p.clone(), true
}

// Save 保存（新增或覆盖）一个用户预设。
func (s *PresetService) Save(p *Preset) error {
	if p == nil {
		return fmt.Errorf("预设内容为空")
	}
	p.Name = strings.TrimSpace(p.Name)
	if p.Name == "" {
		return fmt.Errorf("预设名称不能为空")
	}
	if len([]rune(p.Name)) > 24 {
		return fmt.Errorf("预设名称过长（最多 24 个字符）")
	}
	if strings.ContainsAny(p.Name, "/\\:*?\"<>|") {
		return fmt.Errorf("预设名称不能包含以下字符: / \\ : * ? \" < > |")
	}
	if err := validatePreset(p); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if old, ok := s.presets[p.Name]; ok && old.BuiltIn {
		return fmt.Errorf("「%s」是内置预设，请换一个名称", p.Name)
	}
	now := time.Now()
	cp := p.clone()
	cp.BuiltIn = false
	if old, ok := s.presets[p.Name]; ok {
		cp.CreatedAt = old.CreatedAt
	} else {
		cp.CreatedAt = now
	}
	cp.UpdatedAt = now
	s.presets[p.Name] = cp
	return s.save()
}

// Delete 删除一个用户预设。
func (s *PresetService) Delete(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.presets[name]
	if !ok {
		return fmt.Errorf("%w: %s", ErrPresetNotFound, name)
	}
	if p.BuiltIn {
		return fmt.Errorf("内置预设不可删除")
	}
	delete(s.presets, name)
	return s.save()
}

// validatePreset 校验预设参数是否合法。
func validatePreset(p *Preset) error {
	if _, ok := ffmpeg.FormatByID(p.Options.Format); !ok {
		return fmt.Errorf("输出格式无效: %s", p.Options.Format)
	}
	if p.Options.Quality < 1 || p.Options.Quality > 100 {
		return fmt.Errorf("质量值必须在 1~100 之间")
	}
	if _, ok := ffmpeg.ResizeModeByID(p.Options.Resize); !ok {
		return fmt.Errorf("缩放方式无效: %s", p.Options.Resize)
	}
	if p.Options.Resize == "exact" && (p.Options.CustomWidth <= 0 || p.Options.CustomHeight <= 0) {
		return fmt.Errorf("精确尺寸模式需要填写目标宽高")
	}
	if p.Options.Resize == "percent" && (p.Options.Size < 1 || p.Options.Size > 400) {
		return fmt.Errorf("缩放百分比需在 1~400 之间")
	}
	if p.Options.Resize != "keep" && p.Options.Resize != "exact" && p.Options.Size < 1 {
		return fmt.Errorf("缩放数值必须大于 0")
	}
	if p.Options.Adapt != "" {
		if _, ok := ffmpeg.AdaptByID(p.Options.Adapt); !ok {
			return fmt.Errorf("自适应方式无效: %s", p.Options.Adapt)
		}
	}
	if p.Options.ColorMode != "" && ffmpeg.ColorModeByID(p.Options.ColorMode).ID != p.Options.ColorMode {
		return fmt.Errorf("色彩模式无效: %s", p.Options.ColorMode)
	}
	if p.Options.Chroma != "" && ffmpeg.ChromaByID(p.Options.Chroma).ID != p.Options.Chroma {
		return fmt.Errorf("色度抽样无效: %s", p.Options.Chroma)
	}
	if _, err := normalizeVariants(p.Variants); err != nil {
		return err
	}
	return nil
}

// builtinPresets 内置预设，覆盖常见使用场景。
func builtinPresets() []*Preset {
	base := func(format string, quality int) ffmpeg.ConvertOptions {
		return ffmpeg.ConvertOptions{
			Format:        format,
			Quality:       quality,
			ColorMode:     "keep",
			AutoOrient:    true,
			Resize:        "keep",
			Background:    "white",
			StripMetadata: true,
		}
	}
	list := []*Preset{
		{
			Name:        "网页通用 WebP",
			Description: "长边 1920、质量 80，适合网站正文配图",
			Options: func() ffmpeg.ConvertOptions {
				o := base("webp", 80)
				o.Resize, o.Size = "long_edge", 1920
				o.Chroma = "420"
				return o
			}(),
			Overwrite: false,
			BuiltIn:   true,
		},
		{
			Name:        "社交分享 JPEG",
			Description: "长边 2048、质量 85，兼容性最好",
			Options: func() ffmpeg.ConvertOptions {
				o := base("jpg", 85)
				o.Resize, o.Size = "long_edge", 2048
				o.Chroma = "420"
				return o
			}(),
			Overwrite: false,
			BuiltIn:   true,
		},
		{
			Name:        "高质量 AVIF",
			Description: "同尺寸 AVIF，体积约为 WebP 的 60%，编码较慢",
			Options:     base("avif", 60),
			Overwrite:   false,
			BuiltIn:     true,
		},
		{
			Name:        "无损 PNG",
			Description: "保持尺寸与像素，适合截图、线稿、透明图",
			Options:     base("png", 100),
			Overwrite:   false,
			BuiltIn:     true,
		},
		{
			Name:        "缩略图三档",
			Description: "一次产出 320 / 640 / 1280 三档长边 WebP",
			Options:     base("webp", 80),
			Variants: []Variant{
				{Name: "小图", Resize: "long_edge", Size: 320},
				{Name: "中图", Resize: "long_edge", Size: 640},
				{Name: "大图", Resize: "long_edge", Size: 1280},
			},
			Overwrite: false,
			BuiltIn:   true,
		},
		{
			Name:        "头像 512 方形",
			Description: "512x512 居中裁剪为正方形",
			Options: func() ffmpeg.ConvertOptions {
				o := base("jpg", 88)
				o.Resize, o.CustomWidth, o.CustomHeight = "exact", 512, 512
				o.Adapt = "fill"
				return o
			}(),
			Overwrite: false,
			BuiltIn:   true,
		},
		{
			Name:        "灰度文档",
			Description: "转 8 位灰度，文字扫描件体积可降 70% 以上",
			Options: func() ffmpeg.ConvertOptions {
				o := base("jpg", 85)
				o.ColorMode = "grayscale"
				return o
			}(),
			Overwrite: false,
			BuiltIn:   true,
		},
		{
			Name:        "原格式重压",
			Description: "保持原尺寸与原格式，仅重新压缩（质量 90）",
			Options:     base("", 90),
			Overwrite:   true,
			BuiltIn:     true,
		},
	}
	return list
}
