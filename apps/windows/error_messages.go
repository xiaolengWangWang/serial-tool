//go:build windows

package main

import (
	"context"
	"errors"
	"net"
	"os"
	"strings"
	"unicode"
)

// Keep diagnostic text intact, with a Chinese explanation before OS/driver errors.
func chineseError(err error) string {
	if err == nil {
		return ""
	}
	detail := err.Error()
	lower := strings.ToLower(detail)
	var network net.Error
	var dns *net.DNSError
	message := ""
	switch {
	case errors.Is(err, context.Canceled):
		message = "操作已取消。"
	case errors.Is(err, os.ErrPermission) || strings.Contains(lower, "access is denied") || strings.Contains(lower, "permission denied"):
		message = "无法访问目标。请检查文件或设备权限；串口也可能正被其他程序占用。"
	case errors.Is(err, os.ErrNotExist) || strings.Contains(lower, "cannot find the file") || strings.Contains(lower, "cannot find the path"):
		message = "找不到文件、目录或设备。请检查路径，或刷新串口列表后重新选择。"
	case errors.As(err, &dns):
		message = "无法解析服务器地址。请检查域名、网络连接和 DNS 设置。"
	case errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &network) && network.Timeout()) || strings.Contains(lower, "timed out"):
		message = "等待响应超时。请检查目标是否在线、地址与端口是否正确，以及防火墙设置。"
	case strings.Contains(lower, "actively refused") || strings.Contains(lower, "connection refused"):
		message = "连接被拒绝。请确认目标服务已启动，并正在监听所填端口。"
	case strings.Contains(lower, "address already in use") || strings.Contains(lower, "only one usage of each socket address"):
		message = "本地端口已被占用。请更换监听端口，或关闭占用该端口的程序。"
	case strings.Contains(lower, "connection reset") || strings.Contains(lower, "forcibly closed") || strings.Contains(lower, "broken pipe"):
		message = "连接已中断。请检查对端与网络状态，重新连接后再试。"
	case strings.Contains(lower, "no such host") || strings.Contains(lower, "host is unknown"):
		message = "服务器地址无效或无法解析。请检查 IP 地址或域名。"
	case strings.Contains(lower, "certificate") || strings.Contains(lower, "x509:"):
		message = "无法验证服务器证书。请检查系统时间、服务地址及证书配置。"
	case strings.Contains(lower, "invalid syntax") || strings.Contains(lower, "invalid byte"):
		message = "输入格式无效。请检查数字字段；HEX 数据应由两位十六进制字节组成。"
	case strings.Contains(lower, "invalid port") || strings.Contains(lower, "too many colons") || strings.Contains(lower, "missing port in address"):
		message = "地址或端口格式无效。请检查主机地址，端口应为 1–65535。"
	}
	if message == "" {
		for _, r := range detail {
			if unicode.Is(unicode.Han, r) {
				return detail
			}
		}
		message = "操作失败。请检查输入和连接状态；下方技术详情可用于排查。"
	}
	return message + "\r\n\r\n技术详情：" + detail
}
