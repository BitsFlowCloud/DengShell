package syncvault

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestVaultEncryptionAndMetadataAuthentication(t *testing.T) {
	m, master, err := NewMetadata("test-sync-password-123", true)
	if err != nil {
		t.Fatal(err)
	}
	for _, recovery := range []string{"", Encode(master)} {
		k, e := m.Unlock("test-sync-password-123", recovery)
		if e != nil || !bytes.Equal(k, master) {
			t.Fatal(e)
		}
	}
	if _, e := m.Unlock("wrong-password-123", ""); e == nil {
		t.Fatal("wrong password accepted")
	}
	altered := m
	altered.Secrets = false
	if _, e := altered.Unlock("", Encode(master)); e == nil {
		t.Fatal("metadata policy substitution accepted")
	}
	salt := Random()
	key, _ := PassKey("test-local-password", salt)
	protected, e := Protect(key, salt, []byte("private refresh-token"))
	if e != nil {
		t.Fatal(e)
	}
	if bytes.Contains(protected, []byte("refresh-token")) {
		t.Fatal("plaintext persisted")
	}
	_, _, plain, e := Unprotect("test-local-password", protected)
	if e != nil || string(plain) != "private refresh-token" {
		t.Fatal(e)
	}
	if _, _, _, e = Unprotect("wrong-local-password", protected); e == nil {
		t.Fatal("local password bypass")
	}
	device := ID()
	snap := Snapshot{Protocol, m.Vault, device, 1, Clock{device: 1}, map[string]Entry{"server/abcdefgh": {json.RawMessage(`{"name":"secret"}`), Clock{device: 1}}}}
	obj, data, e := EncodeSnapshot(snap, master)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = DecodeSnapshot(obj, data, master, m.Vault); e != nil {
		t.Fatal(e)
	}
	bad := append([]byte(nil), data...)
	bad[30] ^= 1
	obj.Hash = Hash(bad)
	if _, e = DecodeSnapshot(obj, bad, master, m.Vault); e == nil {
		t.Fatal("tampering accepted")
	}
	if _, e = DecodeSnapshot(obj, data, master, ID()); e == nil {
		t.Fatal("cross vault accepted")
	}
}
func TestVectorMergeConcurrentEditAndDelete(t *testing.T) {
	a, b := ID(), ID()
	v := ID()
	base := Snapshot{Protocol, v, a, 1, Clock{a: 1}, map[string]Entry{"server/abcdefgh": {json.RawMessage(`"base"`), Clock{a: 1}}}}
	one := Clone(base)
	one.Clock[a] = 2
	one.Entries["server/abcdefgh"] = Entry{json.RawMessage(`"A"`), Clock{a: 2}}
	two := Clone(base)
	two.Device = b
	two.Clock[b] = 1
	two.Entries["server/abcdefgh"] = Entry{json.RawMessage(`null`), Clock{a: 1, b: 1}}
	_, conflicts := Merge(one, two)
	if len(conflicts) != 1 {
		t.Fatal("concurrent deletion lost", conflicts)
	}
	resolved := Clone(one)
	resolved.Clock = Union(one.Clock, two.Clock)
	resolved.Clock[a] = 3
	resolved.Entries["server/abcdefgh"] = Entry{json.RawMessage(`null`), resolved.Clock}
	result, conflicts := Merge(two, resolved)
	if len(conflicts) != 0 || !EqualJSON(result.Entries["server/abcdefgh"].Value, []byte("null")) {
		t.Fatal("resolved deletion resurrected")
	}
	two.Entries = map[string]Entry{"command/abcdefgh": {json.RawMessage(`"cmd"`), Clock{a: 1, b: 1}}}
	result, conflicts = Merge(one, two)
	if len(conflicts) != 0 || len(result.Entries) != 2 {
		t.Fatal("independent changes lost")
	}
}
func TestHeadsRejectClonesAndBounds(t *testing.T) {
	id := ID()
	o := Object{ID: ID(), Device: id, Sequence: 1, Hash: ID(), Size: 72}
	clone := o
	clone.Hash = ID()
	if _, e := Heads([]Object{o, clone}); e == nil {
		t.Fatal("duplicate sequence accepted")
	}
	o.Size = MaxBlob + 1
	if _, e := Heads([]Object{o}); e == nil {
		t.Fatal("oversize accepted")
	}
}
