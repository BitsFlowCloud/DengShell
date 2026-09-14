package app

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

type ManagedKey struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	PublicKey     string    `json:"publicKey"`
	Fingerprint   string    `json:"fingerprint"`
	Encrypted     bool      `json:"encrypted"`
	Passphrase    string    `json:"passphrase,omitempty"` // Only persisted inside the encrypted configuration.
	HasPassphrase bool      `json:"hasPassphrase,omitempty"`
	CreatedAt     time.Time `json:"createdAt"`
}

func (key ManagedKey) public() ManagedKey {
	key.HasPassphrase = key.Passphrase != ""
	key.Passphrase = ""
	return key
}

type KeyInput struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Generate   bool   `json:"generate"`
	SourcePath string `json:"sourcePath"`
	PrivateKey string `json:"privateKey"`
	Passphrase string `json:"passphrase"`
}

func readPrivateKey(path string) ([]byte, error) {
	f, err := os.Open(expandHome(path))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, (2<<20)+1))
	if err == nil && len(data) > 2<<20 {
		err = errors.New("私钥文件过大")
	}
	return data, err
}

var errManagedKeyMissing = errors.New("密钥不存在，请重新配置密钥")

func (s *Store) KeyData(id string) ([]byte, error) {
	data, _, err := s.keyMaterial(id)
	return data, err
}

// Read the private file and its saved passphrase from the same store snapshot.
func (s *Store) keyMaterial(id string) ([]byte, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, key := range s.config.Keys {
		if key.ID == id {
			data, err := readPrivateKey(filepath.Join(s.dir, "keys", key.ID))
			return data, key.Passphrase, err
		}
	}
	return nil, "", errManagedKeyMissing
}

func (s *Store) SaveKey(input KeyInput) (ManagedKey, error) {
	input.Name = strings.TrimSpace(input.Name)
	if input.Name == "" || len(input.Name) > 100 {
		return ManagedKey{}, errors.New("请填写密钥名称（最多 100 字节）")
	}
	if input.ID != "" {
		s.mu.Lock()
		defer s.mu.Unlock()
		for i, key := range s.config.Keys {
			if key.ID != input.ID {
				continue
			}
			updated := key
			updated.Name = input.Name
			if input.Passphrase != "" {
				if !key.Encrypted {
					return ManagedKey{}, errors.New("此私钥没有加密，无需保存口令")
				}
				data, err := readPrivateKey(filepath.Join(s.dir, "keys", key.ID))
				if err != nil {
					return ManagedKey{}, fmt.Errorf("读取私钥：%w", err)
				}
				signer, err := ssh.ParsePrivateKeyWithPassphrase(data, []byte(input.Passphrase))
				clear(data)
				if err != nil {
					return ManagedKey{}, fmt.Errorf("私钥格式或口令不正确：%w", err)
				}
				updated.Passphrase = input.Passphrase
				updated.PublicKey = strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey())))
				updated.Fingerprint = ssh.FingerprintSHA256(signer.PublicKey())
			}
			s.config.Keys[i] = updated
			if err := s.writeLocked(); err != nil {
				s.config.Keys[i] = key
				return ManagedKey{}, err
			}
			return updated.public(), nil
		}
		return ManagedKey{}, errors.New("密钥不存在")
	}
	var data []byte
	var signer ssh.Signer
	var err error
	encrypted := false
	if input.Generate {
		_, private, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return ManagedKey{}, err
		}
		signer, err = ssh.NewSignerFromKey(private)
		if err != nil {
			return ManagedKey{}, err
		}
		var block *pem.Block
		if input.Passphrase == "" {
			block, err = ssh.MarshalPrivateKey(private, "CloudShell")
		} else {
			block, err = ssh.MarshalPrivateKeyWithPassphrase(private, "CloudShell", []byte(input.Passphrase))
			encrypted = true
		}
		if err != nil {
			return ManagedKey{}, err
		}
		data = pem.EncodeToMemory(block)
	} else {
		if input.SourcePath != "" {
			data, err = readPrivateKey(input.SourcePath)
		} else {
			data = []byte(input.PrivateKey)
		}
		if err != nil {
			return ManagedKey{}, fmt.Errorf("读取私钥：%w", err)
		}
		if len(data) == 0 || len(data) > 2<<20 {
			return ManagedKey{}, errors.New("请选择私钥文件或粘贴私钥内容")
		}
		signer, err = ssh.ParsePrivateKey(data)
		var missing *ssh.PassphraseMissingError
		if errors.As(err, &missing) {
			encrypted = true
			if input.Passphrase == "" {
				return ManagedKey{}, errors.New("此私钥已加密，请输入私钥口令")
			}
			signer, err = ssh.ParsePrivateKeyWithPassphrase(data, []byte(input.Passphrase))
		}
		if err != nil {
			return ManagedKey{}, fmt.Errorf("私钥格式或口令不正确：%w", err)
		}
	}
	key := ManagedKey{ID: randomID(), Name: input.Name, PublicKey: strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey()))), Fingerprint: ssh.FingerprintSHA256(signer.PublicKey()), Encrypted: encrypted, CreatedAt: time.Now()}
	if encrypted {
		key.Passphrase = input.Passphrase
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	dir := filepath.Join(s.dir, "keys")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return ManagedKey{}, err
	}
	path := filepath.Join(dir, key.ID)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return ManagedKey{}, err
	}
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		os.Remove(path)
		return ManagedKey{}, err
	}
	old := s.config.Keys
	s.config.Keys = append(append([]ManagedKey{}, old...), key)
	if err := s.writeLocked(); err != nil {
		s.config.Keys = old
		os.Remove(path)
		return ManagedKey{}, err
	}
	return key.public(), nil
}

func (s *Store) DeleteKey(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, profile := range s.config.Servers {
		if profile.Auth == "key" && profile.KeyID == id {
			return fmt.Errorf("密钥仍被服务器「%s」使用，请先更换其认证方式或密钥", profile.Name)
		}
	}
	next := []ManagedKey{}
	found := false
	for _, key := range s.config.Keys {
		if key.ID == id {
			found = true
		} else {
			next = append(next, key)
		}
	}
	if !found {
		return errors.New("密钥不存在")
	}
	old := s.config.Keys
	s.config.Keys = next
	if err := s.writeLocked(); err != nil {
		s.config.Keys = old
		return err
	}
	if err := os.Remove(filepath.Join(s.dir, "keys", id)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("密钥记录已删除，但清理本机副本失败：%w", err)
	}
	return nil
}

func (a *App) saveKeyHTTP(w http.ResponseWriter, r *http.Request) {
	var input KeyInput
	if !decode(w, r, &input) {
		return
	}
	key, err := a.store.SaveKey(input)
	respond(w, key, err)
}
