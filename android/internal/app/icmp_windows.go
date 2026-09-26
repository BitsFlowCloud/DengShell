//go:build windows

package app

import (
	"context"
	"encoding/binary"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var icmpLibrary = windows.NewLazySystemDLL("iphlpapi.dll")

type icmpOptions struct {
	TTL, TOS, Flags, OptionsSize byte
	OptionsData                  uintptr
}
type icmpSockaddr6 struct {
	Family, Port uint16
	Flow         uint32
	Address      [16]byte
	Scope        uint32
}

func ProbeICMP(ctx context.Context, targetIP string, timeout time.Duration) ICMPProbeResult {
	return probeICMPTTL(ctx, targetIP, 128, timeout)
}

// The synchronous Windows API writes reply memory only during this call. Calls
// are bounded to one second, so cancellation never leaves a goroutine, borrowed
// Go buffer or pending asynchronous native operation behind.
func probeICMPTTL(ctx context.Context, targetIP string, ttl byte, timeout time.Duration) ICMPProbeResult {
	result := ICMPProbeResult{Address: targetIP, Status: "unavailable"}
	if err := ctx.Err(); err != nil {
		result.Error = err.Error()
		return result
	}
	ip, err := netip.ParseAddr(targetIP)
	if err != nil || ip.IsUnspecified() || ip.IsMulticast() {
		result.Error = "ICMP 目标必须是 IP 地址"
		return result
	}
	ip = ip.Unmap()
	if timeout > time.Second {
		timeout = time.Second
	}
	if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) < timeout {
		timeout = time.Until(deadline)
	}
	if timeout <= 0 {
		result.Error = "探测已超时或取消"
		return result
	}
	createName, sendName := "IcmpCreateFile", "IcmpSendEcho2"
	if ip.Is6() {
		createName, sendName = "Icmp6CreateFile", "Icmp6SendEcho2"
	}
	create, send, closeHandle := icmpLibrary.NewProc(createName), icmpLibrary.NewProc(sendName), icmpLibrary.NewProc("IcmpCloseHandle")
	for _, proc := range []*windows.LazyProc{create, send, closeHandle} {
		if err := proc.Find(); err != nil {
			result.Error = err.Error()
			return result
		}
	}
	handle, _, callErr := create.Call()
	if handle == 0 || handle == ^uintptr(0) {
		result.Error = fmt.Sprintf("创建 ICMP 探测：%v", callErr)
		return result
	}
	defer closeHandle.Call(handle)
	payload := []byte("DengShell ICMP connectivity probe")
	buffer := make([]byte, 512)
	options := icmpOptions{TTL: ttl}
	ms := uintptr(max(1, timeout.Milliseconds()))
	var count uintptr
	if ip.Is4() {
		address := ip.As4()
		count, _, callErr = send.Call(handle, 0, 0, 0, uintptr(binary.LittleEndian.Uint32(address[:])), uintptr(unsafe.Pointer(&payload[0])), uintptr(len(payload)), uintptr(unsafe.Pointer(&options)), uintptr(unsafe.Pointer(&buffer[0])), uintptr(len(buffer)), ms)
	} else {
		source, destination := icmpSockaddr6{Family: 23}, icmpSockaddr6{Family: 23, Address: ip.As16()}
		if zone := ip.Zone(); zone != "" {
			index, e := strconv.ParseUint(zone, 10, 32)
			if e != nil {
				iface, e := net.InterfaceByName(zone)
				if e != nil {
					result.Error = e.Error()
					return result
				}
				index = uint64(iface.Index)
			}
			destination.Scope = uint32(index)
		}
		count, _, callErr = send.Call(handle, 0, 0, 0, uintptr(unsafe.Pointer(&source)), uintptr(unsafe.Pointer(&destination)), uintptr(unsafe.Pointer(&payload[0])), uintptr(len(payload)), uintptr(unsafe.Pointer(&options)), uintptr(unsafe.Pointer(&buffer[0])), uintptr(len(buffer)), ms)
	}
	if err := ctx.Err(); err != nil {
		result.Error = err.Error()
		return result
	}
	var status uint32
	result.Responded = count > 0
	if count == 0 {
		if errno, ok := callErr.(windows.Errno); ok {
			status = uint32(errno)
		}
		if status == 0 {
			result.Error = "Windows ICMP 未返回结果"
			return result
		}
	} else if ip.Is4() {
		result.Address = net.IP(buffer[:4]).String()
		status = binary.LittleEndian.Uint32(buffer[4:8])
		result.Milliseconds = float64(binary.LittleEndian.Uint32(buffer[8:12]))
	} else {
		// The native reply's address starts after the packed port + flowinfo
		// (6 bytes), not sockaddr_in6's 8-byte family + port + flowinfo prefix.
		// IPV6_ADDRESS_EX remains 28 bytes including scope/alignment padding.
		result.Address = net.IP(buffer[6:22]).String()
		status = binary.LittleEndian.Uint32(buffer[28:32])
		result.Milliseconds = float64(binary.LittleEndian.Uint32(buffer[32:36]))
	}
	switch status {
	case 0:
		result.Status = "reply"
	case 11010:
		result.Status = "timeout"
		result.Error = "ICMP 请求超时"
	case 11013, 11014, 11041:
		result.Status = "unreachable"
		result.TTLExpired = true
		result.Error = "TTL 到期"
	case 11002, 11003, 11004, 11005, 11009, 11040:
		result.Status = "unreachable"
		result.Error = fmt.Sprintf("ICMP 目标不可达 (%d)", status)
	default:
		// IP_BAD_DESTINATION (11018), permissions and malformed/local API
		// failures are unavailable probes, not lost network packets.
		result.Error = fmt.Sprintf("Windows ICMP 无法探测 (%d)", status)
	}
	return result
}
