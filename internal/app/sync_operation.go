package app

import (
	"context"
	"errors"
	"net/http"
)

// Every operation holding syncState.mu participates in cancellation, including
// setup and history requests. The UI lock must not wait for a network timeout.
func (a *App) beginSyncOperation(ctx context.Context) (context.Context, func(), error) {
	s := &a.syncState
	s.mu.Lock()
	ctx, cancel := context.WithCancel(ctx)
	s.controlMu.Lock()
	if s.closed {
		s.controlMu.Unlock()
		cancel()
		s.mu.Unlock()
		return nil, nil, errors.New("同步服务正在关闭")
	}
	s.activeCancel = cancel
	s.controlMu.Unlock()
	finish := func() {
		cancel()
		s.controlMu.Lock()
		s.activeCancel = nil
		s.controlMu.Unlock()
		if a.SecurityLockStatus().Locked {
			a.pauseSyncLocked()
		}
		s.mu.Unlock()
	}
	if err := a.RequireUnlocked(); err != nil {
		finish()
		return nil, nil, err
	}
	if err := ctx.Err(); err != nil {
		finish()
		return nil, nil, err
	}
	return ctx, finish, nil
}

func (a *App) cancelActiveSync() {
	s := &a.syncState
	s.controlMu.Lock()
	if s.activeCancel != nil {
		s.activeCancel()
	}
	s.controlMu.Unlock()
}

func (a *App) lockSyncHTTP(w http.ResponseWriter, r *http.Request) (*http.Request, func(), bool) {
	ctx, finish, err := a.beginSyncOperation(r.Context())
	if err != nil {
		a.respondSync(w, nil, err)
		return r, nil, false
	}
	return r.WithContext(ctx), finish, true
}

func (a *App) respondSync(w http.ResponseWriter, value any, err error) {
	// Recheck after potentially long I/O/KDF work, before returning any secrets.
	if locked := a.RequireUnlocked(); locked != nil {
		writeError(w, http.StatusLocked, locked)
		return
	}
	respond(w, value, err)
}
