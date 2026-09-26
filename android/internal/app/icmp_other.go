//go:build !windows

package app

import (
	"context"
	"net/netip"
	"os"
	"os/exec"
	"regexp"
	"runtime"
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
	tool, args := pingCommandForPlatform(runtime.GOOS, address, timeout)
	path, err := exec.LookPath(tool)
	if err != nil {
		result.Error = "本机没有 ping 工具，请安装 iputils-ping"
		if runtime.GOOS == "darwin" {
			result.Error = "无法调用 macOS 系统网络工具：" + tool
		}
		return result
	}
	return runICMPCommand(ctx, runtime.GOOS, address, path, args, timeout)
}

func runICMPCommand(ctx context.Context, platform string, address netip.Addr, path string, args []string, timeout time.Duration) ICMPProbeResult {
	result := ICMPProbeResult{Address: address.String(), Status: "unavailable"}
	interruptForSummary := platform == "darwin" && address.Is6()
	processTimeout := timeout + 250*time.Millisecond
	if interruptForSummary {
		processTimeout = timeout
	}
	probeCtx, cancel := context.WithTimeout(ctx, processTimeout)
	defer cancel()
	command := exec.CommandContext(probeCtx, path, args...)
	if interruptForSummary {
		// Apple's ping6 has no supported receive-timeout option. SIGINT asks
		// it to print packet statistics and exit; kill it if it fails to stop.
		command.Cancel = func() error { return command.Process.Signal(os.Interrupt) }
		command.WaitDelay = 250 * time.Millisecond
	}
	command.Env = append(os.Environ(), "LC_ALL=C", "LANG=C", "IPUTILS_PING_PTR_LOOKUP=0")
	output, err := command.CombinedOutput()
	if ctx.Err() != nil {
		result.Error = "ICMP 检测已取消"
		return result
	}
	if probeCtx.Err() != nil && !(interruptForSummary && command.ProcessState != nil && command.ProcessState.Exited()) {
		result.Error = "本机 ping 进程未按时结束；未计入网络丢包"
		return result
	}
	if command.ProcessState == nil {
		result.Error = "无法启动本机 ping：" + err.Error()
		return result
	}
	return parsePingOutputForPlatform(platform, address.String(), string(output), command.ProcessState.ExitCode())
}

func pingCommandForPlatform(platform string, address netip.Addr, timeout time.Duration) (string, []string) {
	if platform == "darwin" {
		seconds := strconv.FormatInt(int64((timeout+time.Second-1)/time.Second), 10)
		if address.Is6() {
			return "/sbin/ping6", []string{"-n", "-c", "1", address.String()}
		}
		milliseconds := strconv.FormatInt(int64((timeout+time.Millisecond-1)/time.Millisecond), 10)
		return "/sbin/ping", []string{"-n", "-c", "1", "-W", milliseconds, "-t", seconds, address.String()}
	}
	family := "-4"
	if address.Is6() {
		family = "-6"
	}
	return "ping", []string{family, "-n", "-c", "1", "-W", strconv.FormatFloat(timeout.Seconds(), 'f', 3, 64), "--", address.String()}
}

func parsePingOutputForPlatform(platform, address, output string, exitCode int) ICMPProbeResult {
	if platform == "darwin" {
		// BSD uses 2 for no response; iputils uses 1. Keep invocation errors
		// distinct even when an error contains packet statistics.
		if exitCode == 2 {
			exitCode = 1
		} else if exitCode == 1 {
			exitCode = 2
		}
	}
	return parsePingOutput(address, output, exitCode)
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
