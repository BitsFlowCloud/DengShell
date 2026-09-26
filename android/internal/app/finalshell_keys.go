package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

type FinalShellKeyItem struct {
	File    string `json:"file"`
	Name    string `json:"name,omitempty"`
	Status  string `json:"status"`
	Message string `json:"message"`
}

type finalShellKeyCandidate struct {
	data       []byte
	key        ManagedKey
	references []string
	item       int
}

func parseFinalShellKey(data []byte, filename string) (finalShellKeyCandidate, error) {
	name := strings.TrimSuffix(path.Base(filename), path.Ext(filename))
	if name == "" {
		name = path.Base(filename)
	}
	references := []string{path.Base(filename), name}
	passphrase := ""
	trimmed := bytes.TrimSpace(bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf}))
	if len(trimmed) > 0 && trimmed[0] == '{' {
		// Only inline key material from the selected key folder is accepted.
		// Never follow a local path supplied by an export or use a connection's
		// account password as the private-key passphrase.
		var record map[string]json.RawMessage
		if json.Unmarshal(trimmed, &record) != nil {
			return finalShellKeyCandidate{}, errors.New("密钥 JSON 格式无效")
		}
		stringField := func(names ...string) string {
			for _, n := range names {
				var v string
				if json.Unmarshal(record[n], &v) == nil && v != "" {
					return v
				}
			}
			return ""
		}
		material := stringField("private_key", "privateKey", "key_data", "key")
		if material == "" {
			return finalShellKeyCandidate{}, errors.New("密钥 JSON 未包含 PEM/OpenSSH 私钥内容")
		}
		data = []byte(material)
		defer clear(data)
		if value := strings.TrimSpace(stringField("name")); value != "" {
			name = value
		}
		if id := strings.TrimSpace(stringField("id", "secret_key_id")); id != "" && len(id) <= 1024 {
			references = append(references, id)
		}
		passphrase = stringField("passphrase", "password")
	}
	if name == "" || len(name) > 100 {
		name = "FinalShell 私钥"
	}
	signer, err := ssh.ParsePrivateKey(data)
	var locked *ssh.PassphraseMissingError
	encrypted := errors.As(err, &locked)
	var public ssh.PublicKey
	if encrypted {
		public = locked.PublicKey
		if passphrase != "" {
			signer, err = ssh.ParsePrivateKeyWithPassphrase(data, []byte(passphrase))
			if err != nil {
				// Key-specific metadata may use FinalShell's password encoding.
				if decoded, e := decodeFinalShellPassword(passphrase); e == nil && decoded != "" {
					if candidate, e := ssh.ParsePrivateKeyWithPassphrase(data, []byte(decoded)); e == nil {
						signer, err, passphrase = candidate, nil, decoded
					}
				}
			}
			if err != nil {
				passphrase = ""
				signer = nil
			}
		}
	} else if err != nil {
		return finalShellKeyCandidate{}, errors.New("不是受支持的完整 PEM/OpenSSH 私钥")
	}
	if signer != nil {
		public = signer.PublicKey()
	}
	key := ManagedKey{Name: name, Encrypted: encrypted, CreatedAt: time.Now()}
	if public != nil {
		key.PublicKey = strings.TrimSpace(string(ssh.MarshalAuthorizedKey(public)))
		key.Fingerprint = ssh.FingerprintSHA256(public)
	}
	if encrypted {
		key.Passphrase = passphrase
	}
	return finalShellKeyCandidate{data: bytes.Clone(data), key: key, references: references}, nil
}

func readFinalShellKeys(ctx context.Context, root *os.Root, result *FinalShellImportResult, total *int) ([]finalShellKeyCandidate, error) {
	info, err := root.Lstat("key")
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		result.KeyItems = append(result.KeyItems, FinalShellKeyItem{File: "key", Status: "failed", Message: "key 必须是可读取的普通文件夹"})
		result.KeysFailed++
		return nil, nil
	}
	keys := []finalShellKeyCandidate{}
	entries := 0
	err = fs.WalkDir(root.FS(), "key", func(name string, entry fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		entries++
		if entries > 2048 || len(result.KeyItems) >= 256 {
			return errors.New("每批最多导入 256 个密钥文件，请分批导入")
		}
		if walkErr != nil {
			return errors.New("key 文件夹无法完整读取，请检查权限")
		}
		if entry.IsDir() {
			if strings.Count(name, "/") > 16 {
				return errors.New("密钥目录层级过深")
			}
			return nil
		}
		if strings.HasSuffix(strings.ToLower(entry.Name()), ".pub") {
			return nil
		}
		item := FinalShellKeyItem{File: name, Status: "failed"}
		fail := func(message string) {
			item.Message = message
			result.KeyItems = append(result.KeyItems, item)
			result.KeysFailed++
		}
		if !entry.Type().IsRegular() {
			fail("不读取符号链接或特殊文件")
			return nil
		}
		f, err := root.Open(name)
		if err != nil {
			fail("密钥文件无法读取")
			return nil
		}
		info, err := f.Stat()
		if err != nil || !info.Mode().IsRegular() || info.Size() > 2<<20 {
			f.Close()
			fail("私钥必须是普通文件且不超过 2 MiB")
			return nil
		}
		data, err := io.ReadAll(io.LimitReader(f, (2<<20)+1))
		f.Close()
		defer clear(data)
		if err != nil || len(data) > 2<<20 {
			fail("私钥无法读取或超过 2 MiB")
			return nil
		}
		*total += len(data)
		if *total > finalShellMaxTotal {
			return errors.New("连接与密钥导入数据超过 32 MiB，请分批导入")
		}
		candidate, err := parseFinalShellKey(data, name)
		if err != nil {
			fail(err.Error())
			return nil
		}
		item.Name = candidate.key.Name
		item.Status = "pending"
		candidate.item = len(result.KeyItems)
		result.KeyItems = append(result.KeyItems, item)
		keys = append(keys, candidate)
		return nil
	})
	if err != nil {
		for _, key := range keys {
			clear(key.data)
		}
		return nil, err
	}
	return keys, nil
}

// Called under the import's store lock; its caller owns config and file rollback.
func (s *Store) importFinalShellKeysLocked(keys []finalShellKeyCandidate, result *FinalShellImportResult, created *[]string) (map[string]string, error) {
	matched := map[string]string{}
	for _, candidate := range keys {
		key := candidate.key
		item := &result.KeyItems[candidate.item]
		for _, existing := range s.config.Keys {
			data, err := readPrivateKey(filepath.Join(s.dir, "keys", existing.ID))
			if err != nil {
				continue
			}
			same := sha256.Sum256(data) == sha256.Sum256(candidate.data)
			if !same && key.Fingerprint != "" {
				// Confirm against the actual file; do not trust stale metadata.
				if signer, e := ssh.ParsePrivateKey(data); e == nil {
					same = ssh.FingerprintSHA256(signer.PublicKey()) == key.Fingerprint
				}
			}
			clear(data)
			if same {
				key = existing
				break
			}
		}
		if key.ID == "" {
			key.ID = randomID()
			directory := filepath.Join(s.dir, "keys")
			if err := os.MkdirAll(directory, 0700); err != nil {
				return nil, err
			}
			filename := filepath.Join(directory, key.ID)
			f, err := os.OpenFile(filename, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
			if err != nil {
				return nil, err
			}
			*created = append(*created, filename)
			_, err = f.Write(candidate.data)
			if err == nil {
				err = f.Sync()
			}
			closed := f.Close()
			if err == nil {
				err = closed
			}
			if err != nil {
				return nil, err
			}
			s.config.Keys = append(s.config.Keys, key)
			result.KeysImported++
			item.Status = "imported"
			item.Message = "已导入密钥管理器"
		} else {
			result.KeysSkipped++
			item.Status = "skipped"
			item.Message = "复用密钥管理器中的同一私钥"
		}
		if key.Encrypted && key.Passphrase == "" {
			item.Message += "；连接时输入口令，或在密钥管理器中保存"
		}
		for _, ref := range candidate.references {
			if existing, ok := matched[ref]; ok && existing != key.ID {
				matched[ref] = ""
			} else if !ok {
				matched[ref] = key.ID
			}
		}
	}
	return matched, nil
}
