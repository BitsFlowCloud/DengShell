// Package syncvault contains the versioned, client-side encrypted sync format.
// No encryption key is sent to the storage server.
package syncvault

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/chacha20poly1305"
)

const MaxBlob = 16 << 20
const MaxLocalPlain = 64 << 20
const MaxLocalFile = 90 << 20
const Protocol = 1

func Random() []byte {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return b
}
func Encode(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }
func Decode(s string) ([]byte, error) {
	b, e := base64.RawURLEncoding.DecodeString(s)
	if e != nil || len(b) != 32 {
		return nil, errors.New("无效的密钥或连接资料")
	}
	return b, nil
}
func Hash(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

// Derive uses independent, fixed domain labels for recovery, pairing and data.
func Derive(key []byte, purpose string) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte("DengShell/sync/v1/" + purpose))
	return h.Sum(nil)
}
func Seal(key, plain []byte, context string) ([]byte, error) {
	return sealLimit(key, plain, context, MaxBlob)
}
func sealLimit(key, plain []byte, context string, limit int) ([]byte, error) {
	if len(plain) > limit-64 {
		return nil, errors.New("同步数据超过容量限制")
	}
	a, e := chacha20poly1305.NewX(key)
	if e != nil {
		return nil, e
	}
	nonce := make([]byte, a.NonceSize())
	if _, e = rand.Read(nonce); e != nil {
		return nil, e
	}
	return a.Seal(nonce, nonce, plain, []byte("DengShell/sync/v1/"+context)), nil
}
func Open(key, data []byte, context string) ([]byte, error) {
	return openLimit(key, data, context, MaxBlob)
}
func openLimit(key, data []byte, context string, limit int) ([]byte, error) {
	a, e := chacha20poly1305.NewX(key)
	if e != nil {
		return nil, e
	}
	if len(data) < a.NonceSize()+a.Overhead() || len(data) > limit {
		return nil, errors.New("同步密文大小无效")
	}
	b, e := a.Open(nil, data[:a.NonceSize()], data[a.NonceSize():], []byte("DengShell/sync/v1/"+context))
	if e != nil {
		return nil, errors.New("同步数据完整性验证失败，已停止同步")
	}
	return b, nil
}

// A fixed KDF profile prevents an untrusted file from requesting excessive work.
// Credentials are never protected by the colocated portable configuration key.
type LocalEnvelope struct {
	Version int    `json:"version"`
	Salt    []byte `json:"salt"`
	Cipher  []byte `json:"cipher"`
}

func PassKey(password string, salt []byte) ([]byte, error) {
	if utf8.RuneCountInString(password) < 12 || len(password) > 1024 || len(salt) != 32 {
		return nil, errors.New("请使用至少 12 个字符的独立同步口令（建议随机生成），不要使用安全锁的短密码")
	}
	return argon2.IDKey([]byte(password), salt, 3, 64*1024, 4, 32), nil
}
func Protect(key, salt, plain []byte) ([]byte, error) {
	cipher, e := sealLimit(key, plain, "local", MaxLocalPlain)
	if e != nil {
		return nil, e
	}
	return json.Marshal(LocalEnvelope{Protocol, salt, cipher})
}
func Unprotect(password string, data []byte) ([]byte, []byte, []byte, error) {
	var f LocalEnvelope
	if len(data) > MaxLocalFile || json.Unmarshal(data, &f) != nil || f.Version != Protocol {
		return nil, nil, nil, errors.New("同步配置格式不受支持")
	}
	key, e := PassKey(password, f.Salt)
	if e != nil {
		return nil, nil, nil, e
	}
	plain, e := openLimit(key, f.Cipher, "local", MaxLocalPlain)
	if e != nil {
		clear(key)
		return nil, nil, nil, errors.New("同步口令不正确，或同步配置已损坏")
	}
	return key, f.Salt, plain, nil
}

type Connection struct {
	Version   int    `json:"version"`
	URL       string `json:"url"`
	CA        string `json:"ca"`
	Vault     string `json:"vault"`
	Bootstrap string `json:"bootstrap,omitempty"`
}

func Pack(v any) (string, error) {
	b, e := json.Marshal(v)
	if e != nil {
		return "", e
	}
	return "dengsync1." + Encode(b), nil
}
func Unpack(text string, v any) error {
	const prefix = "dengsync1."
	if len(text) > 16000 || len(text) < len(prefix) || text[:len(prefix)] != prefix {
		return errors.New("请粘贴完整的 DengShell 同步连接资料")
	}
	b, e := base64.RawURLEncoding.DecodeString(text[len(prefix):])
	if e != nil {
		return e
	}
	if e = json.Unmarshal(b, v); e != nil {
		return fmt.Errorf("同步连接资料无效")
	}
	return nil
}

type Device struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Owner   bool   `json:"owner"`
	Created int64  `json:"created"`
}
