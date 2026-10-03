package app

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func aiTestSettings(base string) aiSettings {
	return aiSettings{Provider: "fixture", Timeout: 30, Providers: []aiProvider{{ID: "fixture", Name: "Fixture", Format: "openai", BaseURL: base, Model: "test-model", APIKey: "ai-fixture-secret-123"}}}
}

func aiTestInput() aiChatRequest {
	return aiChatRequest{RequestID: "test-request", Messages: []aiMessage{{Role: "system", Content: "Use the supplied tools."}, {Role: "user", Content: "Check the terminal."}}, Tools: []aiTool{{Name: "terminal_read", Description: "Read terminal text", Parameters: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`)}}}
}

func TestAISettingsEncryptedAndSeparate(t *testing.T) {
	a := lockTestApp(t)
	s := aiTestSettings("https://fixture.invalid/v1")
	public, err := a.saveAISettings(s)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(public)
	if bytes.Contains(raw, []byte(s.Providers[0].APIKey)) || bytes.Contains(raw, []byte(`"apiKey"`)) || !public.Providers[0].HasKey {
		t.Fatal("settings response exposed or lost credential metadata")
	}
	disk, err := os.ReadFile(filepath.Join(a.store.dir, aiSettingsFile))
	if err != nil || bytes.Contains(disk, []byte(s.Providers[0].APIKey)) || bytes.Contains(disk, []byte("fixture.invalid")) {
		t.Fatal("AI settings were not encrypted", err)
	}
	info, err := os.Stat(filepath.Join(a.store.dir, aiSettingsFile))
	if err != nil {
		t.Fatal(err)
	}
	// Windows FileMode reports DOS read-only attributes, not Unix owner permissions.
	if runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0 {
		t.Fatal("AI settings readable outside owner")
	}
	config, _ := json.Marshal(a.store.List())
	if bytes.Contains(config, []byte("ai-fixture")) || bytes.Contains(config, []byte("fixture.invalid")) {
		t.Fatal("AI settings escaped into connection configuration")
	}
	loaded, err := a.loadAISettings()
	if err != nil || loaded.Providers[0].APIKey != s.Providers[0].APIKey {
		t.Fatal("encrypted credential did not round trip", err)
	}
	public.Providers[0].Model = "changed-model"
	if _, err = a.saveAISettings(public); err != nil {
		t.Fatal(err)
	}
	loaded, _ = a.loadAISettings()
	if loaded.Providers[0].APIKey != s.Providers[0].APIKey {
		t.Fatal("omitted credential was cleared")
	}
	public.Providers[0].BaseURL = "https://other.invalid/v1"
	if _, err = a.saveAISettings(public); err != nil {
		t.Fatal(err)
	}
	loaded, _ = a.loadAISettings()
	if loaded.Providers[0].APIKey != "" {
		t.Fatal("credential followed changed endpoint")
	}
	public.Providers[0].APIKey = "new-fixture-secret"
	if _, err = a.saveAISettings(public); err != nil {
		t.Fatal(err)
	}
	public.Providers[0].APIKey, public.Providers[0].ClearKey = "", true
	if _, err = a.saveAISettings(public); err != nil {
		t.Fatal(err)
	}
	loaded, _ = a.loadAISettings()
	if loaded.Providers[0].APIKey != "" {
		t.Fatal("explicit clear failed")
	}
	// Damaged state is never overwritten by a fresh UI's defaults.
	bad := []byte("damaged settings")
	if err := os.WriteFile(filepath.Join(a.store.dir, aiSettingsFile), bad, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = a.saveAISettings(s); err == nil {
		t.Fatal("corrupt AI settings overwritten")
	}
	after, _ := os.ReadFile(filepath.Join(a.store.dir, aiSettingsFile))
	if !bytes.Equal(after, bad) {
		t.Fatal("corrupt original changed")
	}
}

func TestAISettingsRejectChangedKeyAndLockedWrite(t *testing.T) {
	a := lockTestApp(t)
	s := aiTestSettings("https://fixture.invalid")
	if _, err := a.saveAISettings(s); err != nil {
		t.Fatal(err)
	}
	enableTestPassword(t, a, 0)
	if _, err := a.LockNow(); err != nil {
		t.Fatal(err)
	}
	if _, err := a.saveAISettings(s); err == nil {
		t.Fatal("locked setting write succeeded")
	}
	if _, err := a.Unlock(LockProof{Method: "password", Value: "1234"}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(a.store.dir, ConfigKeyName), bytes.Repeat([]byte{0x55}, 32), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := a.saveAISettings(s); err == nil {
		t.Fatal("changed local key accepted")
	}
}

func TestAIProtocolToolRoundTrips(t *testing.T) {
	for _, format := range []string{"openai", "anthropic", "gemini"} {
		t.Run(format, func(t *testing.T) {
			var count atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				var request map[string]json.RawMessage
				if err := json.Unmarshal(body, &request); err != nil {
					t.Error(err)
				}
				turn := count.Add(1)
				header, path := "Authorization", "/chat/completions"
				key := "Bearer test-fixture-key"
				if format == "anthropic" {
					header, key, path = "x-api-key", "test-fixture-key", "/v1/messages"
				}
				if format == "gemini" {
					header, key, path = "x-goog-api-key", "test-fixture-key", "/v1beta/models/test-model:generateContent"
				}
				if r.Header.Get(header) != key || r.URL.Path != path {
					t.Errorf("wrong auth or endpoint: %s", r.URL.Path)
				}
				if format == "anthropic" && r.Header.Get("anthropic-version") != "2023-06-01" {
					t.Error("missing Anthropic version")
				}
				if turn == 1 {
					if !bytes.Contains(body, []byte("terminal_read")) {
						t.Error("missing tool schema")
					}
					switch format {
					case "openai":
						io.WriteString(w, `{"choices":[{"finish_reason":"tool_calls","message":{"role":"assistant","content":null,"reasoning_content":"opaque reasoning","tool_calls":[{"id":"call-1","type":"function","function":{"name":"terminal_read","arguments":"{}"}}]}}]}`)
					case "anthropic":
						io.WriteString(w, `{"role":"assistant","stop_reason":"tool_use","content":[{"type":"thinking","thinking":"opaque reasoning","signature":"signed-block"},{"type":"redacted_thinking","data":"hidden-block"},{"type":"tool_use","id":"call-1","name":"terminal_read","input":{}}]}`)
					case "gemini":
						io.WriteString(w, `{"candidates":[{"finishReason":"STOP","content":{"role":"model","parts":[{"thought":true,"text":"opaque reasoning"},{"functionCall":{"id":"native-call","name":"terminal_read","args":{}},"thoughtSignature":"signed-block"}]}}]}`)
					}
					return
				}
				if !bytes.Contains(body, []byte("terminal fixture result")) {
					t.Error("tool result not sent back")
				}
				switch format {
				case "openai":
					if !bytes.Contains(body, []byte(`"reasoning_content":"opaque reasoning"`)) || !bytes.Contains(body, []byte(`"tool_call_id":"call-1"`)) {
						t.Error("reasoning or tool result ID lost")
					}
					io.WriteString(w, `{"choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"done"}}]}`)
				case "anthropic":
					if !bytes.Contains(body, []byte(`"signature":"signed-block"`)) || !bytes.Contains(body, []byte(`"data":"hidden-block"`)) || !bytes.Contains(body, []byte(`"tool_use_id":"call-1"`)) {
						t.Error("thinking blocks or tool result ID lost")
					}
					io.WriteString(w, `{"role":"assistant","stop_reason":"end_turn","content":[{"type":"text","text":"done"}]}`)
				case "gemini":
					if !bytes.Contains(body, []byte(`"thoughtSignature":"signed-block"`)) || !bytes.Contains(body, []byte(`"functionResponse":{"id":"native-call","name":"terminal_read"`)) {
						t.Error("thought signature or native function ID lost")
					}
					io.WriteString(w, `{"candidates":[{"finishReason":"STOP","content":{"role":"model","parts":[{"text":"done"}]}}]}`)
				}
			}))
			defer server.Close()
			p := aiProvider{ID: "fixture", Name: "Fixture", Format: format, BaseURL: server.URL, Model: "test-model", APIKey: "test-fixture-key"}
			input := aiTestInput()
			result, err := aiChat(context.Background(), server.Client(), p, input)
			if err != nil {
				t.Fatal(err)
			}
			if result.Message.Content != "" || len(result.Message.ToolCalls) != 1 {
				t.Fatalf("wrong canonical response: %+v", result.Message)
			}
			// Exercise the actual JSON boundary; native metadata is opaque to UI.
			serialized, _ := json.Marshal(result.Message)
			var replay aiMessage
			if err := json.Unmarshal(serialized, &replay); err != nil {
				t.Fatal(err)
			}
			input.Messages = append(input.Messages, replay, aiMessage{Role: "tool", ToolCallID: replay.ToolCalls[0].ID, Content: "terminal fixture result"})
			if err := aiValidateChat(input); err != nil {
				t.Fatal(err)
			}
			result, err = aiChat(context.Background(), server.Client(), p, input)
			if err != nil || result.Message.Content != "done" || count.Load() != 2 {
				t.Fatal("tool loop failed", err)
			}
			p.Model = "different-model"
			if _, err = aiChat(context.Background(), server.Client(), p, input); err == nil || count.Load() != 2 {
				t.Fatal("native metadata crossed model boundary")
			}
		})
	}
}

func TestAIParallelToolResultsStayGrouped(t *testing.T) {
	for _, format := range []string{"anthropic", "gemini"} {
		t.Run(format, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var input map[string]json.RawMessage
				_ = json.NewDecoder(r.Body).Decode(&input)
				field := "messages"
				if format == "gemini" {
					field = "contents"
				}
				var history []map[string]json.RawMessage
				_ = json.Unmarshal(input[field], &history)
				if len(history) != 3 {
					t.Errorf("parallel results split across %d messages", len(history))
				}
				if format == "anthropic" {
					io.WriteString(w, `{"role":"assistant","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn"}`)
				} else {
					io.WriteString(w, `{"candidates":[{"content":{"role":"model","parts":[{"text":"ok"}]}}]}`)
				}
			}))
			defer server.Close()
			p := aiProvider{ID: "fixture", Format: format, BaseURL: server.URL, Model: "fixture"}
			input := aiTestInput()
			input.Messages = append(input.Messages, aiMessage{Role: "assistant", ToolCalls: []aiToolCall{{ID: "one", Name: "terminal_read", Arguments: "{}"}, {ID: "two", Name: "terminal_read", Arguments: "{}"}}}, aiMessage{Role: "tool", ToolCallID: "one", Content: "one"}, aiMessage{Role: "tool", ToolCallID: "two", Content: "two"})
			if _, err := aiChat(context.Background(), server.Client(), p, input); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestAICancelHTTPAndLock(t *testing.T) {
	for _, mode := range []string{"cancel", "lock", "app-context", "close"} {
		t.Run(mode, func(t *testing.T) {
			a := lockTestApp(t)
			if mode == "lock" {
				enableTestPassword(t, a, 0)
			}
			started := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				close(started)
				<-r.Context().Done()
			}))
			defer server.Close()
			if _, err := a.saveAISettings(aiTestSettings(server.URL)); err != nil {
				t.Fatal(err)
			}
			mux := http.NewServeMux()
			a.registerAIHTTP(mux)
			input, _ := json.Marshal(aiTestInput())
			done := make(chan *httptest.ResponseRecorder, 1)
			go func() {
				w := httptest.NewRecorder()
				mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/ai/chat", bytes.NewReader(input)))
				done <- w
			}()
			select {
			case <-started:
			case <-time.After(3 * time.Second):
				t.Fatal("provider request did not start")
			}
			switch mode {
			case "cancel":
				w := httptest.NewRecorder()
				mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/ai/cancel", strings.NewReader(`{"requestId":"test-request"}`)))
				if w.Code != 200 {
					t.Fatal(w.Body.String())
				}
			case "lock":
				if _, err := a.LockNow(); err != nil {
					t.Fatal(err)
				}
			case "app-context":
				a.cancel()
			case "close":
				a.closeAI()
			}
			select {
			case w := <-done:
				if w.Code == 200 || strings.Contains(w.Body.String(), "ai-fixture-secret") {
					t.Fatal("cancelled request returned successful response or secret", w.Code)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("AI request was not cancelled")
			}
		})
	}
}

func TestAICancelBeforeRegistrationAndDuplicateID(t *testing.T) {
	a := lockTestApp(t)
	a.cancelAIRequest("first")
	if _, finish, err := a.beginAIRequest(context.Background(), "first", 30); err == nil {
		finish()
		t.Fatal("cancel before request registration was forgotten")
	}
	ctx, finish, err := a.beginAIRequest(context.Background(), "unique", 30)
	if err != nil {
		t.Fatal(err)
	}
	defer finish()
	if _, duplicateFinish, err := a.beginAIRequest(context.Background(), "unique", 30); err == nil {
		duplicateFinish()
		t.Fatal("duplicate ID replaced active request")
	}
	a.cancelAIRequest("unique")
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("duplicate request invalidated cancellation")
	}
}

func TestAIIdleLockCancelsWithoutStatusPolling(t *testing.T) {
	a := lockTestApp(t)
	enableTestPassword(t, a, 60)
	started, cancelled := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		close(started)
		<-r.Context().Done()
		close(cancelled)
	}))
	defer server.Close()
	// Cleanup must release the fake server even when the assertion fails.
	defer a.cancelAIRequests()
	if _, err := a.saveAISettings(aiTestSettings(server.URL)); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	a.registerAIHTTP(mux)
	input, _ := json.Marshal(aiTestInput())
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/ai/chat", bytes.NewReader(input)))
		done <- w
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("provider request did not start")
	}
	// Only the idle ticker may detect this expiry. No status request or
	// RequireUnlocked call is made while waiting for the provider cancellation.
	advanceLockClock(a, 61*time.Second)
	select {
	case <-cancelled:
	case <-time.After(2 * time.Second):
		t.Fatal("idle ticker did not cancel AI without frontend status polling")
	}
	select {
	case w := <-done:
		if w.Code != http.StatusLocked {
			t.Fatalf("idle lock response = %d, want %d", w.Code, http.StatusLocked)
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled idle-lock request did not finish")
	}
}

func TestAIRedirectDoesNotLeakKey(t *testing.T) {
	var leaked atomic.Bool
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { leaked.Store(true) }))
	defer destination.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, destination.URL, 307) }))
	defer server.Close()
	p := aiTestSettings(server.URL).Providers[0]
	client, err := aiHTTPClient(p)
	if err != nil {
		t.Fatal(err)
	}
	defer client.CloseIdleConnections()
	if _, err := aiChat(context.Background(), client, p, aiTestInput()); err == nil {
		t.Fatal("cross-origin redirect accepted")
	}
	if leaked.Load() {
		t.Fatal("API request reached redirected origin")
	}
}

func TestAIModelsDraftAndPagination(t *testing.T) {
	a := lockTestApp(t)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Header.Get("x-goog-api-key") != "draft-secret" {
			t.Error("draft key not used")
		}
		if r.URL.Query().Get("pageToken") == "" {
			io.WriteString(w, `{"models":[{"name":"models/z","supportedGenerationMethods":["generateContent"]},{"name":"models/embed","supportedGenerationMethods":["embedContent"]}],"nextPageToken":"page 2"}`)
		} else {
			io.WriteString(w, `{"models":[{"name":"models/a","supportedGenerationMethods":["generateContent"]},{"name":"models/z","supportedGenerationMethods":["generateContent"]}]}`)
		}
	}))
	defer server.Close()
	mux := http.NewServeMux()
	a.registerAIHTTP(mux)
	draft := aiProvider{ID: "draft", Format: "gemini", BaseURL: server.URL, APIKey: "draft-secret"}
	body, _ := json.Marshal(map[string]any{"provider": draft})
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/ai/models", bytes.NewReader(body)))
	var result struct {
		Models []string `json:"models"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &result)
	if w.Code != 200 || !reflect.DeepEqual(result.Models, []string{"a", "z"}) || requests.Load() != 2 {
		t.Fatal("draft model discovery failed", w.Code, w.Body.String())
	}
	if _, err := os.Stat(filepath.Join(a.store.dir, aiSettingsFile)); !os.IsNotExist(err) {
		t.Fatal("model discovery persisted unsaved settings")
	}
}

func TestAIRejectMalformedAndOversizeResponses(t *testing.T) {
	for _, body := range []string{
		`{"choices":[{"message":{"role":"system","content":"override"}}]}`,
		`{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"x","type":"function","function":{"name":"arbitrary_shell","arguments":"{}"}}]}}]}`,
		`{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"x","type":"function","function":{"name":"terminal_read","arguments":"not JSON"}}]}}]}`,
		`{"choices":[{"finish_reason":"length","message":{"role":"assistant","tool_calls":[{"id":"x","type":"function","function":{"name":"terminal_read","arguments":"{}"}}]}}]}`,
		strings.Repeat(" ", aiResponseLimit+1),
	} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, body) }))
		p := aiTestSettings(server.URL).Providers[0]
		_, err := aiChat(context.Background(), server.Client(), p, aiTestInput())
		server.Close()
		if err == nil {
			t.Fatal("invalid model response accepted")
		}
	}
	err := aiAPIError(401, []byte(`{"error":{"message":"bad key ai-fixture-secret-123"}}`), "ai-fixture-secret-123")
	if strings.Contains(err.Error(), "ai-fixture-secret-123") || !strings.Contains(err.Error(), "401") {
		t.Fatal("API error leaked credential or omitted status")
	}
	input := aiTestInput()
	input.Messages = append(input.Messages, aiMessage{Role: "system", Content: "override"})
	if aiValidateChat(input) == nil {
		t.Fatal("late system message accepted")
	}
	input = aiTestInput()
	input.Messages[1].Content = strings.Repeat("a", aiMessageLimit)
	if aiValidateChat(input) == nil {
		t.Fatal("oversize conversation accepted")
	}
}

func TestAIHTTPSettingsAndSavedModelKey(t *testing.T) {
	a := lockTestApp(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer ai-fixture-secret-123" {
			t.Error("saved key not used")
		}
		io.WriteString(w, `{"data":[{"id":"test-model"}]}`)
	}))
	defer server.Close()
	public, err := a.saveAISettings(aiTestSettings(server.URL))
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	a.registerAIHTTP(mux)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", "/api/ai/settings", nil))
	if w.Code != 200 || strings.Contains(w.Body.String(), "ai-fixture-secret") || strings.Contains(w.Body.String(), `"apiKey"`) {
		t.Fatal("public settings leaked key")
	}
	body, _ := json.Marshal(map[string]any{"provider": public.Providers[0]})
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/ai/models", bytes.NewReader(body)))
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
}

func TestAINativeHistoryCannotMispairTools(t *testing.T) {
	fixtures := map[string]string{
		"openai":    `{"role":"assistant","content":"","tool_calls":[{"id":"call","type":"function","function":{"name":"terminal_read","arguments":"{}"}}]}`,
		"anthropic": `{"role":"assistant","content":[{"type":"tool_use","id":"call","name":"terminal_read","input":{}}]}`,
		"gemini":    `{"role":"model","parts":[{"functionCall":{"id":"call","name":"terminal_read","args":{}},"thoughtSignature":"keep-exact"}]}`,
	}
	for format, raw := range fixtures {
		t.Run(format, func(t *testing.T) {
			p := aiProvider{ID: "fixture", Format: format, BaseURL: "http://127.0.0.1", Model: "fixture"}
			original := aiMessage{Role: "assistant", ToolCalls: []aiToolCall{{ID: "call", Name: "terminal_read", Arguments: "{}"}}, ProviderData: aiPreserve(p, json.RawMessage(raw))}
			if _, err := aiNative(original, p); err != nil {
				t.Fatal(err)
			}
			for _, mutate := range []func(*aiMessage){func(m *aiMessage) { m.ToolCalls[0].ID = "different" }, func(m *aiMessage) { m.ToolCalls[0].Name = "other" }, func(m *aiMessage) { m.ToolCalls[0].Arguments = `{"extra":true}` }, func(m *aiMessage) { m.Content = "different" }} {
				m := original
				m.ToolCalls = append([]aiToolCall(nil), original.ToolCalls...)
				mutate(&m)
				if _, err := aiNative(m, p); err == nil {
					t.Fatal("mismatched native and normalized history accepted")
				}
			}
		})
	}
}
