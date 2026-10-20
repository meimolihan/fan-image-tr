package main

import (
	"fmt"
	"net"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/meimolihan/fan-image-tr/internal/version"
)

// ==================== 终端配色（与 install.sh / 系统工具风格对齐） ====================

const (
	clrGrey   = "\x1b[38;5;59m"
	clrRed    = "\x1b[38;5;9m"
	clrGreen  = "\x1b[38;5;10m"
	clrYellow = "\x1b[38;5;11m"
	clrBlue   = "\x1b[38;5;32m"
	clrWhite  = "\x1b[38;5;15m"
	clrPurple = "\x1b[38;5;13m"
	clrCyan   = "\x1b[38;5;14m"
	clrReset  = "\x1b[0m"
)

func paint(s, color string) string { return color + s + clrReset }

func errPrint(a ...string) {
	fmt.Println("  " + paint("[错误]", clrRed) + " " + strings.Join(a, " "))
}

func warnPrint(a ...string) {
	fmt.Println("  " + paint("[警告]", clrYellow) + " " + strings.Join(a, " "))
}

func donePrint(a ...string) { fmt.Println("  " + paint("✔", clrGreen) + " " + strings.Join(a, " ")) }

func sectionPrint(title string) { fmt.Println("  " + paint("▶", clrPurple) + " " + title) }

func sepPrint() {
	fmt.Println(paint("———————————————————", clrCyan))
}

func kvPrint(k, v string) { fmt.Println("  " + paint(padKey(k), clrBlue) + " " + paint(v, clrWhite)) }

func padKey(k string) string {
	if len(k) >= 14 {
		return k
	}
	return k + strings.Repeat(" ", 14-len(k))
}

// banner 打印应用大字标题横幅。
func banner(title string) {
	fmt.Println(paint(`
   ███████╗██╗████████╗      ████████╗██████╗
   ██╔════╝██║╚══██╔══╝      ╚══██╔══╝██╔══██╗
   █████╗  ██║   ██║            ██║   ██████╔╝
   ██╔══╝  ██║   ██║            ██║   ██╔══██╗
   ██║     ██║   ██║            ██║   ██║  ██║
   ╚═╝     ╚═╝   ╚═╝            ╚═╝   ╚═╝  ╚═╝`, clrPurple))
	fmt.Printf("%s — %s\n\n", paint("fan-image-tr v"+version.Current(), clrWhite), paint(title, clrCyan))
}

// ==================== 本地网络 / 访问地址 ====================

func isVirtualInterface(name string) bool {
	return name == "docker0" || name == "docker_gwbridge" ||
		strings.HasPrefix(name, "br-") || strings.HasPrefix(name, "veth") ||
		strings.HasPrefix(name, "virbr") || strings.HasPrefix(name, "vnet") ||
		strings.HasPrefix(name, "vmnet")
}

func localIPv4Addrs() []string {
	var addrs []string
	ifaces, err := net.Interfaces()
	if err != nil {
		return addrs
	}
	for _, ifc := range ifaces {
		if isVirtualInterface(ifc.Name) {
			continue
		}
		list, err := ifc.Addrs()
		if err != nil {
			continue
		}
		for _, a := range list {
			ip, _, err := net.ParseCIDR(a.String())
			if err != nil {
				continue
			}
			ipv4 := ip.To4()
			if ipv4 != nil && !ipv4.IsLoopback() {
				addrs = append(addrs, ipv4.String())
			}
		}
	}
	return addrs
}

// printAccess 打印 Local / Network 访问地址。
func printAccess(port int) {
	if port <= 0 {
		port = defaultPort()
	}
	kvPrint("Local access", fmt.Sprintf("http://localhost:%d", port))
	ips := localIPv4Addrs()
	if len(ips) == 0 {
		kvPrint("Network access", "未检测到局域网 IPv4 地址")
		return
	}
	for _, ip := range ips {
		kvPrint("Network access", fmt.Sprintf("http://%s:%d", ip, port))
	}
}

// ==================== 进程信息工具 ====================

func readProcField(pid int, field string) string {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, field+":") {
			return strings.TrimSpace(strings.TrimPrefix(line, field+":"))
		}
	}
	return ""
}

func procUptimeSec(pid int) int {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return 0
	}
	s := string(data)
	closeIdx := strings.LastIndex(s, ")")
	if closeIdx < 0 {
		return 0
	}
	fields := strings.Fields(s[closeIdx+2:])
	if len(fields) < 20 {
		return 0
	}
	startTicks, err := strconv.ParseFloat(fields[19], 64)
	if err != nil {
		return 0
	}
	boot, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return 0
	}
	upSec, _ := strconv.ParseFloat(strings.Fields(string(boot))[0], 64)
	elapsed := int(upSec) - int(startTicks/100)
	if elapsed > 0 {
		return elapsed
	}
	return 0
}

func formatUptime(sec int) string {
	if sec <= 0 {
		return "未知"
	}
	return fmt.Sprintf("%d小时 %d分钟 %d秒",
		sec/3600, (sec%3600)/60, sec%60)
}

// socketInodes 返回进程 /proc/<pid>/fd 中所有 socket inode。
func socketInodes(pid int) map[string]bool {
	set := map[string]bool{}
	entries, err := os.ReadDir(fmt.Sprintf("/proc/%d/fd", pid))
	if err != nil {
		return set
	}
	for _, e := range entries {
		t, err := os.Readlink(fmt.Sprintf("/proc/%d/fd/%s", pid, e.Name()))
		if err != nil {
			continue
		}
		if m := regexp.MustCompile(`^socket:\[(\d+)\]$`).FindStringSubmatch(t); m != nil {
			set[m[1]] = true
		}
	}
	return set
}

// listenPorts 返回进程正在监听的 TCP 端口列表。
func listenPorts(pid int) []int {
	inodes := socketInodes(pid)
	var ports []int
	for _, proto := range []string{"net/tcp", "net/tcp6"} {
		data, err := os.ReadFile(fmt.Sprintf("/proc/%d/%s", pid, proto))
		if err != nil {
			continue
		}
		lines := strings.Split(string(data), "\n")
		for _, line := range lines[1:] {
			fields := strings.Fields(line)
			if len(fields) < 10 || fields[3] != "0A" {
				continue
			}
			if !inodes[fields[9]] {
				continue
			}
			parts := strings.Split(fields[1], ":")
			if len(parts) < 2 {
				continue
			}
			p, err := strconv.ParseInt(parts[1], 16, 32)
			if err != nil || p <= 0 {
				continue
			}
			dup := false
			for _, existing := range ports {
				if existing == int(p) {
					dup = true
					break
				}
			}
			if !dup {
				ports = append(ports, int(p))
			}
		}
	}
	return ports
}

// listenPort 返回进程监听端口；prefer 为期望的 Web 端口，命中则优先返回。
func listenPort(pid, prefer int) int {
	ports := listenPorts(pid)
	if prefer > 0 {
		for _, p := range ports {
			if p == prefer {
				return p
			}
		}
	}
	if len(ports) > 0 {
		return ports[0]
	}
	return 0
}

// findProcess 探测运行方式与 PID：systemd → 直接运行（/proc）→ PID 文件。
func findProcess(binPath string, preferPort int) (mode string, pid int) {
	// 1) systemd
	if isSystemd() {
		if out, err := os.ReadFile(defaultPidFile); err == nil {
			if n := strings.TrimSpace(string(out)); n != "" {
				if p, perr := strconv.Atoi(n); perr == nil && p > 0 && pidAlive(n) {
					return "systemd（fan-image-tr.service）", p
				}
			}
		}
	}
	// 2) 直接运行（/proc 扫描，优先查找监听端口的进程）
	pids := matchByProc(binPath)
	for _, p := range pids {
		if listenPort(p, preferPort) > 0 {
			return "直接运行（/proc）", p
		}
	}
	if len(pids) > 0 {
		return "直接运行（/proc）", pids[0]
	}
	// 3) PID 文件兜底
	if out, err := os.ReadFile(defaultPidFile); err == nil {
		if n := strings.TrimSpace(string(out)); n != "" && pidAlive(n) {
			if p, _ := strconv.Atoi(n); p > 0 {
				return "PID 文件", p
			}
		}
	}
	return "", 0
}

// matchByProc 扫描 /proc，返回命令行包含 binPath 的进程 PID 列表（排除自身）。
func matchByProc(binPath string) []int {
	var pids []int
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return pids
	}
	selfPID := os.Getpid()
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid <= 0 {
			continue
		}
		if pid == selfPID {
			continue
		}
		cmd, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
		if err != nil {
			continue
		}
		cmdline := strings.ReplaceAll(string(cmd), "\x00", " ")
		if strings.Contains(cmdline, binPath) && !strings.Contains(cmdline, "uninstall") && !strings.Contains(cmdline, "status") {
			pids = append(pids, pid)
		}
	}
	return pids
}
