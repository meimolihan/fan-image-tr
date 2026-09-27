package main

import (
	"context"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/meimolihan/fan-image-tr/internal/config"
	"github.com/meimolihan/fan-image-tr/internal/ffmpeg"
)

// runCheck 输出 FFmpeg 环境与格式 / 硬件加速能力，便于部署排障。
func runCheck(args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("加载配置失败: %w", err)
	}
	if flags, err := parseFlags(args); err == nil && flags.data != "" {
		cfg.App.DataDir = flags.data
	}

	ff, err := ffmpeg.New(ffmpeg.Options{
		FFmpegBin:   cfg.FFmpegBin(),
		FFprobeBin:  cfg.FFprobeBin(),
		Threads:     cfg.FFmpeg.Threads,
		Format:      cfg.FFmpeg.Format,
		Quality:     cfg.FFmpeg.Quality,
		Accel:       cfg.FFmpeg.Accel,
		VAAPIDevice: cfg.FFmpeg.VAAPIDevice,
		ThumbSize:   cfg.FFmpeg.ThumbSize,
		ProbeTO:     cfg.FFmpeg.ProbeTimeout,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "FFmpeg 环境检查失败: ", err)
		fmt.Fprintln(os.Stderr, "请安装 ffmpeg 与 ffprobe，或用 FIT_FFMPEG_PATH 指定路径")
		return err
	}

	caps := ff.Detect(context.Background())

	fmt.Println("== FFmpeg 环境 ==")
	fmt.Println("  ffmpeg :", caps.FFmpeg)
	fmt.Println("  ffprobe:", caps.FFprobe)
	fmt.Println("  路径   :", caps.FfmpegPath)

	fmt.Println("\n== 输出格式 ==")
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "  格式\t扩展名\t可用\t动图\t透明\t无损\t说明")
	for _, f := range caps.Formats {
		mark := "✓"
		if !f.Available {
			mark = "✗"
		}
		note := f.Format.Note
		if !f.Available && f.Reason != "" {
			note = f.Reason
		}
		fmt.Fprintf(w, "  %s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			f.Format.Name, f.Format.Ext, mark,
			yesNo(f.Format.Animated), yesNo(f.Format.Alpha), yesNo(f.Format.Lossless), note)
	}
	_ = w.Flush()

	fmt.Println("\n== 硬件加速 ==")
	// 统计每个加速下真实自检通过的格式
	verified := map[string]int{}
	for _, hwf := range caps.HWFormats {
		verified[hwf.Accel]++
	}
	for _, a := range caps.HWAccel {
		mark := "✓"
		note := ""
		if !a.Available {
			mark = "✗"
			note = a.Reason
		} else if verified[a.ID] == 0 {
			mark = "△"
			note = "编码器存在，但没有通过实测的图片格式"
		} else {
			note = fmt.Sprintf("实测可用格式数 %d", verified[a.ID])
		}
		if a.Device != "" {
			note += "，设备 " + a.Device
		}
		fmt.Printf("  %s %-8s %s\n", mark, a.ID, note)
	}

	fmt.Println("\n== 汇总 ==")
	fmt.Println(" ", summarizeCaps(caps))
	if len(caps.HWFormats) > 0 {
		fmt.Println("\n  硬件编码器自检结果:")
		for id, hw := range caps.HWFormats {
			fmt.Printf("    %-6s → %s (%s)\n", id, hw.Encoder, hw.Accel)
		}
	}
	fmt.Println("\n提示: 格式不可用通常是编译时缺少对应编码器（如 libjxl 需要 libjxl、AVIF 需要 libsvtav1/libaom）。")
	return nil
}

func yesNo(b bool) string {
	if b {
		return "✓"
	}
	return "-"
}
