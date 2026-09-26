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

func groupDeleteFixture(t *testing.T) (*App, ServerGroup, Profile, Profile) {
	t.Helper()
	a, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	addGroup := func(name, parent string) ServerGroup {
		g, e := a.store.SaveGroup(ServerGroup{Name: name, ParentID: parent, BackgroundColor: "#183F52"})
		if e != nil {
			t.Fatal(e)
		}
		return g
	}
	root := addGroup("删除目标", "")
	child := addGroup("子分组", root.ID)
	leaf := addGroup("末级", child.ID)
	other := addGroup("保留组", "")
	add := func(name, group string) Profile {
		p, e := a.store.Save(Profile{Name: name, Host: "127.0.0.1", Port: 22, User: "root", Auth: "password", Secret: "saved-secret", GroupID: group}, false)
		if e != nil {
			t.Fatal(e)
		}
		return p
	}
	target := add("删除连接", leaf.ID)
	keep := add("保留连接", other.ID)
	for _, p := range []Profile{target, keep} {
		if _, err = a.store.commandHistory(p.ID, randomID(), "printf "+p.Name, false); err != nil {
			t.Fatal(err)
		}
		if err = a.store.MarkConnected(p.ID, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	return a, root, target, keep
}
func TestGroupTreeDeleteScopePersistenceAndHistory(t *testing.T) {
	a, root, target, keep := groupDeleteFixture(t)
	s := a.store
	trashed, e := s.Save(Profile{Name: "回收站", Host: "localhost", User: "root", GroupID: root.ID}, false)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Delete(trashed.ID); e != nil {
		t.Fatal(e)
	}
	before := s.List()
	plan, e := s.GroupDeletionPlan(root.ID)
	if e != nil {
		t.Fatal(e)
	}
	if plan.Groups != 3 || plan.Servers != 1 || plan.Trashed != 1 {
		t.Fatal(plan)
	}
	closed, cancel := context.WithCancel(context.Background())
	a.sessions["closing"] = &Session{ProfileID: target.ID, cancel: cancel}
	result, e := a.deleteGroupTree(root.ID, plan.Revision, plan.Name)
	if e != nil || len(result) != 2 {
		t.Fatal(result, e)
	}
	select {
	case <-closed.Done():
	default:
		t.Fatal("session not closed")
	}
	if _, e = s.Get(target.ID); e == nil {
		t.Fatal("profile survived")
	}
	if e = s.MarkConnected(target.ID, time.Now()); e == nil {
		t.Fatal("late connect resurrected deleted profile")
	}
	if _, e = a.commandHistory(target.ID, randomID(), "late history", false); e == nil {
		t.Fatal("late flush resurrected deleted history")
	}
	for _, h := range s.List().ConnectionHistory {
		if h.ProfileID == target.ID {
			t.Fatal("connection history survived")
		}
	}
	if s.List().CommandHistory.Revision != before.CommandHistory.Revision {
		t.Fatal("global history changed")
	}
	reopened, e := OpenStore(s.dir)
	if e != nil {
		t.Fatal(e)
	}
	p, e := reopened.Get(keep.ID)
	if e != nil || p.Secret != "saved-secret" {
		t.Fatal("unrelated credentials changed", e)
	}
	if len(reopened.List().GroupNodes) != len(before.GroupNodes)-3 {
		t.Fatal("subtree deletion not persisted")
	}
	if _, ok := reopened.config.Appearance.Layout["dengshell.history."+target.ID]; ok {
		t.Fatal("per-profile history survived")
	}
	if _, ok := reopened.config.Appearance.Layout["dengshell.history."+keep.ID]; !ok {
		t.Fatal("unrelated history removed")
	}
}
func TestGroupTreeDeleteRejectsStaleScopeAndWrongName(t *testing.T) {
	a, root, target, _ := groupDeleteFixture(t)
	s := a.store
	plan, _ := s.GroupDeletionPlan(root.ID)
	if _, e := a.deleteGroupTree(root.ID, plan.Revision, "wrong"); e == nil {
		t.Fatal("wrong name accepted")
	}
	target.Secret = "new-secret"
	if _, e := s.Save(target, false); e != nil {
		t.Fatal(e)
	}
	if _, e := a.deleteGroupTree(root.ID, plan.Revision, plan.Name); e == nil {
		t.Fatal("changed credentials accepted")
	}
	plan, _ = s.GroupDeletionPlan(root.ID)
	if _, e := s.SaveGroup(ServerGroup{Name: "new child", ParentID: root.ID}); e != nil {
		t.Fatal(e)
	}
	if _, e := a.deleteGroupTree(root.ID, plan.Revision, plan.Name); e == nil {
		t.Fatal("new child accepted")
	}
	if _, e := s.Get(target.ID); e != nil {
		t.Fatal("failed deletion changed store")
	}
}
func TestGroupTreeDeleteWriteFailureRollsBack(t *testing.T) {
	a, root, target, _ := groupDeleteFixture(t)
	s := a.store
	plan, _ := s.GroupDeletionPlan(root.ID)
	before, _ := json.Marshal(s.config)
	oldDir := s.dir
	blocked := filepath.Join(t.TempDir(), "file")
	if e := os.WriteFile(blocked, []byte("not a directory"), 0600); e != nil {
		t.Fatal(e)
	}
	s.dir = blocked
	defer func() { s.dir = oldDir }()
	if _, e := a.deleteGroupTree(root.ID, plan.Revision, plan.Name); e == nil {
		t.Fatal("write failure ignored")
	}
	after, _ := json.Marshal(s.config)
	if !bytes.Equal(before, after) || s.historyState[target.ID] == nil {
		t.Fatal("failed write mutated memory")
	}
}
func TestGroupTreeDeleteHTTPRequiresThreeConfirmations(t *testing.T) {
	a, root, _, _ := groupDeleteFixture(t)
	mux := http.NewServeMux()
	a.registerConnectionManagementHTTP(mux)
	plan, _ := a.store.GroupDeletionPlan(root.ID)
	for _, count := range []int{0, 1, 2} {
		data, _ := json.Marshal(map[string]any{"revision": plan.Revision, "name": plan.Name, "confirmations": count})
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/group-nodes/"+root.ID+"/deletion", bytes.NewReader(data)))
		if w.Code < 400 {
			t.Fatal("missing confirmation accepted", count, w.Body.String())
		}
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", "/api/group-nodes/"+root.ID+"/deletion", nil))
	if w.Code != 200 || strings.Contains(w.Body.String(), "saved-secret") {
		t.Fatal("bad public plan")
	}
}
func TestGroupColorsAndPurgeCleanup(t *testing.T) {
	a, root, target, _ := groupDeleteFixture(t)
	s := a.store
	root.BackgroundColor = "url(evil)"
	if _, e := s.SaveGroup(root); e == nil {
		t.Fatal("invalid color accepted")
	}
	root.BackgroundColor = "#00aAfF"
	g, e := s.SaveGroup(root)
	if e != nil || g.BackgroundColor != root.BackgroundColor {
		t.Fatal("color lost", e)
	}
	reopened, e := OpenStore(s.dir)
	if e != nil {
		t.Fatal(e)
	}
	plan, e := reopened.GroupDeletionPlan(root.ID)
	if e != nil || plan.Name != root.Name {
		t.Fatal(e)
	}
	if e = s.Delete(target.ID); e != nil {
		t.Fatal(e)
	}
	if e = s.Purge(target.ID); e != nil {
		t.Fatal(e)
	}
	if s.historyState[target.ID] != nil {
		t.Fatal("history state remains")
	}
	if _, e = s.commandHistory(target.ID, randomID(), "late flush", false); e == nil {
		t.Fatal("purged profile history accepted")
	}
}
