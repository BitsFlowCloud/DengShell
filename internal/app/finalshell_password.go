package app

import (
	"crypto/des"
	"crypto/md5"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"strings"
	"unicode/utf8"
)

// Legacy FinalShell export compatibility only. Imported credentials are saved
// using DengShell's AES-GCM store, never DES. No external decoder is executed.
// Format reference: https://github.com/sncce/finalshell-decoder-use-python
type finalShellRandom uint64

func newFinalShellRandom(seed int64) finalShellRandom {
	return finalShellRandom((uint64(seed) ^ 0x5deece66d) & ((1 << 48) - 1))
}
func (r *finalShellRandom) bits(n uint) uint32 {
	*r = finalShellRandom((uint64(*r)*0x5deece66d + 11) & ((1 << 48) - 1))
	return uint32(uint64(*r) >> (48 - n))
}
func (r *finalShellRandom) long() int64 {
	hi, lo := int32(r.bits(32)), int32(r.bits(32))
	return int64(hi)<<32 + int64(lo)
}
func (r *finalShellRandom) int127() int64 {
	for {
		u := r.bits(31)
		v := u % 127
		if int32(u-v+126) >= 0 {
			return int64(v)
		}
	}
}
func finalShellDESKey(head []byte) ([8]byte, error) {
	var key [8]byte
	if len(head) != 8 {
		return key, errors.New("密码头部无效")
	}
	r := newFinalShellRandom(int64(int8(head[5])))
	divisor := r.int127()
	if divisor == 0 {
		return key, errors.New("密码头部无效")
	}
	r = newFinalShellRandom(3680984568597093857 / divisor)
	for i := 0; i < int(int8(head[0])); i++ {
		r.long()
	}
	r2 := newFinalShellRandom(r.long())
	values := [8]int64{int64(int8(head[4])), r2.long(), int64(int8(head[7])), int64(int8(head[3])), r2.long(), int64(int8(head[1])), r.long(), int64(int8(head[2]))}
	var material [64]byte
	for i, value := range values {
		binary.BigEndian.PutUint64(material[i*8:], uint64(value))
	}
	digest := md5.Sum(material[:])
	copy(key[:], digest[:8])
	clear(material[:])
	clear(digest[:])
	return key, nil
}
func decodeFinalShellPassword(encoded string) (string, error) {
	invalid := errors.New("保存的密码无法解析，请在连接设置中重新填写密码")
	encoded = strings.TrimSpace(encoded)
	if encoded == "" {
		return "", nil
	}
	if len(encoded) > 65536 {
		return "", invalid
	}
	data, err := base64.StdEncoding.Strict().DecodeString(encoded)
	if err != nil || len(data) < 16 || len(data)%8 != 0 {
		return "", invalid
	}
	key, err := finalShellDESKey(data[:8])
	if err != nil {
		return "", invalid
	}
	block, err := des.NewCipher(key[:])
	clear(key[:])
	if err != nil {
		return "", invalid
	}
	plain := make([]byte, len(data)-8)
	defer clear(plain)
	for i := 0; i < len(plain); i += 8 {
		block.Decrypt(plain[i:i+8], data[i+8:i+16])
	}
	padding := int(plain[len(plain)-1])
	if padding < 1 || padding > 8 {
		return "", invalid
	}
	for _, b := range plain[len(plain)-padding:] {
		if int(b) != padding {
			return "", invalid
		}
	}
	plain = plain[:len(plain)-padding]
	if !utf8.Valid(plain) {
		return "", invalid
	}
	return string(plain), nil
}
