package syncvault

import (
	"bytes"
	"context"
	"crypto/hmac"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
)

var validID = regexp.MustCompile(`^[a-f0-9]{64}$`)

const MaxDevices = 128
const MaxEntries = 50000

func ID() string            { return Hash(Random()) }
func ValidID(s string) bool { return validID.MatchString(s) }

// Each device writes immutable snapshots. Vector clocks preserve concurrent
// edits and deletions without assuming Drive offers compare-and-swap writes.
type Clock map[string]uint64
type Entry struct {
	Value json.RawMessage `json:"value"`
	Clock Clock           `json:"clock"`
}
type Snapshot struct {
	Version  int              `json:"version"`
	Vault    string           `json:"vault"`
	Device   string           `json:"device"`
	Sequence uint64           `json:"sequence"`
	Clock    Clock            `json:"clock"`
	Entries  map[string]Entry `json:"entries"`
}
type Object struct {
	ID       string `json:"id"`
	Device   string `json:"device"`
	Sequence uint64 `json:"sequence"`
	Hash     string `json:"hash"`
	Size     int64  `json:"size"`
	Created  int64  `json:"created"`
}
type Metadata struct {
	Version    int    `json:"version"`
	Vault      string `json:"vault"`
	Salt       []byte `json:"salt"`
	WrappedKey []byte `json:"wrappedKey"`
	Proof      string `json:"proof"`
	Secrets    bool   `json:"secrets"`
}
type Backend interface {
	Metadata(context.Context) (Metadata, error)
	Create(context.Context, Metadata) error
	List(context.Context) ([]Object, error)
	Read(context.Context, Object) ([]byte, error)
	Write(context.Context, Object, []byte) error
	Close()
}

func NewMetadata(password string, secrets bool) (Metadata, []byte, error) {
	return NewMetadataFor(ID(), password, secrets)
}
func NewMetadataFor(vault, password string, secrets bool) (Metadata, []byte, error) {
	m := Metadata{Version: Protocol, Vault: vault, Salt: Random(), Secrets: secrets}
	key, e := PassKey(password, m.Salt)
	if e != nil {
		return m, nil, e
	}
	defer clear(key)
	master := Random()
	m.WrappedKey, e = Seal(key, master, "master/"+m.Vault)
	m.Proof = m.proof(master)
	return m, master, e
}
func (m Metadata) proof(master []byte) string {
	m.Proof = ""
	b, _ := json.Marshal(m)
	return Encode(Derive(master, "metadata/"+string(b)))
}
func (m Metadata) Unlock(password, recovery string) ([]byte, error) {
	if m.Version != Protocol || !ValidID(m.Vault) || len(m.Salt) != 32 || len(m.WrappedKey) != 72 {
		return nil, errors.New("同步空间格式无效")
	}
	var master []byte
	var e error
	if recovery != "" {
		master, e = Decode(recovery)
	} else {
		var key []byte
		key, e = PassKey(password, m.Salt)
		if e == nil {
			master, e = Open(key, m.WrappedKey, "master/"+m.Vault)
		}
		clear(key)
	}
	if e != nil || len(master) != 32 || !hmac.Equal([]byte(m.Proof), []byte(m.proof(master))) {
		clear(master)
		return nil, errors.New("同步口令或恢复码不正确")
	}
	return master, nil
}
func Union(a, b Clock) Clock {
	c := Clock{}
	for k, v := range a {
		c[k] = v
	}
	for k, v := range b {
		c[k] = max(c[k], v)
	}
	return c
}
func Dominates(a, b Clock) bool {
	for k, v := range b {
		if a[k] < v {
			return false
		}
	}
	return true
}
func EqualJSON(a, b json.RawMessage) bool {
	var x, y any
	if len(a) == 0 {
		a = []byte("null")
	}
	if len(b) == 0 {
		b = []byte("null")
	}
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	aa, _ := json.Marshal(x)
	bb, _ := json.Marshal(y)
	return bytes.Equal(aa, bb)
}
func Clone(s Snapshot) Snapshot {
	b, _ := json.Marshal(s)
	var n Snapshot
	_ = json.Unmarshal(b, &n)
	if n.Entries == nil {
		n.Entries = map[string]Entry{}
	}
	if n.Clock == nil {
		n.Clock = Clock{}
	}
	return n
}
func Validate(s Snapshot) error {
	if s.Version != Protocol || !ValidID(s.Vault) || !ValidID(s.Device) || s.Sequence == 0 || s.Sequence > 1<<53 || s.Clock[s.Device] != s.Sequence || len(s.Clock) > MaxDevices || len(s.Entries) > MaxEntries {
		return errors.New("同步快照格式或容量无效")
	}
	for id, n := range s.Clock {
		if !ValidID(id) || n == 0 || n > 1<<53 {
			return errors.New("同步版本无效")
		}
	}
	for k, e := range s.Entries {
		if len(k) < 10 || len(k) > 160 || len(e.Clock) == 0 || !Dominates(s.Clock, e.Clock) || !json.Valid(e.Value) {
			return errors.New("同步记录格式无效")
		}
		for id, n := range e.Clock {
			if !ValidID(id) || n == 0 {
				return errors.New("同步记录版本无效")
			}
		}
	}
	return nil
}
func EncodeSnapshot(s Snapshot, master []byte) (Object, []byte, error) {
	if e := Validate(s); e != nil {
		return Object{}, nil, e
	}
	plain, e := json.Marshal(s)
	if e != nil {
		return Object{}, nil, e
	}
	b, e := Seal(Derive(master, "data"), plain, fmt.Sprintf("snapshot/%s/%s/%d", s.Vault, s.Device, s.Sequence))
	if e != nil {
		return Object{}, nil, e
	}
	o := Object{Device: s.Device, Sequence: s.Sequence, Hash: Hash(b), Size: int64(len(b))}
	o.ID = o.Hash
	return o, b, nil
}
func DecodeSnapshot(o Object, b, master []byte, vault string) (Snapshot, error) {
	var s Snapshot
	if !ValidID(o.Device) || o.Sequence == 0 || Hash(b) != o.Hash {
		return s, errors.New("同步快照校验失败")
	}
	p, e := Open(Derive(master, "data"), b, fmt.Sprintf("snapshot/%s/%s/%d", vault, o.Device, o.Sequence))
	if e != nil {
		return s, e
	}
	defer clear(p)
	if json.Unmarshal(p, &s) != nil || s.Vault != vault || s.Device != o.Device || s.Sequence != o.Sequence {
		return s, errors.New("同步快照身份不匹配")
	}
	return s, Validate(s)
}

// Merge never resolves different concurrent values by wall-clock time.
// A conflict leaves the original entries intact until the user chooses.
func Merge(a, b Snapshot) (Snapshot, []string) {
	r := Clone(a)
	r.Clock = Union(a.Clock, b.Clock)
	conflicts := []string{}
	for k, y := range b.Entries {
		x, ok := r.Entries[k]
		if !ok {
			r.Entries[k] = y
			continue
		}
		if EqualJSON(x.Value, y.Value) {
			x.Clock = Union(x.Clock, y.Clock)
			r.Entries[k] = x
			continue
		}
		xd, yd := Dominates(x.Clock, y.Clock), Dominates(y.Clock, x.Clock)
		switch {
		case xd && !yd:
		case yd && !xd:
			r.Entries[k] = y
		default:
			conflicts = append(conflicts, k)
		}
	}
	sort.Strings(conflicts)
	return r, conflicts
}
func Heads(objects []Object) ([]Object, error) {
	by := map[string]Object{}
	for _, o := range objects {
		if !ValidID(o.Device) || !ValidID(o.Hash) || o.Sequence == 0 || o.Sequence > 1<<53 || o.Size < 40 || o.Size > MaxBlob {
			return nil, errors.New("云端快照索引无效")
		}
		p, ok := by[o.Device]
		if ok && p.Sequence == o.Sequence && p.Hash != o.Hash {
			return nil, errors.New("检测到设备配置副本并发写入，请重新配对此设备")
		}
		if !ok || p.Sequence < o.Sequence {
			by[o.Device] = o
		}
	}
	if len(by) > MaxDevices {
		return nil, errors.New("同步设备数量超过限制")
	}
	out := []Object{}
	for _, o := range by {
		out = append(out, o)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Device < out[j].Device })
	return out, nil
}
