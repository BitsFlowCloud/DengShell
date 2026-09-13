package app

import "testing"

func TestTerminalBoundaryAcrossChunks(t *testing.T) {
	for _, tc := range []struct {
		name, sequence string
	}{
		{"chinese", "中"}, {"emoji", "🙂"},
		{"colour", "\x1b[38;2;120;240;160m"},
		{"charset", "\x1b(B"},
		{"osc-bell", "\x1b]0;窗口标题\x07"},
		{"osc-st", "\x1b]0;窗口标题\x1b\\"},
		{"dcs", "\x1bP$qm\x1b\\"},
		{"apc", "\x1b_payload\x1b\\"},
		{"c1-csi", "\u009b38;2;120;240;160m"},
		{"c1-osc", "\u009d0;窗口标题\u009c"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for split := 1; split < len(tc.sequence); split++ {
				var b terminalBoundary
				b.write([]byte(tc.sequence[:split]))
				if b.safe() {
					t.Fatalf("handoff allowed inside sequence at byte %d", split)
				}
				b.write([]byte(tc.sequence[split:]))
				if !b.safe() {
					t.Fatalf("complete sequence remains unsafe at split %d: %+v", split, b)
				}
			}
		})
	}
}

func TestTerminalBoundaryMalformedInputCanRecover(t *testing.T) {
	for _, data := range []string{"\xe4text", "\xc0\xaf", "\xf7\xbf\xbf\xbf", "\x1b[12\x18", "\x1b]title\x1b[0m"} {
		var b terminalBoundary
		for _, c := range []byte(data) {
			b.write([]byte{c})
		}
		if !b.safe() {
			t.Fatalf("parser failed to recover after %q: %+v", data, b)
		}
	}
}
