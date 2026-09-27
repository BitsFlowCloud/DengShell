package app

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// Exercise the actual Windows process exit code (Unix truncates it to 8 bits),
// stdout lifecycle marker and UI session status without contacting a server.
func TestRDPWindowsServerLogoffLifecycle(t *testing.T) {
	for _, connected := range []bool{false, true} {
		name, expected, ready := "during login", "error", "0"
		if connected {
			name, expected, ready = "after desktop ready", "disconnected", "1"
		}
		t.Run(name, func(t *testing.T) {
			a, err := New(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer a.Close()
			self, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			a.rdp.engine = func() (string, error) { return self, nil }
			a.rdp.command = func(ctx context.Context, engine string, args ...string) *exec.Cmd {
				cmd := exec.CommandContext(ctx, engine, "-test.run=^TestRDPHelperProcess$")
				cmd.Env = append(os.Environ(), "DENGSHELL_RDP_TEST_HELPER=1", "DENGSHELL_RDP_TEST_EXIT=65548", "DENGSHELL_RDP_TEST_READY="+ready)
				return cmd
			}
			p, err := a.store.Save(Profile{Name: "Exit-code fixture", Protocol: "rdp", Host: "localhost", User: "test", Secret: "helper-password"}, false)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = a.startRDP(p.ID, ""); err != nil {
				t.Fatal(err)
			}
			a.rdp.wg.Wait()
			items := a.rdpSessions()
			if len(items) != 1 || items[0].Status != expected || !strings.Contains(items[0].Message, "注销") || strings.Contains(items[0].Message, "密码") {
				t.Fatalf("incorrect exit status: %+v", items)
			}
			if !connected && (!strings.Contains(items[0].Message, "尚未建立") || !strings.Contains(items[0].Message, "65548")) {
				t.Fatalf("missing login-phase diagnosis: %+v", items)
			}
		})
	}
}
