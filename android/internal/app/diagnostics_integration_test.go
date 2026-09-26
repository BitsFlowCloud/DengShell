//go:build !windows

package app

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

func TestDiagnosticMTRIntegration(t *testing.T) {
	if os.Getenv("DENGSHELL_TEST_MTR") != "1" {
		t.Skip("set DENGSHELL_TEST_MTR=1 for real local ICMP mtr")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	d := &Diagnostic{}
	if err := runLocalDiagnostic(ctx, "127.0.0.1", d); err != nil {
		t.Fatalf("mtr: %v\n%s", err, d.snapshot().Output)
	}
	if out := d.snapshot().Output; !strings.Contains(out, "127.0.0.1") || !strings.Contains(out, "Loss%") {
		t.Fatal(out)
	}
	cancelCtx, stop := context.WithCancel(context.Background())
	time.AfterFunc(200*time.Millisecond, stop)
	started := time.Now()
	if err := runLocalDiagnostic(cancelCtx, "127.0.0.1", &Diagnostic{}); err == nil {
		t.Fatal("cancelled mtr succeeded")
	}
	if time.Since(started) > 3*time.Second {
		t.Fatal("mtr process cancellation was not bounded")
	}
}
func TestDiagnosticRemoteMTRIntegration(t *testing.T) {
	key := os.Getenv("CLOUDSHELL_TEST_KEY")
	if key == "" || os.Getenv("DENGSHELL_TEST_MTR") != "1" {
		t.Skip("requires dedicated SSH fixture on127.0.0.1:19225")
	}
	data, err := os.ReadFile(key)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.ParsePrivateKey(data)
	if err != nil {
		t.Fatal(err)
	}
	client, err := ssh.Dial("tcp", "127.0.0.1:19225", &ssh.ClientConfig{User: os.Getenv("USER"), Auth: []ssh.AuthMethod{ssh.PublicKeys(signer)}, HostKeyCallback: ssh.InsecureIgnoreHostKey(), Timeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	session := &Session{client: client, ctx: ctx}
	d := &Diagnostic{}
	if err := runRemoteDiagnostic(ctx, session, "127.0.0.1", d); err != nil {
		t.Fatalf("remote mtr: %v\n%s", err, d.snapshot().Output)
	}
	if !strings.Contains(d.snapshot().Output, "Loss%") {
		t.Fatal(d.snapshot().Output)
	}
	cancelCtx, stop := context.WithCancel(ctx)
	time.AfterFunc(300*time.Millisecond, stop)
	started := time.Now()
	if err := runRemoteDiagnostic(cancelCtx, session, "127.0.0.1", &Diagnostic{}); err == nil {
		t.Fatal("remote cancellation succeeded")
	}
	if time.Since(started) > 3*time.Second {
		t.Fatal("remote channel did not cancel")
	}
	probe, err := client.NewSession()
	if err != nil {
		t.Fatal("diagnostic closed shared SSH transport", err)
	}
	defer probe.Close()
	if out, err := probe.Output("printf SSH_STILL_ALIVE"); err != nil || string(out) != "SSH_STILL_ALIVE" {
		t.Fatalf("shared SSH broken: %s %v", out, err)
	}
}
