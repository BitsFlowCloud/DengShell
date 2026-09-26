package app

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
)

func TestCommandOrderingPersistenceAndReturnOption(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"日常", "部署", "空组"} {
		if _, err := s.SaveCommandGroup(name); err != nil {
			t.Fatal(err)
		}
	}
	items := []QuickCommand{}
	for _, c := range []QuickCommand{{Name: "A", Group: "日常", Body: "docker logs [p#1 容器]", Color: "blue"}, {Name: "B", Group: "日常", Body: "pwd", Color: "green", AppendCR: true}, {Name: "C", Group: "部署", Body: "echo C", Color: "rose"}} {
		item, err := s.SaveCommand(c)
		if err != nil {
			t.Fatal(err)
		}
		items = append(items, item)
	}
	if err := s.MoveCommand(items[1].ID, items[0].ID, "", false); err != nil {
		t.Fatal(err)
	}
	// A concurrent edit is preserved by a move submitted from an older UI view.
	items[0].Body = "printf updated"
	if _, err := s.SaveCommand(items[0]); err != nil {
		t.Fatal(err)
	}
	if err := s.MoveCommand(items[0].ID, "", "部署", false); err != nil {
		t.Fatal(err)
	}
	if err := s.MoveCommandGroup("空组", "日常", false); err != nil {
		t.Fatal(err)
	}
	if err := s.RenameCommandGroup("部署", "生产"); err != nil {
		t.Fatal(err)
	}
	s, err = OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	c := s.List()
	if !slices.Equal(c.CommandGroups, []string{"空组", "日常", "生产"}) {
		t.Fatal(c.CommandGroups)
	}
	if c.Commands[0].ID != items[1].ID || c.Commands[1].ID != items[2].ID || c.Commands[2].ID != items[0].ID || c.Commands[2].Group != "生产" || c.Commands[2].Body != "printf updated" {
		t.Fatal(c.Commands)
	}
	if !c.Commands[0].AppendCR || c.Commands[2].AppendCR {
		t.Fatal("return policy not preserved")
	}
	var legacy QuickCommand
	if err := json.Unmarshal([]byte(`{"name":"legacy","body":"pwd"}`), &legacy); err != nil || legacy.AppendCR {
		t.Fatal("legacy commands must default to no automatic return")
	}
}

func TestCommandMovesRejectStaleTargetsAndRollback(t *testing.T) {
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a, _ := s.SaveCommand(QuickCommand{Name: "A", Group: "A", Body: "pwd", Color: "blue"})
	b, _ := s.SaveCommand(QuickCommand{Name: "B", Group: "B", Body: "id", Color: "green"})
	before := s.List()
	for _, operation := range []func() error{
		func() error { return s.MoveCommand("missing", b.ID, "", false) },
		func() error { return s.MoveCommand(a.ID, "missing", "", false) },
		func() error { return s.MoveCommand(a.ID, b.ID, "missing", false) },
		func() error { return s.MoveCommandGroup("A", "missing", false) },
		func() error { return s.RenameCommandGroup("missing", "B") },
		func() error { return s.RenameCommandGroup("A", "B") },
	} {
		if err := operation(); err == nil {
			t.Fatal("invalid move accepted")
		}
	}
	if !reflect.DeepEqual(before, s.List()) {
		t.Fatal("invalid move changed store")
	}
	if err := os.WriteFile(filepath.Join(s.dir, ConfigKeyName), bytes.Repeat([]byte{1}, 32), 0600); err != nil {
		t.Fatal(err)
	}
	for _, operation := range []func() error{
		func() error { return s.MoveCommand(a.ID, b.ID, "B", true) },
		func() error { return s.MoveCommandGroup("B", "A", false) },
		func() error { return s.RenameCommandGroup("A", "new") },
	} {
		if err := operation(); err == nil {
			t.Fatal("write failure ignored")
		}
		if !reflect.DeepEqual(before, s.List()) {
			t.Fatal("failed write changed store")
		}
	}
}

func TestQuickCommandRejectsTerminalControls(t *testing.T) {
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{"pwd\x1b[201~\rwhoami", "pwd\x00", "pwd\x08", "pwd\u009b"} {
		if _, err := s.SaveCommand(QuickCommand{Name: "bad", Body: body, Color: "blue"}); err == nil {
			t.Fatal("terminal control accepted")
		}
	}
	if _, err := s.SaveCommand(QuickCommand{Name: "valid", Body: "echo 中文\r\n\tpwd", Color: "blue"}); err != nil {
		t.Fatal(err)
	}
}
