package app

import (
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Optional browser integration, with an actual SFTP server and only temp files.
// DENGSHELL_PUPPETEER points to puppeteer-core's ESM entry point.
func TestUploadConfirmationBrowser(t *testing.T) {
	module := os.Getenv("DENGSHELL_PUPPETEER")
	if module == "" {
		t.Skip("set DENGSHELL_PUPPETEER for browser integration")
	}
	_, session, remote, _ := uploadSFTPFixture(t, 0)
	a, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	a.sessions[session.ID] = session
	defer func() { a.mu.Lock(); delete(a.sessions, session.ID); a.mu.Unlock() }()
	server := httptest.NewUnstartedServer(a.Handler(os.DirFS("../../mobile/assets/web")))
	defer server.Close()
	a.baseURL = "http://" + server.Listener.Addr().String()
	server.Start()
	script, err := filepath.Abs("../../../scripts/test-upload-confirmation.mjs")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("node", script, a.BrowserURL(), session.ID, remote, t.TempDir(), module)
	output, err := cmd.CombinedOutput()
	t.Log(string(output))
	if err != nil {
		t.Fatal(err)
	}
}
