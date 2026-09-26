package syncserver

import (
	"cloudshell/internal/syncvault"
	"errors"
	"time"
)

var errStorageCapacity = errors.New("各设备最新快照已超过同步空间容量，请减少同步数据或新建空间")

// Publish and prune in one transaction. A failed write must retain all prior
// snapshots. History may yield space, but each device's latest head must remain.
func (s *Server) storeSnapshot(o syncvault.Object, data []byte) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec("INSERT INTO snapshots VALUES(?,?,?,?,?)", o.Hash, o.Device, o.Sequence, time.Now().Unix(), data); err != nil {
		return err
	}
	if _, err = tx.Exec("DELETE FROM snapshots WHERE device=? AND hash NOT IN (SELECT hash FROM snapshots WHERE device=? ORDER BY seq DESC LIMIT 20)", o.Device, o.Device); err != nil {
		return err
	}
	var total int64
	if err = tx.QueryRow("SELECT COALESCE(sum(length(data)),0) FROM snapshots").Scan(&total); err != nil {
		return err
	}
	if total > s.maxStorageBytes {
		rows, err := tx.Query(`SELECT hash,length(data) FROM snapshots AS old
WHERE seq < (SELECT MAX(seq) FROM snapshots WHERE device=old.device)
ORDER BY created,seq,hash`)
		if err != nil {
			return err
		}
		type obsolete struct {
			hash string
			size int64
		}
		history := []obsolete{}
		for rows.Next() {
			var old obsolete
			if err = rows.Scan(&old.hash, &old.size); err != nil {
				rows.Close()
				return err
			}
			history = append(history, old)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		for _, old := range history {
			if total <= s.maxStorageBytes {
				break
			}
			if _, err = tx.Exec("DELETE FROM snapshots WHERE hash=?", old.hash); err != nil {
				return err
			}
			total -= old.size
		}
	}
	if total > s.maxStorageBytes {
		return errStorageCapacity
	}
	return tx.Commit()
}
