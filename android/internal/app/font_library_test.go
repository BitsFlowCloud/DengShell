package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

type fontTransport func(*http.Request) (*http.Response, error)

func (f fontTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func fontFixture(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "mobile", "assets", "web", "assets", "fonts", "jetbrains-mono.woff2"))
	if err != nil {
		t.Fatal(err)
	}
	return data
}
func fontDescriptor(data []byte) LibraryFile {
	hash := sha256.Sum256(data)
	return LibraryFile{Path: "/fonts/files/test.woff2", SHA256: hex.EncodeToString(hash[:]), Size: int64(len(data))}
}
func fontClient(data []byte) *http.Client {
	return &http.Client{Transport: fontTransport(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(data)), ContentLength: int64(len(data)), Header: make(http.Header), Request: r}, nil
	})}
}
func TestFontLibraryCatalogAndBundle(t *testing.T) {
	catalog, err := libraryCatalogOnce()
	if err != nil {
		t.Fatal(err)
	}
	counts, builtins := map[string]int{}, map[string]int{}
	for _, f := range catalog.Fonts {
		counts[f.Kind]++
		if f.BuiltinID != "" {
			builtins[f.Kind]++
			if f.Kind == "font" && !builtinTerminalFont(f.BuiltinID) || f.Kind == "ui-font" && !builtinUIFont(f.BuiltinID) {
				t.Fatal("unknown builtin", f.ID)
			}
		}
		if !strings.Contains(strings.ToUpper(f.LicenseText), "OPEN FONT LICENSE") {
			t.Fatal("license missing", f.ID)
		}
	}
	if counts["font"] != 30 || counts["ui-font"] != 20 || builtins["font"] != 5 || builtins["ui-font"] != 1 {
		t.Fatal(counts, builtins)
	}
	for _, kind := range []string{"fonts", "ui-fonts"} {
		data, err := os.ReadFile(filepath.Join("..", "..", "mobile", "assets", "web", "assets", kind, "catalog.json"))
		if err != nil {
			t.Fatal(err)
		}
		var bundle struct {
			Fonts []struct{ File, SHA256 string }
		}
		if err = json.Unmarshal(data, &bundle); err != nil {
			t.Fatal(err)
		}
		want := 5
		if kind == "ui-fonts" {
			want = 1
		}
		if len(bundle.Fonts) != want {
			t.Fatal(kind, len(bundle.Fonts))
		}
		for _, font := range bundle.Fonts {
			raw, err := os.ReadFile(filepath.Join("..", "..", "mobile", "assets", "web", font.File))
			if err != nil {
				t.Fatal(err)
			}
			sum := sha256.Sum256(raw)
			if hex.EncodeToString(sum[:]) != font.SHA256 {
				t.Fatal(font.File, "checksum")
			}
		}
	}
}
func TestFontDownloadIntegrityAndCancellation(t *testing.T) {
	data := fontFixture(t)
	file := fontDescriptor(data)
	client := fontClient(data)
	var received int64
	got, err := fetchLibraryFile(context.Background(), client, file, func(n int64) { received = n })
	if err != nil || !bytes.Equal(got, data) || received != file.Size {
		t.Fatal(err, received)
	}
	for _, mutate := range []func(*LibraryFile){func(f *LibraryFile) { f.SHA256 = strings.Repeat("0", 64) }, func(f *LibraryFile) { f.Size-- }, func(f *LibraryFile) { f.Path = "https://other.test/file.woff2" }, func(f *LibraryFile) { f.Path = "/fonts/files/../../secret.woff2" }, func(f *LibraryFile) { f.Size = maxFontBytes + 1 }} {
		bad := file
		mutate(&bad)
		if _, err = fetchLibraryFile(context.Background(), client, bad, nil); err == nil {
			t.Fatal("accepted", bad)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = fetchLibraryFile(ctx, client, file, nil); err == nil {
		t.Fatal("cancelled download accepted")
	}
	invalid := []byte("<html>server error</html>")
	if _, err = fetchLibraryFile(context.Background(), fontClient(invalid), fontDescriptor(invalid), nil); err == nil {
		t.Fatal("HTML accepted as font")
	}
}
func TestLibraryStoreReuseRepairAndRuntime(t *testing.T) {
	a, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	data := fontFixture(t)
	font := LibraryFont{ID: "test-ui", Kind: "ui-font", Name: "Fixture", File: fontDescriptor(data), LicenseName: "OFL", LicenseText: "original notice", WeightRange: "100 800", Project: "https://example.org"}
	run := func(ctx context.Context) FontDownload {
		t.Helper()
		ctx, cancel := context.WithCancel(ctx)
		job := &FontDownload{ID: randomID(), Status: "downloading", Total: int64(len(data)), cancel: cancel}
		a.runFontDownload(ctx, job, font)
		return *job
	}
	a.fontLibrary.client = fontClient(data)
	job := run(a.ctx)
	if job.Status != "complete" || job.Asset == nil {
		t.Fatal(job)
	}
	first := *job.Asset
	if first.LicenseText != font.LicenseText || first.LibraryID != font.ID || first.WeightRange != font.WeightRange {
		t.Fatal(first)
	}
	if _, err = a.store.PatchAppearance(preferencePatch(t, `{"uiFontId":"`+first.ID+`"}`)); err != nil {
		t.Fatal(err)
	}
	if runtime := a.uiFontRuntime(); runtime.ActiveID != first.ID || runtime.PendingID != "" {
		t.Fatal(runtime)
	}
	// Reuse never needs the website, and does not duplicate assets.
	a.fontLibrary.client = &http.Client{Transport: fontTransport(func(*http.Request) (*http.Response, error) {
		t.Error("cache made network request")
		return nil, context.Canceled
	})}
	if reused := run(a.ctx); reused.Asset == nil || reused.Asset.ID != first.ID || len(a.store.List().Assets) != 1 {
		t.Fatal(reused)
	}
	ctx, cancel := context.WithCancel(a.ctx)
	cancel()
	if stopped := run(ctx); stopped.Status != "cancelled" {
		t.Fatal(stopped)
	}
	// Damaged bytes are never reused. Repair keeps other assets and preferences.
	if err = os.WriteFile(filepath.Join(a.store.dir, "assets", first.ID), []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, ok := a.store.cachedLibraryAsset(font); ok {
		t.Fatal("damaged font reused")
	}
	a.fontLibrary.client = fontClient(data)
	fixed := run(a.ctx)
	if fixed.Status != "complete" || fixed.Asset.ID == first.ID {
		t.Fatal(fixed)
	}
	if a.store.List().Appearance.UIFontID != first.ID {
		t.Fatal("download changed selection")
	}
	if _, err = a.store.PatchAppearance(preferencePatch(t, `{"uiFontId":"`+fixed.Asset.ID+`"}`)); err != nil {
		t.Fatal(err)
	}
	b, err := New(a.store.dir)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if b.uiFontRuntime().ActiveID != fixed.Asset.ID {
		t.Fatal("offline restart lost selection")
	}
	if err = b.store.DeleteAsset(fixed.Asset.ID); err != nil {
		t.Fatal(err)
	}
	if b.uiFontRuntime().ActiveID != defaultUIFontID {
		t.Fatal("deletion did not restore default")
	}
}
func TestFontJobsLimitsAuthAndUnknownIDs(t *testing.T) {
	a, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	a.fontLibrary.client = &http.Client{Transport: fontTransport(func(r *http.Request) (*http.Response, error) { <-r.Context().Done(); return nil, r.Context().Err() })}
	catalog, _ := libraryCatalogOnce()
	var candidates []string
	for _, f := range catalog.Fonts {
		if f.BuiltinID == "" {
			candidates = append(candidates, f.ID)
		}
	}
	if len(candidates) < 3 {
		t.Fatal("catalog incomplete")
	}
	one, err := a.beginFontDownload(candidates[0])
	if err != nil {
		t.Fatal(err)
	}
	again, err := a.beginFontDownload(candidates[0])
	if err != nil || one.ID != again.ID {
		t.Fatal(err, again)
	}
	two, err := a.beginFontDownload(candidates[1])
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.beginFontDownload(candidates[2]); err == nil {
		t.Fatal("unbounded parallel jobs")
	}
	if _, err = a.beginFontDownload("../../outside"); err == nil {
		t.Fatal("unknown ID accepted")
	}
	handler := a.Handler(fstest.MapFS{"index.html": {Data: []byte("fixture")}})
	req := httptest.NewRequest("POST", "/api/font-library/downloads", strings.NewReader(`{"fontId":"`+candidates[2]+`"}`))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code == 200 {
		t.Fatal("unauthenticated mutation")
	}
	for _, id := range []string{one.ID, two.ID} {
		req := httptest.NewRequest("DELETE", "/api/font-library/downloads/"+id, nil)
		req.Header.Set("X-CloudShell-Token", a.Token())
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != 200 {
			t.Fatal(rec.Code, rec.Body.String())
		}
		deadline := time.Now().Add(3 * time.Second)
		for {
			job, _ := a.fontDownload(id)
			if job.Status == "cancelled" {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal(job)
			}
			time.Sleep(time.Millisecond)
		}
	}
}
func TestRemovedFontMigrationPreservesUserPreferences(t *testing.T) {
	for _, id := range []string{"builtin:ui-sarasa", "builtin:ui-wenkai", "builtin:ui-noto-sans", "builtin:ui-noto-serif", "builtin:ui-maple"} {
		var got Appearance
		if err := json.Unmarshal([]byte(`{"uiFontId":"`+id+`","fontId":"builtin:iosevka","uiTextColors":{"light":"#123456"},"fontBold":{"builtin:iosevka":true}}`), &got); err != nil {
			t.Fatal(err)
		}
		if got.UIFontID != defaultUIFontID || got.FontID != "builtin:jetbrains-mono" || got.UITextColors["light"] != "#123456" || !got.FontBold["builtin:iosevka"] {
			t.Fatal(got)
		}
	}
	var got Appearance
	if err := json.Unmarshal([]byte(`{"uiFontId":"user-font","fontId":"user-terminal"}`), &got); err != nil {
		t.Fatal(err)
	}
	if got.UIFontID != "user-font" || got.FontID != "user-terminal" {
		t.Fatal("custom font changed", got)
	}
}

// Explicit deployment smoke test: the production HTTPS client, a temporary
// Store, and only fonts pinned in the embedded catalog. Never updates the app.
func TestFontLibraryLiveDeployment(t *testing.T) {
	if os.Getenv("DENG_FONT_LIVE_VERIFY") != "1" {
		t.Skip("opt in after deploying the pinned website font files")
	}
	a, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	for _, id := range []string{"ui-marker", "shell-iosevka"} {
		t.Run(id, func(t *testing.T) {
			job, err := a.beginFontDownload(id)
			if err != nil {
				t.Fatal(err)
			}
			deadline := time.Now().Add(125 * time.Second)
			for job.Status == "downloading" && time.Now().Before(deadline) {
				time.Sleep(100 * time.Millisecond)
				job, err = a.fontDownload(job.ID)
				if err != nil {
					t.Fatal(err)
				}
			}
			if job.Status != "complete" || job.Asset == nil {
				t.Fatalf("live download: %s %s", job.Status, job.Error)
			}
			font, _ := findLibraryFont(id)
			if job.Asset.LicenseText != font.LicenseText {
				t.Fatal("license mismatch")
			}
			if _, ok := a.store.cachedLibraryAsset(font); !ok {
				t.Fatal("verified font not persisted")
			}
			if font.Kind == "ui-font" {
				a.uiFontMu.Lock()
				available := a.uiFontsAtStartup[job.Asset.ID]
				a.uiFontMu.Unlock()
				if !available {
					t.Fatal("UI font not available immediately")
				}
			}
		})
	}
}
