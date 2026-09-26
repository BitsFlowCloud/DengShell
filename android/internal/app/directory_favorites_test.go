package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
)

func TestDirectoryFavoritesPersistenceIsolationAndLimits(t *testing.T) {
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"a", "b"} {
		if _, err = s.saveProfile(Profile{ID: id, Name: id, Host: "fixture.invalid", User: "root"}, false, true); err != nil {
			t.Fatal(err)
		}
	}
	one, two := strings.Repeat("a", 64), strings.Repeat("b", 64)
	put := func(id, identity, path string) {
		t.Helper()
		if _, e := s.directoryFavorites(id, identity, path, "POST"); e != nil {
			t.Fatal(e)
		}
	}
	put("a", one, "/private/a")
	put("b", one, "/private/b")
	put("a", two, "/new/endpoint")
	before := s.diskDigest
	put("a", one, "/private/a/.")
	if s.diskDigest != before {
		t.Fatal("duplicate rewrote config")
	}
	reopened, err := OpenStore(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	got, err := reopened.directoryFavorites("a", one, "", "GET")
	if err != nil || !slices.Equal(got, []string{"/private/a"}) {
		t.Fatal(got, err)
	}
	pub, _ := json.Marshal(s.List())
	proj, _ := s.syncProjection(false)
	raw, _ := json.Marshal(proj)
	if bytes.Contains(pub, []byte("/private/")) || bytes.Contains(raw, []byte("/private/")) {
		t.Fatal("private favorites leaked")
	}
	if _, err = s.directoryFavorites("a", one, "/private/a", "DELETE"); err != nil {
		t.Fatal(err)
	}
	for _, pair := range [][2]string{{"b", one}, {"a", two}} {
		got, _ = s.directoryFavorites(pair[0], pair[1], "", "GET")
		if len(got) != 1 {
			t.Fatal("other server changed")
		}
	}
	for _, p := range []string{"relative", "/bad\x00", "/" + strings.Repeat("a", 4096), string([]byte{'/', 255})} {
		if _, err = s.directoryFavorites("a", one, p, "POST"); err == nil {
			t.Fatal("invalid accepted")
		}
	}
	for i := 0; i < 100; i++ {
		put("a", one, fmt.Sprintf("/folder/%d", i))
	}
	if _, err = s.directoryFavorites("a", one, "/overflow", "POST"); err == nil {
		t.Fatal("limit ignored")
	}
	// Failed encrypted config writes must not alter the current in-memory list.
	target := filepath.Join(s.dir, EncryptedConfigName)
	data, _ := os.ReadFile(target)
	data[len(data)-1] ^= 1
	if err = os.WriteFile(target, data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = s.directoryFavorites("a", one, "/folder/1", "DELETE"); err == nil {
		t.Fatal("write conflict ignored")
	}
	got, _ = s.directoryFavorites("a", one, "", "GET")
	if len(got) != 100 {
		t.Fatal("failed delete changed state")
	}
}
func TestDirectoryFavoritesConcurrentWritesAndDeletion(t *testing.T) {
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.Save(Profile{Name: "server", Host: "fixture.invalid", User: "root"}, false)
	if err != nil {
		t.Fatal(err)
	}
	identity := strings.Repeat("a", 64)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Go(func() {
			_, e := s.directoryFavorites(p.ID, identity, fmt.Sprintf("/dir/%d", i), "POST")
			if e != nil {
				t.Error(e)
			}
		})
	}
	wg.Wait()
	got, _ := s.directoryFavorites(p.ID, identity, "", "GET")
	if len(got) != 20 {
		t.Fatal("lost concurrent favorite")
	}
	if err = s.Delete(p.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.directoryFavorites(p.ID, identity, "/late", "POST"); err == nil {
		t.Fatal("deleted connection accepted write")
	}
	if err = s.Purge(p.ID); err != nil {
		t.Fatal(err)
	}
	if len(s.config.DirectoryFavorites) != 0 {
		t.Fatal("purge retained favorites")
	}
	if _, err = s.directoryFavorites(p.ID, identity, "/late", "POST"); err == nil {
		t.Fatal("purged favorite resurrected")
	}
}
func TestDirectoryFavoritesAPIValidatesDirectory(t *testing.T) {
	a, s, root, _ := uploadSFTPFixture(t, 0)
	var err error
	a.store, err = OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	p, err := a.store.Save(Profile{Name: "server", Host: "fixture.invalid", User: "root"}, false)
	if err != nil {
		t.Fatal(err)
	}
	s.ProfileID = p.ID
	s.fileWriteIdentity = "isolated endpoint"
	target := filepath.Join(root, "file.txt")
	if err = os.WriteFile(target, []byte("test"), 0600); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	a.registerDirectoryFavoritesHTTP(mux)
	call := func(method, path string) *httptest.ResponseRecorder {
		t.Helper()
		body, _ := json.Marshal(map[string]string{"path": path})
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest(method, "/api/sessions/"+s.ID+"/directory-favorites", bytes.NewReader(body)))
		return w
	}
	for _, p := range []string{target, filepath.Join(root, "missing"), "relative"} {
		if w := call("POST", p); w.Code == 200 {
			t.Fatal("invalid directory accepted", p)
		}
	}
	if w := call("POST", root); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if w := call("GET", ""); w.Code != 200 || strings.Contains(w.Body.String(), "identity") || !strings.Contains(w.Body.String(), root) {
		t.Fatal(w.Body.String())
	}
	if w := call("DELETE", root); w.Code != 200 || !strings.Contains(w.Body.String(), `"paths":[]`) {
		t.Fatal(w.Body.String())
	}
}
