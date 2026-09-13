package app

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"net"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"golang.org/x/crypto/ssh"
)

func trustFixtureHostKey(t *testing.T, a *App, address string, key ssh.PublicKey) {
	t.Helper()
	if err := a.store.hostKeyWithApproval(address, key, &HostKeyApproval{Host: address, Fingerprint: ssh.FingerprintSHA256(key)}); err != nil {
		t.Fatal(err)
	}
}

func connectLocalSSHFixture(t *testing.T, a *App, profileID, secret string) (*Session, error) {
	t.Helper()
	fingerprint := os.Getenv("CLOUDSHELL_TEST_HOST_FINGERPRINT")
	if fingerprint == "" {
		t.Skip("set CLOUDSHELL_TEST_HOST_FINGERPRINT from the dedicated fixture host public key")
	}
	if !strings.HasPrefix(fingerprint, "SHA256:") {
		t.Fatal("fixture host fingerprint must use SHA256 format")
	}
	profile, err := a.store.Get(profileID)
	if err != nil {
		return nil, err
	}
	if profile.Host != "127.0.0.1" || profile.Port != 19225 {
		t.Fatal("dedicated localhost:19225 fixture required")
	}
	return a.ConnectWithHostKeyApproval(context.Background(), profileID, secret, &HostKeyApproval{Host: net.JoinHostPort(profile.Host, strconv.Itoa(profile.Port)), Fingerprint: fingerprint})
}

func TestHostKeyApprovalPrecedesCredentialsAndPersists(t *testing.T) {
	for _, changed := range []bool{false, true} {
		t.Run(map[bool]string{false: "first connection", true: "changed key"}[changed], func(t *testing.T) {
			var sent atomic.Int32
			a, host, port := authenticationFixture(t, &ssh.ServerConfig{PasswordCallback: func(ssh.ConnMetadata, []byte) (*ssh.Permissions, error) { sent.Add(1); return nil, nil }})
			address := net.JoinHostPort(host, strconv.Itoa(port))
			presented := a.store.config.HostKeys[address]
			prior := ""
			if changed {
				_, key, _ := ed25519.GenerateKey(rand.Reader)
				signer, _ := ssh.NewSignerFromKey(key)
				prior = ssh.FingerprintSHA256(signer.PublicKey())
				a.store.config.HostKeys[address] = prior
			} else {
				delete(a.store.config.HostKeys, address)
			}
			if err := a.store.writeLocked(); err != nil {
				t.Fatal(err)
			}
			p, err := a.store.Save(Profile{Name: "isolated host key", Host: host, Port: port, User: "tester", Auth: "password", Secret: "synthetic-password"}, false)
			if err != nil {
				t.Fatal(err)
			}
			_, err = a.Connect(context.Background(), p.ID, "")
			var keyError *HostKeyError
			if !errors.As(err, &keyError) || keyError.Fingerprint != presented || keyError.PreviousFingerprint != prior {
				t.Fatalf("missing fingerprint challenge: %v", err)
			}
			if sent.Load() != 0 || a.store.config.HostKeys[address] != prior {
				t.Fatal("unapproved handshake sent credentials or changed trust")
			}
			for _, approval := range []*HostKeyApproval{
				{Host: "other.invalid:22", Fingerprint: presented, PreviousFingerprint: prior},
				{Host: address, Fingerprint: "wrong-key", PreviousFingerprint: prior},
				{Host: address, Fingerprint: presented, PreviousFingerprint: "stale-approval"},
			} {
				if _, err := a.ConnectWithHostKeyApproval(context.Background(), p.ID, "", approval); !errors.As(err, &keyError) {
					t.Fatalf("unscoped approval accepted: %v", err)
				}
			}
			if sent.Load() != 0 {
				t.Fatal("rejected approvals sent credentials")
			}
			approval := &HostKeyApproval{Host: address, Fingerprint: presented, PreviousFingerprint: prior}
			session, err := a.ConnectWithHostKeyApproval(context.Background(), p.ID, "", approval)
			if err != nil {
				t.Fatal(err)
			}
			a.disconnect(session.ID)
			if sent.Load() != 1 || a.store.config.HostKeys[address] != presented {
				t.Fatal("confirmed key did not authenticate/persist")
			}
			reopened, err := OpenStore(a.store.dir)
			if err != nil || reopened.config.HostKeys[address] != presented {
				t.Fatal("confirmed fingerprint lost on reopen", err)
			}
			session, err = a.Connect(context.Background(), p.ID, "")
			if err != nil {
				t.Fatal("known key unexpectedly required new confirmation", err)
			}
			a.disconnect(session.ID)
		})
	}
}
