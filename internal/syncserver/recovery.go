package syncserver

import (
	"cloudshell/internal/syncvault"
	"encoding/json"
	"errors"
	"time"
)

func mustJSON(v any) []byte { b, _ := json.Marshal(v); return b }

func (s *Server) Info() syncvault.Connection {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Connection
}

// LocalMetadata and RestoreOwner are in-process administrative operations.
// Neither is exposed by the network API. Possession of server files alone does
// not decrypt data: the app still verifies the independent password/recovery.
func (s *Server) LocalMetadata() (syncvault.Metadata, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var raw []byte
	var m syncvault.Metadata
	if e := s.db.QueryRow("SELECT v FROM settings WHERE k='meta'").Scan(&raw); e != nil {
		return m, e
	}
	return m, json.Unmarshal(raw, &m)
}
func (s *Server) RestoreOwner(d Join, master []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !validJoin(d) {
		return errors.New("设备信息无效")
	}
	var raw []byte
	var m syncvault.Metadata
	if s.db.QueryRow("SELECT v FROM settings WHERE k='meta'").Scan(&raw) != nil || json.Unmarshal(raw, &m) != nil {
		return errors.New("同步空间数据无效")
	}
	verified, e := m.Unlock("", syncvault.Encode(master))
	clear(verified)
	if e != nil {
		return e
	}
	if old, e := s.authenticate(d.Token); e == nil && old.ID == d.ID && old.Owner {
		return nil
	}
	tx, e := s.db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if _, e = tx.Exec("DELETE FROM devices WHERE owner=1"); e == nil {
		_, e = tx.Exec("INSERT INTO devices VALUES(?,?,?,?,?)", d.ID, d.Name, syncvault.Hash([]byte(d.Token)), 1, time.Now().Unix())
	}
	if e == nil {
		_, e = tx.Exec("DELETE FROM invites")
	}
	if e == nil {
		e = tx.Commit()
	}
	return e
}
