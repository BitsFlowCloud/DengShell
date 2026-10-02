package app

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"net"
	"testing"

	"github.com/pkg/sftp"
)

// Model a v3-only server: ordinary RENAME works, OpenSSH extensions do not.
type sftpV3Conn struct{ net.Conn }

func (c sftpV3Conn) Write(p []byte) (int, error) {
	if len(p) >= 9 && p[4] == 2 { // SSH_FXP_VERSION
		version := bytes.Clone(p[:9])
		binary.BigEndian.PutUint32(version, 5)
		if _, err := c.Conn.Write(version); err != nil {
			return 0, err
		}
		return len(p), nil
	}
	return c.Conn.Write(p)
}

type sftpV3Commands struct{ sftp.FileCmder }

func (c sftpV3Commands) Filecmd(r *sftp.Request) error {
	// The library's in-memory fixture cannot chmod directories. Permissions are
	// exercised by the separate real-filesystem upload tests.
	if r.Method == "Setstat" && !r.AttrFlags().Size {
		return nil
	}
	return c.FileCmder.Filecmd(r)
}

func (sftpV3Commands) PosixRename(*sftp.Request) error { return sftp.ErrSSHFxOpUnsupported }

func TestUploadNewFileWithOverwriteAllowedOnSFTPV3Server(t *testing.T) {
	cc, sc := net.Pipe()
	handlers := sftp.InMemHandler()
	handlers.FileCmd = sftpV3Commands{handlers.FileCmd}
	server := sftp.NewRequestServer(sftpV3Conn{sc}, handlers)
	go server.Serve()
	client, err := sftp.NewClientPipe(cc, cc)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { client.Close(); server.Close(); sc.Close() })
	if _, ok := client.HasExtension("posix-rename@openssh.com"); ok {
		t.Fatal("fixture unexpectedly advertises POSIX rename")
	}
	s := &Session{ID: "v3-upload", ctx: context.Background(), files: client}
	a := &App{transfers: make(map[string]*Transfer)}
	data := []byte("new file in overwrite-approved batch\r\n中文")
	ctx, transfer, err := a.beginTransfer(context.Background(), s, "", "/uploads/new.txt", int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	defer transfer.cancel()
	if err := a.copyUpload(ctx, s, transfer, bytes.NewReader(data), true); err != nil {
		t.Fatalf("new file requires no overwrite extension: %v", err)
	}
	f, err := client.Open(transfer.Target)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	got, err := io.ReadAll(f)
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("upload content changed: %q %v", got, err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	ctx, replacement, err := a.beginTransfer(context.Background(), s, "", transfer.Target, 3)
	if err != nil {
		t.Fatal(err)
	}
	defer replacement.cancel()
	if err := a.copyUpload(ctx, s, replacement, bytes.NewReader([]byte("new")), true); err == nil {
		t.Fatal("server without atomic replacement unexpectedly overwrote existing file")
	}
	f, err = client.Open(transfer.Target)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	got, err = io.ReadAll(f)
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("unsupported overwrite damaged the original: %q %v", got, err)
	}
}
