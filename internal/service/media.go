// Package service 实现业务逻辑：图片库浏览与探测、缩略图缓存、转换任务队列、转换预设。
package service

import (
	"container/list"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/meimolihan/fan-image-tr/internal/config"
	"github.com/meimolihan/fan-image-tr/internal/ffmpeg"
)

// 缩略图缓存条目上限与单条目字节上限，避免浏览大目录时把内存吃满。
const (
	thumbCacheEntries = 160
	thumbCacheBytes   = 512 << 10
	infoCacheEntries  = 512
)

// MediaService 负责图片目录浏览、尺寸探测、缩略图生成与文件整理。
type MediaService struct {
	cfg    *config.Config
	ff     *ffmpeg.Client
	logger *zap.Logger

	mu        sync.RWMutex
	infoCache map[string]infoCacheEntry
	thumbs    map[string]*list.Element
	thumbLRU  *list.List
	thumbSize int64
}

type infoCacheEntry struct {
	info    *ffmpeg.ImageInfo
	modTime time.Time
	size    int64
	expires time.Time
}

type thumbEntry struct {
	data    []byte
	modTime time.Time
	size    int64
}

// NewMediaService 创建媒体服务。
func NewMediaService(cfg *config.Config, ff *ffmpeg.Client, logger *zap.Logger) *MediaService {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &MediaService{
		cfg:       cfg,
		ff:        ff,
		logger:    logger,
		infoCache: make(map[string]infoCacheEntry),
		thumbs:    make(map[string]*list.Element),
		thumbLRU:  list.New(),
	}
}

// ==================== 路径处理 ====================

// ErrPathNotAllowed 表示请求的路径超出了允许浏览的范围。
var ErrPathNotAllowed = fmt.Errorf("路径超出允许访问的目录范围")

// allowedRoots 返回允许访问的根目录：浏览根目录、上传目录、输出目录。
// 任何文件操作都必须落在这些根目录之内。
func (s *MediaService) allowedRoots() []string {
	roots := []string{s.cfg.HomeDir(), s.cfg.UploadDir(), s.cfg.OutputDir()}
	out := roots[:0]
	for _, r := range roots {
		if r == "" {
			continue
		}
		abs, err := filepath.Abs(r)
		if err != nil {
			continue
		}
		out = append(out, filepath.Clean(abs))
	}
	return out
}

// ResolvePath 把客户端传入的路径解析为绝对路径，并校验其位于允许的根目录内。
// 相对路径基于浏览根目录解析；绝对路径仅在落在允许根目录内时接受。
func (s *MediaService) ResolvePath(p string) (string, error) {
	p = strings.TrimSpace(p)
	p = strings.ReplaceAll(p, "\\", "/")

	var abs string
	switch {
	case p == "" || p == "." || p == "/":
		abs = s.cfg.HomeDir()
	case strings.HasPrefix(p, "/"):
		abs = filepath.Clean(p)
	default:
		abs = filepath.Join(s.cfg.HomeDir(), filepath.FromSlash(p))
	}
	abs = filepath.Clean(abs)

	for _, root := range s.allowedRoots() {
		if abs == root || strings.HasPrefix(abs, root+string(filepath.Separator)) {
			return abs, nil
		}
	}
	return "", ErrPathNotAllowed
}

// RelPath 把绝对路径转换为便于前端拼接的相对路径（基于浏览根目录）。
// 不在浏览根目录内时原样返回绝对路径。
func (s *MediaService) RelPath(abs string) string {
	home, err := filepath.Abs(s.cfg.HomeDir())
	if err != nil {
		return abs
	}
	home = filepath.Clean(home)
	if abs == home {
		return ""
	}
	if rel, err := filepath.Rel(home, abs); err == nil && !strings.HasPrefix(rel, "..") {
		return filepath.ToSlash(rel)
	}
	return abs
}

// HomeDir 返回浏览根目录。
func (s *MediaService) HomeDir() string { return s.cfg.HomeDir() }

// OutputDir 返回转换输出目录。
func (s *MediaService) OutputDir() string { return s.cfg.OutputDir() }

// UploadDir 返回上传文件落盘目录。
func (s *MediaService) UploadDir() string { return s.cfg.UploadDir() }

// ==================== 目录浏览 ====================

// Entry 目录列表项。
type Entry struct {
	// Name 文件名
	Name string `json:"name"`
	// Path 相对浏览根目录的路径（斜杠分隔）
	Path string `json:"path"`
	// IsDir 是否为目录
	IsDir bool `json:"is_dir"`
	// Size 文件体积（字节）
	Size int64 `json:"size"`
	// ModTimeUnix 修改时间（秒）
	ModTimeUnix int64 `json:"mod_time"`
	// Ext 小写扩展名
	Ext string `json:"ext"`
	// IsImage 是否为可处理的图片
	IsImage bool `json:"is_image"`
	// Animated 扩展名上判断可能为动图（精确判断需探测）
	Animated bool `json:"animated"`
}

// List 目录浏览结果。
type List struct {
	// Path 当前目录（相对浏览根目录）
	Path string `json:"path"`
	// Parent 上一级目录（根目录时为空）
	Parent string `json:"parent"`
	// Dirs 子目录列表
	Dirs []Entry `json:"dirs"`
	// Files 图片文件列表
	Files []Entry `json:"files"`
	// Count 文件数量
	Count int `json:"count"`
	// TotalSize 文件总体积
	TotalSize int64 `json:"total_size"`
}

// List 列出目录下的子目录与图片文件。
func (s *MediaService) List(p string) (*List, error) {
	dir, err := s.ResolvePath(p)
	if err != nil {
		return nil, err
	}
	items, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("读取目录 %s 失败: %w", s.RelPath(dir), err)
	}

	out := &List{Path: s.RelPath(dir), Dirs: []Entry{}, Files: []Entry{}}
	// 上一级目录必须仍在允许范围内；根目录的父目录（通常是 / 或上级临时目录）不暴露给前端
	if parent := filepath.Dir(dir); parent != dir {
		if _, err := s.ResolvePath(parent); err == nil {
			out.Parent = s.RelPath(parent)
		}
	}

	for _, it := range items {
		name := it.Name()
		if strings.HasPrefix(name, ".") {
			continue // 隐藏文件与临时文件不展示
		}
		full := filepath.Join(dir, name)
		if it.IsDir() {
			info, err := it.Info()
			if err != nil {
				continue
			}
			out.Dirs = append(out.Dirs, Entry{
				Name:        name,
				Path:        s.RelPath(full),
				IsDir:       true,
				ModTimeUnix: info.ModTime().Unix(),
			})
			continue
		}
		if !it.Type().IsRegular() {
			continue
		}
		if !ffmpeg.IsImageFile(name) {
			continue
		}
		info, err := it.Info()
		if err != nil {
			continue
		}
		ext := strings.ToLower(filepath.Ext(name))
		out.Files = append(out.Files, Entry{
			Name:        name,
			Path:        s.RelPath(full),
			Size:        info.Size(),
			ModTimeUnix: info.ModTime().Unix(),
			Ext:         ext,
			IsImage:     true,
			Animated:    ext == ".gif" || ext == ".webp" || ext == ".apng" || ext == ".avif",
		})
		out.TotalSize += info.Size()
		out.Count++
	}

	sort.Slice(out.Dirs, func(i, j int) bool { return strings.ToLower(out.Dirs[i].Name) < strings.ToLower(out.Dirs[j].Name) })
	sort.Slice(out.Files, func(i, j int) bool { return strings.ToLower(out.Files[i].Name) < strings.ToLower(out.Files[j].Name) })
	return out, nil
}

// ==================== 探测与缩略图 ====================

// Info 探测单张图片的尺寸、色彩与 EXIF 方向等信息（带缓存）。
func (s *MediaService) Info(ctx context.Context, p string) (*ffmpeg.ImageInfo, error) {
	abs, err := s.ResolvePath(p)
	if err != nil {
		return nil, err
	}
	st, err := os.Stat(abs)
	if err != nil {
		return nil, fmt.Errorf("文件不存在: %s", s.RelPath(abs))
	}
	if st.IsDir() {
		return nil, fmt.Errorf("%s 是目录", s.RelPath(abs))
	}
	if !ffmpeg.IsImageFile(abs) {
		return nil, fmt.Errorf("不支持的文件类型: %s", filepath.Base(abs))
	}

	// 探测结果按「路径 + 大小 + 修改时间」缓存：文件一变就重新探测
	key := abs
	s.mu.RLock()
	cached, ok := s.infoCache[key]
	s.mu.RUnlock()
	if ok && cached.size == st.Size() && cached.modTime.Equal(st.ModTime()) && time.Now().Before(cached.expires) {
		return cached.info, nil
	}

	info, err := s.ff.Probe(ctx, abs)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	if len(s.infoCache) >= infoCacheEntries {
		// 简单粗暴：缓存满时整体清空，图片工具的访问集中度高，命中率依旧很好
		s.infoCache = make(map[string]infoCacheEntry)
	}
	s.infoCache[key] = infoCacheEntry{
		info:    info,
		modTime: st.ModTime(),
		size:    st.Size(),
		expires: time.Now().Add(5 * time.Minute),
	}
	s.mu.Unlock()
	return info, nil
}

// Thumbnail 返回图片缩略图的 JPEG 字节（带 LRU 缓存）。
func (s *MediaService) Thumbnail(ctx context.Context, p string, size int) ([]byte, error) {
	abs, err := s.ResolvePath(p)
	if err != nil {
		return nil, err
	}
	st, err := os.Stat(abs)
	if err != nil {
		return nil, fmt.Errorf("文件不存在: %s", s.RelPath(abs))
	}
	if size <= 0 {
		size = s.ff.ThumbSize()
	}
	if size > 2048 {
		size = 2048
	}

	key := fmt.Sprintf("%s@%d", abs, size)
	s.mu.RLock()
	if el, ok := s.thumbs[key]; ok {
		e := el.Value.(*thumbEntry)
		if e.size == st.Size() && e.modTime.Equal(st.ModTime()) {
			s.mu.RUnlock()
			s.mu.Lock()
			s.thumbLRU.MoveToFront(el)
			s.mu.Unlock()
			return e.data, nil
		}
	}
	s.mu.RUnlock()

	data, err := s.ff.ThumbnailBytes(ctx, abs, size)
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	if el, ok := s.thumbs[key]; ok {
		el.Value.(*thumbEntry).data = data
		el.Value.(*thumbEntry).size = st.Size()
		el.Value.(*thumbEntry).modTime = st.ModTime()
		s.thumbLRU.MoveToFront(el)
	} else {
		el := s.thumbLRU.PushFront(&thumbEntry{data: data, size: st.Size(), modTime: st.ModTime()})
		s.thumbs[key] = el
		s.evictThumbsLocked()
	}
	s.mu.Unlock()
	return data, nil
}

// evictThumbsLocked 按条目数与总体积淘汰缩略图缓存，调用方须持有写锁。
func (s *MediaService) evictThumbsLocked() {
	var total int64
	for el := s.thumbLRU.Back(); el != nil; {
		prev := el.Prev()
		e := el.Value.(*thumbEntry)
		total += int64(len(e.data))
		if s.thumbLRU.Len() > thumbCacheEntries || (total > thumbCacheBytes && s.thumbLRU.Len() > 1) {
			s.thumbLRU.Remove(el)
			for k, v := range s.thumbs {
				if v == el {
					delete(s.thumbs, k)
				}
			}
		}
		el = prev
	}
}

// ==================== 文件整理 ====================

// Delete 删除文件或空目录。
func (s *MediaService) Delete(p string) error {
	abs, err := s.ResolvePath(p)
	if err != nil {
		return err
	}
	if abs == s.cfg.HomeDir() {
		return fmt.Errorf("不允许删除根目录")
	}
	st, err := os.Stat(abs)
	if err != nil {
		return fmt.Errorf("删除失败: %w", err)
	}
	// 只删文件：目录请先自行清空，避免误删整棵目录树
	if st.IsDir() {
		return fmt.Errorf("不允许删除目录: %s", filepath.Base(abs))
	}
	if err := os.Remove(abs); err != nil {
		return fmt.Errorf("删除失败: %w", err)
	}
	s.invalidate(abs)
	return nil
}

// Rename 重命名文件或目录。
func (s *MediaService) Rename(p, newName string) error {
	abs, err := s.ResolvePath(p)
	if err != nil {
		return err
	}
	newName = strings.TrimSpace(newName)
	if newName == "" {
		return fmt.Errorf("新名称不能为空")
	}
	if newName != filepath.Base(newName) {
		return fmt.Errorf("新名称不能包含路径分隔符")
	}
	if strings.HasPrefix(newName, ".") {
		return fmt.Errorf("名称不能以点开头")
	}
	if _, err := os.Stat(abs); err != nil {
		return fmt.Errorf("文件不存在: %s", s.RelPath(abs))
	}
	dst := filepath.Join(filepath.Dir(abs), newName)
	if _, err := os.Stat(dst); err == nil {
		return fmt.Errorf("目标名称已存在: %s", newName)
	}
	if err := os.Rename(abs, dst); err != nil {
		return fmt.Errorf("重命名失败: %w", err)
	}
	s.invalidate(abs)
	return nil
}

// CreateFolder 在指定目录下新建子目录。
func (s *MediaService) CreateFolder(p, name string) error {
	dir, err := s.ResolvePath(p)
	if err != nil {
		return err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("目录名不能为空")
	}
	if name != filepath.Base(name) {
		return fmt.Errorf("目录名不能包含路径分隔符")
	}
	if strings.HasPrefix(name, ".") {
		return fmt.Errorf("目录名不能以点开头")
	}
	if err := os.Mkdir(filepath.Join(dir, name), 0755); err != nil {
		return fmt.Errorf("创建目录失败: %w", err)
	}
	return nil
}

// invalidate 清除某个路径相关的缓存（重命名 / 删除后调用）。
func (s *MediaService) invalidate(abs string) {
	s.mu.Lock()
	delete(s.infoCache, abs)
	for k, el := range s.thumbs {
		if strings.HasPrefix(k, abs) {
			s.thumbLRU.Remove(el)
			delete(s.thumbs, k)
		}
	}
	s.mu.Unlock()
}

// ProbePath 探测任意路径的图片信息（不做格式限制，供任务内部使用）。
func (s *MediaService) ProbePath(ctx context.Context, abs string) (*ffmpeg.ImageInfo, error) {
	return s.ff.Probe(ctx, abs)
}
