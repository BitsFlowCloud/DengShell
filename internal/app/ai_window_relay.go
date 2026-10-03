package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"sync"
	"time"
)

const (
	aiWindowLimit      = 16
	aiWindowTombstones = 64
	aiWindowEventLimit = 256
	aiWindowPayloadMax = 1536 << 10
	aiWindowQueueMax   = 4 << 20
	aiWindowHeartbeat  = 15 * time.Second
	aiWindowStartup    = 45 * time.Second
	aiWindowClosedTTL  = 45 * time.Second
)

var aiWindowIDPattern = regexp.MustCompile(`^[0-9a-f]{48}$`)

// The relay carries opaque JSON only. The owning window retains its runner and
// tool whitelist; an assistant window never gets a backend execution endpoint.
// No relay data, window IDs or messages are persisted or written to logs.
type aiWindowEvent struct {
	Seq     uint64          `json:"seq"`
	Payload json.RawMessage `json:"payload"`
}

type aiWindowQueue struct {
	lastSeq uint64
	ack     uint64
	events  []aiWindowEvent
	bytes   int
}

type aiWindowRoom struct {
	created  time.Time
	seen     [2]time.Time
	queues   [2]aiWindowQueue
	closedAt time.Time
	closed   bool
	reason   string
}

type aiWindowState struct {
	mu     sync.Mutex
	rooms  map[string]*aiWindowRoom
	closed bool
	stop   chan struct{}
	done   chan struct{}
	now    func() time.Time // deterministic clock in isolated tests
}

type aiWindowPoll struct {
	Events  []aiWindowEvent `json:"events"`
	Closed  bool            `json:"closed"`
	LastSeq uint64          `json:"lastSeq"`
	Reason  string          `json:"reason,omitempty"`
}

type aiWindowError struct {
	status  int
	message string
}

func (e *aiWindowError) Error() string { return e.message }

func aiWindowSide(side string) (int, error) {
	switch side {
	case "owner":
		return 0, nil
	case "assistant":
		return 1, nil
	default:
		return 0, &aiWindowError{http.StatusBadRequest, "AI 窗口通信角色无效"}
	}
}

func (s *aiWindowState) clockLocked() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

func (r *aiWindowRoom) closeLocked(now time.Time, reason string) {
	if r.closed {
		return
	}
	r.closed, r.closedAt, r.reason = true, now, reason
	for i := range r.queues {
		// Release any pending credential input or conversation snapshot promptly.
		r.queues[i].events = nil
		r.queues[i].bytes = 0
	}
}

func (s *aiWindowState) cleanupLocked(now time.Time) {
	for id, room := range s.rooms {
		if !room.closed {
			switch {
			case now.Sub(room.seen[0]) >= aiWindowHeartbeat:
				room.closeLocked(now, "owner_timeout")
			case room.seen[1].IsZero() && now.Sub(room.created) >= aiWindowStartup:
				room.closeLocked(now, "startup_timeout")
			case !room.seen[1].IsZero() && now.Sub(room.seen[1]) >= aiWindowHeartbeat:
				room.closeLocked(now, "assistant_timeout")
			}
		}
		if room.closed && now.Sub(room.closedAt) >= aiWindowClosedTTL {
			delete(s.rooms, id)
		}
	}
	s.trimTombstonesLocked()
}

func (s *aiWindowState) trimTombstonesLocked() {
	for {
		count, oldest := 0, ""
		var at time.Time
		for id, room := range s.rooms {
			if room.closed {
				count++
				if oldest == "" || room.closedAt.Before(at) {
					oldest, at = id, room.closedAt
				}
			}
		}
		if count <= aiWindowTombstones {
			return
		}
		delete(s.rooms, oldest)
	}
}

func (s *aiWindowState) startLocked(ctx context.Context) {
	if s.stop != nil {
		return
	}
	s.stop, s.done = make(chan struct{}), make(chan struct{})
	stop, done := s.stop, s.done
	var ctxDone <-chan struct{}
	if ctx != nil {
		ctxDone = ctx.Done()
	}
	go func() {
		defer close(done)
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ctxDone:
				s.mu.Lock()
				s.closed, s.rooms = true, nil
				s.mu.Unlock()
				return
			case <-ticker.C:
				s.mu.Lock()
				s.cleanupLocked(s.clockLocked())
				s.mu.Unlock()
			}
		}
	}()
}

func (a *App) createAIWindow() (string, error) {
	s := &a.aiWindows
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || a.ctx != nil && a.ctx.Err() != nil {
		return "", &aiWindowError{http.StatusServiceUnavailable, "AI 窗口服务已关闭"}
	}
	now := s.clockLocked()
	s.cleanupLocked(now)
	active := 0
	for _, room := range s.rooms {
		if !room.closed {
			active++
		}
	}
	if active >= aiWindowLimit {
		return "", &aiWindowError{http.StatusTooManyRequests, "同时打开的 AI 窗口过多，请先关闭其他窗口"}
	}
	if s.rooms == nil {
		s.rooms = make(map[string]*aiWindowRoom)
	}
	var id string
	for {
		var random [24]byte
		if _, err := rand.Read(random[:]); err != nil {
			return "", errors.New("无法生成 AI 窗口标识")
		}
		id = hex.EncodeToString(random[:])
		if s.rooms[id] == nil {
			break
		}
	}
	s.rooms[id] = &aiWindowRoom{created: now, seen: [2]time.Time{now, {}}}
	s.startLocked(a.ctx)
	return id, nil
}

func (a *App) pollAIWindow(id, side string, after uint64, probe bool) (aiWindowPoll, error) {
	result := aiWindowPoll{Events: []aiWindowEvent{}}
	if !aiWindowIDPattern.MatchString(id) {
		return result, &aiWindowError{http.StatusBadRequest, "AI 窗口标识无效"}
	}
	index, err := aiWindowSide(side)
	if err != nil {
		return result, err
	}
	s := &a.aiWindows
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.clockLocked()
	s.cleanupLocked(now)
	room := s.rooms[id]
	if room == nil || s.closed {
		result.Closed, result.Reason = true, "expired"
		if s.closed {
			result.Reason = "app_closed"
		}
		return result, nil
	}
	queue := &room.queues[index]
	result.LastSeq = queue.lastSeq
	if room.closed {
		result.Closed, result.Reason = true, room.reason
		return result, nil
	}
	// Native launch/focus validation is a liveness probe, not a consumer. It
	// must still work after the child has acknowledged a later cursor, and it
	// never needs a copy of conversation or credential-input payloads.
	if probe {
		return result, nil
	}
	if after < queue.ack || after > queue.lastSeq {
		return result, &aiWindowError{http.StatusConflict, "AI 窗口消息游标无效，请关闭后重新打开窗口"}
	}
	// `after` acknowledges already processed events. Merely reading does
	// not consume newly returned messages: retrying the same cursor is safe.
	queue.ack = after
	remove := 0
	for remove < len(queue.events) && queue.events[remove].Seq <= after {
		queue.bytes -= len(queue.events[remove].Payload)
		queue.events[remove].Payload = nil
		remove++
	}
	if remove > 0 {
		remaining := append([]aiWindowEvent(nil), queue.events[remove:]...)
		queue.events = remaining
	}
	room.seen[index] = now
	for _, event := range queue.events {
		if event.Seq > after {
			result.Events = append(result.Events, aiWindowEvent{Seq: event.Seq, Payload: append(json.RawMessage(nil), event.Payload...)})
		}
	}
	return result, nil
}

func (a *App) postAIWindow(id, side string, payload json.RawMessage) (uint64, error) {
	if !aiWindowIDPattern.MatchString(id) {
		return 0, &aiWindowError{http.StatusBadRequest, "AI 窗口标识无效"}
	}
	index, err := aiWindowSide(side)
	if err != nil {
		return 0, err
	}
	if len(payload) == 0 || !json.Valid(payload) {
		return 0, &aiWindowError{http.StatusBadRequest, "AI 窗口消息必须是有效 JSON"}
	}
	s := &a.aiWindows
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.clockLocked()
	s.cleanupLocked(now)
	room := s.rooms[id]
	if room == nil || room.closed || s.closed {
		return 0, &aiWindowError{http.StatusGone, "AI 窗口已关闭，请重新打开"}
	}
	if len(payload) > aiWindowPayloadMax {
		room.closeLocked(now, "overflow")
		s.trimTombstonesLocked()
		return 0, &aiWindowError{http.StatusRequestEntityTooLarge, "AI 窗口消息过大，窗口已关闭，请重新打开"}
	}
	target := &room.queues[1-index]
	if len(target.events) >= aiWindowEventLimit || room.queues[0].bytes+room.queues[1].bytes+len(payload) > aiWindowQueueMax {
		room.closeLocked(now, "overflow")
		s.trimTombstonesLocked()
		return 0, &aiWindowError{http.StatusConflict, "AI 窗口消息积压过多，窗口已关闭，请重新打开"}
	}
	room.seen[index] = now
	target.lastSeq++
	target.events = append(target.events, aiWindowEvent{Seq: target.lastSeq, Payload: append(json.RawMessage(nil), payload...)})
	target.bytes += len(payload)
	return target.lastSeq, nil
}

func (a *App) deleteAIWindow(id string) error {
	if !aiWindowIDPattern.MatchString(id) {
		return &aiWindowError{http.StatusBadRequest, "AI 窗口标识无效"}
	}
	s := &a.aiWindows
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.clockLocked()
	s.cleanupLocked(now)
	if room := s.rooms[id]; room != nil {
		room.closeLocked(now, "closed")
	}
	s.trimTombstonesLocked()
	return nil
}

func (a *App) suspendAIWindows(reason string) {
	s := &a.aiWindows
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.clockLocked()
	for _, room := range s.rooms {
		room.closeLocked(now, reason)
	}
	s.trimTombstonesLocked()
}

func (a *App) closeAIWindows() {
	s := &a.aiWindows
	s.mu.Lock()
	s.closed, s.rooms = true, nil
	if s.stop != nil {
		select {
		case <-s.stop:
		default:
			close(s.stop)
		}
	}
	done := s.done
	s.mu.Unlock()
	if done != nil {
		<-done
	}
}

func (a *App) respondAIWindow(w http.ResponseWriter, value any, err error) {
	w.Header().Set("Cache-Control", "no-store")
	// No aiWindows.mu is held here; lock callbacks suspend and erase relays.
	if locked := a.RequireUnlocked(); locked != nil {
		writeError(w, http.StatusLocked, locked)
		return
	}
	if err != nil {
		status := http.StatusBadRequest
		var failure *aiWindowError
		if errors.As(err, &failure) {
			status = failure.status
		}
		writeError(w, status, err)
		return
	}
	writeJSON(w, value)
}

func (a *App) registerAIWindowHTTP(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/ai/windows", func(w http.ResponseWriter, r *http.Request) {
		if err := a.RequireUnlocked(); err != nil {
			a.respondAIWindow(w, nil, err)
			return
		}
		var input struct{}
		if !decode(w, r, &input) {
			return
		}
		id, err := a.createAIWindow()
		a.respondAIWindow(w, map[string]string{"id": id}, err)
	})
	mux.HandleFunc("GET /api/ai/windows/{id}", func(w http.ResponseWriter, r *http.Request) {
		if err := a.RequireUnlocked(); err != nil {
			a.respondAIWindow(w, nil, err)
			return
		}
		afterText := r.URL.Query().Get("after")
		if afterText == "" {
			afterText = "0"
		}
		after, err := strconv.ParseUint(afterText, 10, 64)
		if err != nil {
			a.respondAIWindow(w, nil, &aiWindowError{http.StatusBadRequest, "AI 窗口消息游标无效"})
			return
		}
		value, err := a.pollAIWindow(r.PathValue("id"), r.URL.Query().Get("side"), after, r.URL.Query().Get("probe") == "1")
		a.respondAIWindow(w, value, err)
	})
	mux.HandleFunc("POST /api/ai/windows/{id}/messages", func(w http.ResponseWriter, r *http.Request) {
		if err := a.RequireUnlocked(); err != nil {
			a.respondAIWindow(w, nil, err)
			return
		}
		var input struct {
			Side    string          `json:"side"`
			Payload json.RawMessage `json:"payload"`
		}
		r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
		decoder := json.NewDecoder(r.Body)
		if err := decoder.Decode(&input); err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				// Even a wrapper exceeding the HTTP limit must explicitly close
				// the addressed room, not leave the peer waiting on a lost event.
				a.closeAIWindowOverflow(r.PathValue("id"))
				a.respondAIWindow(w, nil, &aiWindowError{http.StatusRequestEntityTooLarge, "AI 窗口消息过大，窗口已关闭"})
			} else {
				a.respondAIWindow(w, nil, &aiWindowError{http.StatusBadRequest, "AI 窗口消息格式无效"})
			}
			return
		}
		if err := decoder.Decode(new(any)); err != io.EOF {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				a.closeAIWindowOverflow(r.PathValue("id"))
				a.respondAIWindow(w, nil, &aiWindowError{http.StatusRequestEntityTooLarge, "AI 窗口消息过大，窗口已关闭"})
			} else {
				a.respondAIWindow(w, nil, &aiWindowError{http.StatusBadRequest, "AI 窗口消息包含多余内容"})
			}
			return
		}
		seq, err := a.postAIWindow(r.PathValue("id"), input.Side, input.Payload)
		a.respondAIWindow(w, map[string]any{"ok": true, "seq": seq}, err)
	})
	mux.HandleFunc("DELETE /api/ai/windows/{id}", func(w http.ResponseWriter, r *http.Request) {
		if err := a.RequireUnlocked(); err != nil {
			a.respondAIWindow(w, nil, err)
			return
		}
		a.respondAIWindow(w, map[string]bool{"ok": true}, a.deleteAIWindow(r.PathValue("id")))
	})
}

func (a *App) closeAIWindowOverflow(id string) {
	s := &a.aiWindows
	s.mu.Lock()
	defer s.mu.Unlock()
	if room := s.rooms[id]; room != nil {
		room.closeLocked(s.clockLocked(), "overflow")
	}
	s.trimTombstonesLocked()
}
