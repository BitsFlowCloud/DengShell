//go:build !windows

package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

func TestMTRRemoteDetachedFixture(t *testing.T) {
	key := os.Getenv("CLOUDSHELL_TEST_KEY")
	if key == "" {
		t.Skip("dedicated localhost:19225 SSH fixture required")
	}
	data, err := os.ReadFile(key)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.ParsePrivateKey(data)
	if err != nil {
		t.Fatal(err)
	}
	dial := func() *ssh.Client {
		client, err := ssh.Dial("tcp", "127.0.0.1:19225", &ssh.ClientConfig{User: os.Getenv("USER"), Auth: []ssh.AuthMethod{ssh.PublicKeys(signer)}, HostKeyCallback: ssh.InsecureIgnoreHostKey(), Timeout: 3 * time.Second})
		if err != nil {
			t.Fatal(err)
		}
		return client
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	first := dial()
	defer first.Close()
	session := &Session{client: first, ctx: ctx}
	output, err := runMTRScript(ctx, session, mtrEnvironmentCommand, 64<<10)
	if err != nil {
		t.Fatal(err)
	}
	env := parseMTREnvironment(output)
	plan := mtrPlanForEnvironment(env)
	if env.os != "Linux" || plan.PackageManager == "" || !plan.AlreadyInstalled {
		t.Fatalf("fixture environment: %+v; %s", plan, output)
	}
	t.Logf("Read-only remote plan: distro=%s manager=%s permissions=%s installed=%v", plan.Distro, plan.PackageManager, plan.Permission, plan.AlreadyInstalled)
	parent := t.TempDir()
	marker := filepath.Join(parent, "fake-completed")
	// Absolutely no package manager is executed. The fake operation demonstrates
	// detached completion after the real SSH transport has been disconnected.
	output, err = runMTRScript(ctx, session, mtrDetachedCommand("sleep 0.3; printf safe > "+quoteMTRShell(marker), filepath.Join(parent, "dengshell-mtr.XXXXXXXX")), 64<<10)
	if err != nil {
		t.Fatal(err)
	}
	dir, err := parseMTRLogDirectory(output)
	if err != nil {
		t.Fatal(err)
	}
	first.Close()
	second := dial()
	defer second.Close()
	session = &Session{client: second, ctx: ctx}
	for {
		status, _, err := readMTRInstallation(ctx, session, dir)
		if err != nil {
			t.Fatal(err)
		}
		if status == "0" {
			break
		}
		if ctx.Err() != nil {
			t.Fatal("detached fixture did not finish")
		}
		time.Sleep(30 * time.Millisecond)
	}
	content, err := os.ReadFile(marker)
	if err != nil || string(content) != "safe" {
		t.Fatalf("detached remote failed: %s %v", content, err)
	}
}
