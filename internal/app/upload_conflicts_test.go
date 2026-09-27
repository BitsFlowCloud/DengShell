package app

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func conflictRequest(t *testing.T, s *Session, handler http.HandlerFunc, body any) *httptest.ResponseRecorder {
	t.Helper()
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(data))
	r.SetPathValue("id", s.ID)
	w := httptest.NewRecorder()
	handler(w, r)
	return w
}
func writeConflictFixture(t *testing.T, name, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
}
func readConflictFixture(t *testing.T, name, want string) {
	t.Helper()
	data, err := os.ReadFile(name)
	if err != nil || string(data) != want {
		t.Fatalf("%s: got %q (%v), want %q", name, data, err, want)
	}
}
func awaitConflictUploads(t *testing.T, a *App) []Transfer {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		var result []Transfer
		finished := true
		a.mu.Lock()
		for _, task := range a.transfers {
			result = append(result, *task)
			if task.FinishedAt.IsZero() {
				finished = false
			}
		}
		a.mu.Unlock()
		if finished {
			return result
		}
		if time.Now().After(deadline) {
			t.Fatal("uploads did not finish")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestUploadConflictPreflightIsReadOnlyAndAggregates(t *testing.T) {
	a, s, root, _ := uploadSFTPFixture(t, 0)
	local := filepath.Join(t.TempDir(), "batch")
	for _, f := range []string{"a.txt", "nested/二.txt", "new.txt", "empty/.keep"} {
		writeConflictFixture(t, filepath.Join(local, f), "new")
	}
	if err := os.Remove(filepath.Join(local, "empty/.keep")); err != nil {
		t.Fatal(err)
	}
	remote := filepath.Join(root, "batch")
	for _, f := range []string{"a.txt", "nested/二.txt"} {
		writeConflictFixture(t, filepath.Join(remote, f), "old")
	}
	w := conflictRequest(t, s, a.checkUploads, map[string]any{"paths": []string{local}, "directory": root})
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var got struct {
		Conflicts []uploadConflict
		Total     int
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Conflicts) != 2 || got.Total != 6 {
		t.Fatalf("unexpected conflicts: %+v", got)
	}
	for _, c := range got.Conflicts {
		if !c.CanOverwrite {
			t.Fatal("regular file not overwritable")
		}
		readConflictFixture(t, c.Path, "old")
	}
	if len(a.transfers) != 0 {
		t.Fatal("preflight created transfers")
	}
	if _, err := os.Stat(filepath.Join(remote, "empty")); !os.IsNotExist(err) {
		t.Fatal("preflight created directory")
	}
	// Browser selection receives the same combined answer.
	w = conflictRequest(t, s, a.checkUploads, map[string]any{"targets": []string{filepath.Join(remote, "a.txt"), filepath.Join(remote, "nested/二.txt"), filepath.Join(remote, "new.txt")}})
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Conflicts) != 2 || got.Total != 3 {
		t.Fatalf("browser conflicts: %+v", got)
	}
}

func TestNativeUploadConflictDecisionIsScopedAndSkipPreservesFiles(t *testing.T) {
	for _, decision := range []string{"overwrite", "skip", "all-skip"} {
		t.Run(decision, func(t *testing.T) {
			a, s, root, _ := uploadSFTPFixture(t, 0)
			local := filepath.Join(t.TempDir(), "batch")
			remote := filepath.Join(root, "batch")
			for _, f := range []string{"a.txt", "b.txt", "fresh.txt"} {
				writeConflictFixture(t, filepath.Join(local, f), "new")
			}
			for _, f := range []string{"a.txt", "b.txt"} {
				writeConflictFixture(t, filepath.Join(remote, f), "old")
			}
			input := map[string]any{"paths": []string{local}, "directory": root}
			switch decision {
			case "overwrite":
				input["overwriteTargets"] = []string{filepath.Join(remote, "a.txt")}
			case "skip":
				input["skipTargets"] = []string{filepath.Join(remote, "a.txt"), filepath.Join(remote, "b.txt")}
			case "all-skip":
				input["skipTargets"] = []string{remote}
			}
			w := conflictRequest(t, s, a.uploadLocal, input)
			if w.Code != 200 {
				t.Fatal(w.Body.String())
			}
			tasks := awaitConflictUploads(t, a)
			if decision == "all-skip" {
				if len(tasks) != 0 {
					t.Fatal("skipped directory started uploads")
				}
				return
			}
			readConflictFixture(t, filepath.Join(remote, "fresh.txt"), "new")
			readConflictFixture(t, filepath.Join(remote, "b.txt"), "old")
			if decision == "overwrite" {
				readConflictFixture(t, filepath.Join(remote, "a.txt"), "new")
				var failed int
				for _, task := range tasks {
					if task.Status == "failed" && strings.Contains(task.Error, "已存在") {
						failed++
					}
				}
				if failed != 1 {
					t.Fatalf("unapproved conflict must remain protected: %+v", tasks)
				}
			} else {
				readConflictFixture(t, filepath.Join(remote, "a.txt"), "old")
				if len(tasks) != 1 || tasks[0].Status != "done" {
					t.Fatalf("skip uploaded conflicting files: %+v", tasks)
				}
			}
		})
	}
}

func TestUploadConflictPreflightRejectsInvalidAndProtectsNonRegular(t *testing.T) {
	a, s, root, _ := uploadSFTPFixture(t, 0)
	file := filepath.Join(root, "file")
	writeConflictFixture(t, file, "original")
	folder := filepath.Join(root, "folder")
	if err := os.Mkdir(folder, 0700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(file, link); err != nil {
		t.Fatal(err)
	}
	for _, targets := range [][]string{{"relative"}, {file, file}, make([]string, maxUploadItems+1)} {
		w := conflictRequest(t, s, a.checkUploads, map[string]any{"targets": targets})
		if w.Code == 200 {
			t.Fatalf("accepted invalid request: %v", targets[:min(2, len(targets))])
		}
	}
	w := conflictRequest(t, s, a.checkUploads, map[string]any{"targets": []string{folder, link}})
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var result struct{ Conflicts []uploadConflict }
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Conflicts) != 2 {
		t.Fatal(w.Body.String())
	}
	for _, c := range result.Conflicts {
		if c.CanOverwrite {
			t.Fatal("non-regular file overwrite offered")
		}
	}
	readConflictFixture(t, file, "original")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := checkUploadConflicts(ctx, s, []uploadItem{{target: file}}); err == nil {
		t.Fatal("ignored cancellation")
	}
}

func TestUploadFolderCollisionIsReportedOnceAndSkippedWithChildren(t *testing.T) {
	a, s, root, _ := uploadSFTPFixture(t, 0)
	local := filepath.Join(t.TempDir(), "folder")
	writeConflictFixture(t, filepath.Join(local, "sub/file.txt"), "new")
	target := filepath.Join(root, "folder")
	writeConflictFixture(t, target, "keep-file")
	input := map[string]any{"paths": []string{local}, "directory": root}
	w := conflictRequest(t, s, a.checkUploads, input)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var got struct{ Conflicts []uploadConflict }
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Conflicts) != 1 || got.Conflicts[0].Path != target || got.Conflicts[0].CanOverwrite {
		t.Fatalf("folder collision: %+v", got)
	}
	input["skipTargets"] = []string{target}
	w = conflictRequest(t, s, a.uploadLocal, input)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if len(awaitConflictUploads(t, a)) != 0 {
		t.Fatal("skipped subtree uploaded")
	}
	readConflictFixture(t, target, "keep-file")
}
