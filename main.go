// fan-image-tr 图片处理工具：内置 FFmpeg 能力的 Web 服务。
//
// 用法：
//
//	fan-image-tr [命令] [选项]
//
// 命令：
//
//	（无命令）      启动 Web 服务
//	check           检查 FFmpeg 环境并输出格式 / 硬件加速能力
//	status          查看本机服务运行状态
//	version / -v    打印版本号
//	help / -h       显示帮助
//
// 启动选项：
//
//	-port     监听端口（默认 8791）
//	-data     数据目录（任务快照、预设、上传、输出产物）
//	-media    图片浏览根目录（留空则浏览当前工作目录）
//	-output   转换产物目录（相对路径基于 -data）
//	-worker   转换并发数（默认 2）
//	-web      前端静态资源目录（留空则使用二进制内嵌资源）
//	-debug    开启调试日志
package main

import (
	"fmt"
	"os"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "错误: ", err)
		os.Exit(1)
	}
}
