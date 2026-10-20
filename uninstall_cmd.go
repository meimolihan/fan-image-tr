package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/meimolihan/fan-image-tr/internal/config"
)

// readConfigOrDir 优先读取配置得到数据目录；配置不可用时返回调用方提供的默认值。
func readConfigOrDir(fallback string) string {
	cfg, err := config.Load()
	if err == nil && cfg.App.DataDir != "" {
		return cfg.App.DataDir
	}
	return fallback
}

// uninstallOpts 解析并校验 uninstall 子命令选项。
type uninstallOpts struct {
	yes   bool // -y / --yes
	purge bool // --purge / --delete-data
	keep  bool // --keep-data
}

func parseUninstallArgs(args []string) (*uninstallOpts, bool, error) {
	o := &uninstallOpts{}
	for _, a := range args {
		switch a {
		case "-y", "--yes":
			o.yes = true
		case "--purge", "--delete-data":
			o.purge = true
		case "--keep-data":
			o.keep = true
		case "-h", "--help":
			return o, true, nil
		default:
			return nil, false, fmt.Errorf("未知参数: %s（使用 -h 查看帮助）", a)
		}
	}
	if o.purge && o.keep {
		return nil, false, fmt.Errorf("--purge 与 --keep-data 不能同时使用")
	}
	return o, false, nil
}

func uninstallUsage() {
	fmt.Println("用法: fan-image-tr uninstall [选项]")
	fmt.Println()
	fmt.Println("选项:")
	for _, r := range [][2]string{
		{"-y, --yes", "免确认，静默卸载（默认保留数据目录）"},
		{"--purge", "卸载时同时删除数据目录"},
		{"--keep-data", "卸载时保留数据目录"},
		{"-h, --help", "显示帮助"},
	} {
		fmt.Printf("  %s %s\n", paint(r[0]+strings.Repeat(" ", 18-len(r[0])), clrWhite), paint(r[1], clrGrey))
	}
	fmt.Println()
	fmt.Println("示例:")
	fmt.Println("  sudo fan-image-tr uninstall -y            # 免确认卸载，保留数据目录")
	fmt.Println("  sudo fan-image-tr uninstall -y --purge    # 免确认卸载，并删除数据目录")
}

// runUninstall 停止服务并移除安装（数据目录默认保留，询问后清除）。
func runUninstall(args []string) error {
	o, showHelp, err := parseUninstallArgs(args)
	if err != nil {
		errPrint(err.Error())
		uninstallUsage()
		return err
	}
	if showHelp {
		uninstallUsage()
		return nil
	}

	banner("卸载")
	sepPrint()

	fmt.Println("正在卸载 fan-image-tr ...")

	rec := readRecord()
	binPath := floatNull(rec["BIN_PATH"], defaultBinPath)
	dataDir := floatNull(rec["DATA_DIR"], readConfigOrDir(defaultDataDir))
	installDir := floatNull(rec["INSTALL_DIR"], dirOf(binPath))

	// 1. 停止并禁用 systemd 服务
	if isSystemd() {
		runCmd("systemctl", "stop", "fan-image-tr.service")
		runCmd("systemctl", "disable", "fan-image-tr.service")
		if err := os.Remove(defaultServiceFile); err != nil && !os.IsNotExist(err) {
			fmt.Printf("提示: 移除服务文件失败（%v），可手动删除 %s\n", err, defaultServiceFile)
		}
		runCmd("systemctl", "daemon-reload")
	} else {
		// 非 systemd：尽力结束残留进程
		stopByPidFile()
	}

	// 2. 移除二进制
	if err := os.Remove(binPath); err != nil {
		if os.IsNotExist(err) {
			fmt.Printf("未发现安装二进制 %s，跳过\n", binPath)
		} else {
			fmt.Printf("提示: 移除二进制失败（%v），可手动删除 %s\n", err, binPath)
		}
	} else {
		fmt.Printf("已移除二进制: %s\n", binPath)
	}

	// 3. 移除记录文件
	_ = os.Remove(defaultRecordFile)
	fmt.Printf("已移除记录文件: %s\n", defaultRecordFile)

	// 4. 数据目录
	if dataDir != "" {
		fmt.Printf("数据目录（含预设、上传与转换产物）位于: %s\n", dataDir)
		remove := o.purge
		if !remove && !o.keep && !o.yes {
			remove = confirm("是否同时清除数据目录？此操作不可恢复")
		}
		if remove {
			if err := os.RemoveAll(dataDir); err != nil {
				fmt.Printf("提示: 清除数据目录失败（%v）\n", err)
			} else {
				donePrint("已清除数据目录:", dataDir)
			}
		} else {
			donePrint("已保留数据目录:", dataDir)
		}
	}
	if installDir != "" && installDir != dataDir && dirExists(installDir) {
		fmt.Printf("安装目录位于: %s\n", installDir)
		if o.purge || confirm("是否同时移除空安装目录？") {
			if emptyDir(installDir) {
				_ = os.Remove(installDir)
				donePrint("已移除安装目录:", installDir)
			} else {
				fmt.Printf("安装目录非空，已保留: %s\n", installDir)
			}
		}
	}

	fmt.Println("卸载完成。")
	return nil
}

// stopByPidFile 读取 PID 文件终止进程（非 systemd 环境）。
func stopByPidFile() {
	data, err := os.ReadFile(defaultPidFile)
	if err != nil {
		return
	}
	pid := strings.TrimSpace(string(data))
	if pid == "" {
		return
	}
	_ = exec.Command("kill", pid).Run()
	_ = os.Remove(defaultPidFile)
}

// dirExists 判断目录是否存在。
func dirExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

// emptyDir 判断目录是否为空（不含任何条目）。
func emptyDir(p string) bool {
	es, err := os.ReadDir(p)
	return err == nil && len(es) == 0
}

// runCmd 忽略错误执行外部命令（卸载过程尽量继续）。
func runCmd(name string, args ...string) {
	_ = exec.Command(name, args...).Run()
}
