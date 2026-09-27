package app

import (
	"strings"
	"testing"
)

func TestRDPExitStatusDistinguishesServerLogoffFromAuthentication(t *testing.T) {
	for _, tc := range []struct {
		name      string
		code      int
		connected bool
		status    string
		message   string
	}{
		{"logoff before desktop", 65548, false, "error", "尚未建立远程桌面：会话已被服务器注销"},
		{"logoff after desktop", 65548, true, "disconnected", "会话已被服务器注销"},
		{"admin logoff", 0x10002, true, "disconnected", "注销"},
		{"session taken over", 0x10005, true, "disconnected", "另一条连接"},
		{"idle timeout", 0x10003, true, "disconnected", "空闲超时"},
		{"security negotiation is another class", 0x2000c, false, "error", "安全协议协商"},
		{"authentication rejected", 0x20009, false, "error", "身份验证失败"},
		{"wrong password", 0x20015, false, "error", "身份验证失败"},
		{"TLS failure", 0x20008, false, "error", "TLS"},
		{"network interrupted", 0x2000d, true, "error", "网络连接"},
		{"user cancelled", 0x2000b, false, "disconnected", "取消"},
		{"unknown before desktop", 12, false, "error", "未能建立连接"},
		{"unknown after desktop", 12, true, "error", "意外中断"},
		{"process failure without code", -1, false, "error", "未能建立连接"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			status, message := rdpExitStatus(tc.code, tc.connected)
			if status != tc.status || !strings.Contains(message, tc.message) {
				t.Fatalf("got %s %s", status, message)
			}
			if tc.code == 65548 && strings.Contains(message, "密码") {
				t.Fatal("server logoff must not be reported as a password failure")
			}
		})
	}
}
