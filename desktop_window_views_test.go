//go:build desktop

package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestWindowTerminalGenerationGuardsLateCleanupAndInput(t *testing.T) {
	received := make(chan string, 1)
	closed := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		socket, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer socket.Close()
		defer close(closed)
		for {
			_, data, err := socket.ReadMessage()
			if err != nil {
				return
			}
			received <- string(data)
		}
	}))
	defer server.Close()
	socket, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer socket.Close()
	current := &nativeTerminal{socket: socket, handoff: "returned-session-new-nonce"}
	desktop := &Desktop{terminals: map[string]*nativeTerminal{"session": current}}
	desktop.CloseTerminalWithHandoff("session", "previous-residence-nonce")
	if desktop.terminals["session"] != current {
		t.Fatal("late old renderer cleanup closed the returned SSH tab")
	}
	if err = desktop.SendTerminalWithHandoff("session", "previous-residence-nonce", "OLD INPUT"); err == nil {
		t.Fatal("late old renderer input reached the new tab")
	}
	if err = desktop.SendTerminalWithHandoff("session", "returned-session-new-nonce", "CURRENT INPUT"); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-received:
		if got != "CURRENT INPUT" {
			t.Fatalf("stale data was sent: %q", got)
		}
	case <-time.After(time.Second):
		t.Fatal("current generation could not send")
	}
	desktop.CloseTerminalWithHandoff("session", "returned-session-new-nonce")
	if desktop.terminals["session"] != nil {
		t.Fatal("current renderer failed to close its socket")
	}
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("current socket remained open")
	}
}
