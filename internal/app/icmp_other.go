//go:build !windows

package app

import (
	"context"
	"net/netip"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var pingRTT = regexp.MustCompile(`(?m)time([=<])([0-9]+(?:\.[0-9]+)?)\s*ms`)
var pingSummary = regexp.MustCompile(`(?m)(\d+) packets transmitted,\s*(\d+)(?: packets)? received`)
var pingAverage = regexp.MustCompile(`(?m)(?:rtt|round-trip)[^=]*=\s*[0-9.]+/([0-9.]+)/`)
var pingReplyFrom = regexp.MustCompile(`(?m)^(?:From\s+|[0-9]+\s+bytes\s+from\s+)(\S+)`)

// Linux uses iputils' ICMP socket and numeric RTT output, never elapsed command
// execution time. Its own one-second receive timeout is bounded by an additional
// process deadline so cancellation cannot leave accumulating ping children.
func ProbeICMP(ctx context.Context, targetIP string, timeout time.Duration) ICMPProbeResult {
	result := ICMPProbeResult{Address: targetIP, Status: "unavailable"}
	address, err := netip.ParseAddr(targetIP)
	if err != nil || address.IsUnspecified() || address.IsMulticast() {
		result.Error = "ICMP 目标必须是有效的单播 IP 地址"
		return result
	}
	address = address.Unmap()
	targetIP = address.String()
	result.Address = targetIP
	if timeout <= 0 {
		result.Error = "ICMP 超时时间必须大于零"
		return result
	}
	if ctx.Err() != nil {
		result.Error = "ICMP 检测已取消"
		return result
	}
	path, err := exec.LookPath("ping")
	if err != nil {
		result.Error = "本机没有 ping 工具，请安装 iputils-ping"
		return result
	}
	probeCtx, cancel := context.WithTimeout(ctx, timeout+250*time.Millisecond)
	defer cancel()
	family := "-4"
	if address.Is6() && !address.Is4In6() {
		family = "-6"
	}
	command := exec.CommandContext(probeCtx, path, family, "-n", "-c", "1", "-W", strconv.FormatFloat(timeout.Seconds(), 'f', 3, 64), "--", targetIP)
	command.Env = append(os.Environ(), "LC_ALL=C", "LANG=C", "IPUTILS_PING_PTR_LOOKUP=0")
	output, err := command.CombinedOutput()
	if ctx.Err() != nil {
		result.Error = "ICMP 检测已取消"
		return result
	}
	if probeCtx.Err() != nil {
		result.Error = "本机 ping 进程未按时结束；未计入网络丢包"
		return result
	}
	if command.ProcessState == nil {
		result.Error = "无法启动本机 ping：" + err.Error()
		return result
	}
	return parsePingOutput(targetIP, string(output), command.ProcessState.ExitCode())
}

func parsePingOutput(address, output string, exitCode int) ICMPProbeResult {
	result := ICMPProbeResult{Address: address, Status: "unavailable"}
	if exitCode == 0 {
		match := pingAverage.FindStringSubmatch(output)
		if len(match) > 1 {
			result.Milliseconds, _ = strconv.ParseFloat(match[1], 64)
			result.Status = "reply"
			result.Responded = true
			return result
		}
		match = pingRTT.FindStringSubmatch(output)
		if len(match) > 2 {
			result.Milliseconds, _ = strconv.ParseFloat(match[2], 64)
			result.Status = "reply"
			result.Responded = true
			if match[1] == "<" {
				result.Error = "ping 输出为小于 " + match[2] + " ms，此值为显示上限"
			}
			return result
		}
		result.Error = "ping 未返回可识别的 ICMP 往返时间"
		return result
	}
	if exitCode == 1 {
		// iputils exit status 1 denotes no reply; require a transmitted packet
		// summary as well so a different executable cannot fabricate packet loss.
		match := pingSummary.FindStringSubmatch(output)
		if len(match) > 2 && number(match[1]) > 0 && number(match[2]) == 0 {
			result.Status = "timeout"
			result.Error = "ICMP 在超时时间内未收到回包；目标或路径可能禁用 ICMP"
			lower := strings.ToLower(output)
			if strings.Contains(lower, "unreachable") || strings.Contains(lower, "time to live exceeded") || strings.Contains(lower, "time exceeded") {
				result.Status = "unreachable"
				result.Responded = true
				result.Address = ""
				if source := pingReplyFrom.FindStringSubmatch(output); len(source) > 1 {
					if ip, err := netip.ParseAddr(strings.TrimSuffix(source[1], ":")); err == nil {
						result.Address = ip.Unmap().String()
					}
				}
				result.TTLExpired = strings.Contains(lower, "time to live exceeded") || strings.Contains(lower, "time exceeded")
				result.Error = "收到 ICMP 不可达或超时响应"
			}
			return result
		}
	}
	message := strings.TrimSpace(output)
	if message == "" {
		message = "ping 退出代码 " + strconv.Itoa(exitCode)
	}
	if len(message) > 500 {
		message = message[:500]
	}
	result.Error = "本机 ICMP 检测不可用：" + message
	return result
}
