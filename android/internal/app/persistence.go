package app

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

const (
	EncryptedConfigName = "dengshell.config.enc"
	ConfigKeyName       = "dengshell.config.key"
	configEnvelopeAAD   = "DengShell/config/v1/AES-256-GCM"
	maxConfigBytes      = 24 << 20
)

// The key travels with the portable folder for automatic unlock. This detects
// ciphertext edits; it is not a boundary against a process that can read the key.
type configEnvelope struct {
	Format    string `json:"format"`
	Version   int    `json:"version"`
	Algorithm string `json:"algorithm"`
	Payload   []byte `json:"payload"` // Go's AEAD prepends its random 96-bit nonce.
}

func configCipher(key []byte) (cipher.AEAD, error) {
	if len(key) != 32 {
		return nil, errors.New("配置解锁密钥长度无效，请恢复同一备份中的配置和密钥文件")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCMWithRandomNonce(block)
}

func encryptConfig(key, plain []byte) ([]byte, error) {
	aead, err := configCipher(key)
	if err != nil {
		return nil, err
	}
	return json.MarshalIndent(configEnvelope{Format: "DengShell", Version: 1, Algorithm: "AES-256-GCM", Payload: aead.Seal(nil, nil, plain, []byte(configEnvelopeAAD))}, "", "  ")
}

func decryptConfig(key, data []byte) ([]byte, error) {
	var envelope configEnvelope
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&envelope); err != nil {
		return nil, fmt.Errorf("加密配置格式无效，原文件未修改：%w", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, errors.New("加密配置包含多余内容，原文件未修改")
	}
	if envelope.Format != "DengShell" || envelope.Version != 1 || envelope.Algorithm != "AES-256-GCM" {
		return nil, errors.New("加密配置版本或算法不受支持，请使用匹配版本；原文件未修改")
	}
	aead, err := configCipher(key)
	if err != nil {
		return nil, err
	}
	plain, err := aead.Open(nil, nil, envelope.Payload, []byte(configEnvelopeAAD))
	if err != nil {
		return nil, errors.New("配置完整性验证失败：文件被修改、损坏或解锁密钥不匹配；已拒绝覆盖，请恢复同一备份中的配置和密钥")
	}
	return plain, nil
}

func readConfigFile(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > limit {
		return nil, errors.New("配置文件类型或大小无效，原文件未修改")
	}
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if len(data) > int(limit) {
		return nil, errors.New("配置文件超过大小限制")
	}
	return data, err
}

func OpenStore(dir string) (*Store, error) {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("程序配置目录不可写，请把完整程序文件夹移动到可写位置：%w", err)
	}
	unlock, err := lockConfigDirectory(dir)
	if err != nil {
		return nil, err
	}
	defer unlock()
	s := &Store{dir: dir, config: Config{Servers: []Profile{}, Groups: []string{"我的服务器"}, HostKeys: map[string]string{}, Appearance: defaultAppearance()}}
	ciphertext, err := readConfigFile(filepath.Join(dir, EncryptedConfigName), maxConfigBytes)
	var plain []byte
	legacy := false
	if err == nil {
		s.hasDisk = true
		s.diskDigest = sha256.Sum256(ciphertext)
		s.key, err = readConfigFile(filepath.Join(dir, ConfigKeyName), 32)
		if err != nil {
			return nil, fmt.Errorf("无法读取配置解锁密钥，不会创建新密钥或覆盖原配置：%w", err)
		}
		plain, err = decryptConfig(s.key, ciphertext)
		if err != nil {
			return nil, err
		}
	} else if os.IsNotExist(err) {
		// Do not silently replace a deleted primary file when its backup remains.
		if _, backupErr := os.Lstat(filepath.Join(dir, EncryptedConfigName+".bak")); !os.IsNotExist(backupErr) {
			return nil, errors.New("主配置缺失，但检测到加密备份；请先将 dengshell.config.enc.bak 恢复为 dengshell.config.enc，原文件未修改")
		}
		plain, err = readConfigFile(filepath.Join(dir, "config.json"), maxConfigBytes)
		if err == nil {
			legacy = true
		} else if !os.IsNotExist(err) {
			return nil, err
		}
	} else {
		return nil, err
	}
	if len(plain) > 0 || legacy || s.hasDisk {
		if err = json.Unmarshal(plain, &s.config); err != nil {
			return nil, fmt.Errorf("配置内容无法解析，已保留原文件：%w", err)
		}
	}
	previousVersion := s.config.SchemaVersion
	normalizeConfigCollections(&s.config)
	commandGroupsChanged := normalizeCommandGroups(&s.config)
	commandHistoryChanged := migrateGlobalCommandHistory(&s.config)
	if err := s.upgradeConfig(); err != nil {
		return nil, err
	}
	if !s.hasDisk {
		s.key, err = readConfigFile(filepath.Join(dir, ConfigKeyName), 32)
		if os.IsNotExist(err) {
			s.key = make([]byte, 32)
			if _, err = rand.Read(s.key); err != nil {
				return nil, err
			}
			err = atomicConfigFile(filepath.Join(dir, ConfigKeyName), s.key)
		}
		if err != nil {
			return nil, fmt.Errorf("无法创建或读取便携配置密钥，请使用可写目录：%w", err)
		}
		if _, err = configCipher(s.key); err != nil {
			return nil, err
		}
	}
	if legacy {
		// Preserve the exact legacy bytes inside an authenticated envelope. Even
		// migration backups and their temporary files must never expose plaintext.
		prefix := "config.pre-encryption"
		if previousVersion < ConfigSchemaVersion {
			prefix = fmt.Sprintf("config.pre-v%d", ConfigSchemaVersion)
		}
		backup := filepath.Join(dir, fmt.Sprintf("%s.%s.%s.enc", prefix, time.Now().UTC().Format("20060102T150405Z"), randomID()[:8]))
		backupData, err := encryptConfig(s.key, plain)
		if err != nil {
			return nil, fmt.Errorf("无法加密旧配置备份，已取消迁移：%w", err)
		}
		if err := atomicConfigFile(backup, backupData); err != nil {
			return nil, fmt.Errorf("无法保存旧配置备份，已取消迁移：%w", err)
		}
	}
	if !s.hasDisk || previousVersion != ConfigSchemaVersion || commandGroupsChanged || commandHistoryChanged {
		if err := s.writeWithFileLock(); err != nil {
			return nil, err
		}
	}
	if legacy {
		if err := os.Remove(filepath.Join(dir, "config.json")); err != nil {
			return nil, fmt.Errorf("配置已加密，但无法移除旧 config.json（旧文件备份已保留）：%w", err)
		}
	}
	return s, nil
}

func normalizeConfigCollections(c *Config) {
	if c.Servers == nil {
		c.Servers = []Profile{}
	}
	if c.Groups == nil {
		c.Groups = []string{"我的服务器"}
	}
	if c.HostKeys == nil {
		c.HostKeys = map[string]string{}
	}
	if c.Commands == nil {
		c.Commands = []QuickCommand{}
	}
	if c.Keys == nil {
		c.Keys = []ManagedKey{}
	}
	if c.Proxies == nil {
		c.Proxies = []ManagedProxy{}
	}
	if c.Assets == nil {
		c.Assets = []ManagedAsset{}
	}
}

func (s *Store) writeLocked() error {
	unlock, err := lockConfigDirectory(s.dir)
	if err != nil {
		return err
	}
	defer unlock()
	return s.writeWithFileLock()
}

// Caller holds the OS lock (and the Store mutex after initialization). Compare
// the exact loaded bytes to avoid overwriting corruption or another instance.
func (s *Store) writeWithFileLock() error {
	key, err := readConfigFile(filepath.Join(s.dir, ConfigKeyName), 32)
	if err != nil || !bytes.Equal(key, s.key) {
		return errors.New("配置解锁密钥已改变或丢失，已拒绝保存；请恢复原文件后重新启动")
	}
	path := filepath.Join(s.dir, EncryptedConfigName)
	previous, err := readConfigFile(path, maxConfigBytes)
	if s.hasDisk {
		if err != nil || sha256.Sum256(previous) != s.diskDigest {
			return errors.New("磁盘配置已被其他实例修改、损坏或删除，已拒绝覆盖；请检查原文件并重新启动")
		}
	} else if !os.IsNotExist(err) {
		return errors.New("目标配置已存在或不可读取，已拒绝覆盖")
	}
	plain, err := json.Marshal(s.config)
	if err != nil {
		return err
	}
	data, err := encryptConfig(s.key, plain)
	if err != nil {
		return err
	}
	if len(data) > maxConfigBytes {
		return errors.New("配置内容过大，已取消保存")
	}
	if s.hasDisk {
		if err := atomicConfigFile(path+".bak", previous); err != nil {
			return fmt.Errorf("无法保存上一次配置的加密备份，已取消保存：%w", err)
		}
	}
	if err := atomicConfigFile(path, data); err != nil {
		return err
	}
	s.hasDisk = true
	s.diskDigest = sha256.Sum256(data)
	return nil
}

func atomicConfigFile(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".dengshell-write-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err = replaceConfigFile(f.Name(), path); err != nil {
		return err
	}
	// A failed directory fsync after rename cannot be rolled back safely. The
	// file itself is already synced; sync the directory where supported.
	if dir, err := os.Open(filepath.Dir(path)); err == nil {
		_ = dir.Sync()
		_ = dir.Close()
	}
	return nil
}
