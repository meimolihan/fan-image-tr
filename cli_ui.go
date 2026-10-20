package main

import (
	"fmt"
	"strings"

	"github.com/meimolihan/fan-image-tr/internal/version"
)

// printVersion 输出版本信息。
func printVersion() {
	fmt.Printf("fan-image-tr v%s\n", version.Current())
}

// printHelp 打印帮助（banner + 命令表 + 访问地址 + 示例）。
func printHelp() {
	banner("管理命令")
	rows := [][2]string{
		{"status", "显示运行方式（systemd / 直接运行）、PID、端口、访问地址、运行时长、内存、路径、健康检查"},
		{"start | stop | restart", "启动 / 停止 / 重启服务（systemd 优先，其次直接管理进程）"},
		{"uninstall [-y] [--purge|--keep-data]", "停止并移除服务/进程，删除程序与安装记录；可选删除数据目录"},
		{"check", "检查 FFmpeg 环境，输出格式与硬件加速能力"},
		{"backup | restore", "备份 / 恢复数据目录操作说明"},
		{"version, -version, --version, -v", "显示版本号"},
		{"help, -h, --help", "显示本帮助"},
		{"（无命令，加 -port/-data/-media/-output/-worker）", "启动 Web 服务"},
	}
	width := 0
	for _, r := range rows {
		if n := len([]rune(r[0])); n > width {
			width = n
		}
	}
	for _, r := range rows {
		padded := r[0] + strings.Repeat(" ", width-len([]rune(r[0]))+2)
		fmt.Printf("  %s %s\n", paint(padded, clrWhite), paint(r[1], clrGrey))
	}
	fmt.Println()
	sectionPrint("访问地址")
	printAccess(defaultPortFromRecord())
	fmt.Println()
	fmt.Println(paint("使用示例：", clrCyan))
	for _, line := range []string{
		"  fan-image-tr status",
		"  fan-image-tr restart",
		"  fan-image-tr check",
		"  fan-image-tr -port 8791 -data /var/lib/fan-image-tr/data -media /volume1/photo -worker 2",
		"  sudo fan-image-tr uninstall -y            # 免确认卸载，保留数据目录",
		"  sudo fan-image-tr uninstall -y --purge    # 免确认卸载，并删除数据目录",
	} {
		fmt.Println(paint("    "+line, clrGreen))
	}
}

// commandUsage 打印简短用法（未知参数等场景）。
func commandUsage() {
	fmt.Println("用法: fan-image-tr [命令] [选项]")
	fmt.Println()
	fmt.Println("命令:")
	for _, r := range [][2]string{
		{"status", "查看运行状态"},
		{"start | stop | restart", "启动 / 停止 / 重启服务"},
		{"uninstall [-y] [--purge|--keep-data]", "停止服务并移除安装"},
		{"check", "检查 FFmpeg 环境"},
		{"version", "显示版本号"},
		{"help", "显示帮助"},
		{"backup | restore", "备份 / 恢复说明"},
	} {
		fmt.Printf("  %-38s%s\n", paint(r[0], clrWhite), paint(r[1], clrGrey))
	}
	fmt.Println()
	fmt.Println("（无命令时启动 Web 服务，可加 -port/-data/-media/-output/-worker 选项）")
}

// confirm 交互式二次确认。
func confirm(prompt string) bool {
	fmt.Printf("%s [y/N]: ", prompt)
	var input string
	_, err := fmt.Scanln(&input)
	if err != nil {
		return false
	}
	input = strings.TrimSpace(strings.ToLower(input))
	return input == "y" || input == "yes"
}
