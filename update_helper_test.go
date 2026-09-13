package main

import (
	"cloudshell/internal/app"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestUpdateReplacementRetainsPreviousAndRollsBack(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "DengShell.exe")
	staged := filepath.Join(dir, "up.exe")
	os.WriteFile(target, []byte("old executable"), 0700)
	os.WriteFile(staged, []byte("new executable"), 0700)
	old, _ := app.FileSHA256(target)
	next, _ := app.FileSHA256(staged)
	p := updatePlan{Target: target, Staged: staged, OldSHA256: old, ExecutableSHA256: next}
	if _, e := replaceUpdateBinary(p); e != nil {
		t.Fatal(e)
	}
	if b, _ := os.ReadFile(target); string(b) != "new executable" {
		t.Fatal("target not replaced")
	}
	if b, _ := os.ReadFile(updateBackupPath(p)); string(b) != "old executable" {
		t.Fatal("previous program lost")
	}
	if e := restoreUpdateBinary(p); e != nil {
		t.Fatal(e)
	}
	if b, _ := os.ReadFile(target); string(b) != "old executable" {
		t.Fatal("rollback did not restore")
	}
}

func TestUpdateReceiptFailureAbortsAndLaunchFailureRestoresReceipt(t *testing.T) {
	for _, mode := range []string{"unwritable", "launch-failed", "launch-ok"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			file := filepath.Join(dir, "update-receipt.json")
			previous := []byte(`{"sha256":"previous"}`)
			if mode == "unwritable" {
				os.Mkdir(file, 0700)
			} else {
				os.WriteFile(file, previous, 0600)
			}
			called := false
			err := startUpdateWithReceipt(updatePlan{Build: app.ApplicationBuild + 1, Version: app.ApplicationVersion, ConfigDir: dir, PackageSHA256: "next", ExecutableSHA256: "binary"}, func() error {
				called = true
				if mode == "launch-failed" {
					return errors.New("fixture launch failed")
				}
				return nil
			})
			if mode == "unwritable" {
				if called || err == nil {
					t.Fatal("unrecorded build was launched")
				}
				return
			}
			if !called {
				t.Fatal("writable receipt did not launch")
			}
			if mode == "launch-failed" {
				if err == nil {
					t.Fatal("launch failure lost")
				}
				if data, _ := os.ReadFile(file); string(data) != string(previous) {
					t.Fatal("failed launch retained false success receipt")
				}
			} else if err != nil {
				t.Fatal(err)
			} else {
				var receipt app.UpdateReceipt
				data, _ := os.ReadFile(file)
				if json.Unmarshal(data, &receipt) != nil || receipt.Build != app.ApplicationBuild+1 {
					t.Fatal("installed high-water mark missing")
				}
			}
		})
	}
}
func TestUpdateReplacementRejectsChangedTargetAndCorruptStaging(t *testing.T) {
	for _, corruptTarget := range []bool{false, true} {
		dir := t.TempDir()
		target := filepath.Join(dir, "program")
		staged := filepath.Join(dir, "download")
		os.WriteFile(target, []byte("original"), 0700)
		os.WriteFile(staged, []byte("next"), 0700)
		old, _ := app.FileSHA256(target)
		next, _ := app.FileSHA256(staged)
		p := updatePlan{Target: target, Staged: staged, OldSHA256: old, ExecutableSHA256: next}
		if corruptTarget {
			os.WriteFile(target, []byte("changed externally"), 0700)
		} else {
			os.WriteFile(staged, []byte("corrupt"), 0700)
		}
		before, _ := os.ReadFile(target)
		if _, e := replaceUpdateBinary(p); e == nil {
			t.Fatal("changed file accepted")
		}
		after, _ := os.ReadFile(target)
		if string(before) != string(after) {
			t.Fatal("rejected update modified existing program")
		}
	}
}
func TestUpdatePlanRejectsUnverifiedFileAndRelativePaths(t *testing.T) {
	dir := t.TempDir()
	staged := filepath.Join(dir, "up.deb")
	os.WriteFile(staged, []byte("fixture"), 0600)
	hash, _ := app.FileSHA256(staged)
	p := updatePlan{Schema: 2, Build: app.ApplicationBuild + 1, Version: app.ApplicationVersion, ParentPID: os.Getpid() + 100, Target: filepath.Join(dir, "program"), Staged: staged, ConfigDir: dir, OldSHA256: hash, PackageSHA256: hash, ExecutableSHA256: hash, Platform: runtime.GOOS + "-" + runtime.GOARCH}
	file := filepath.Join(dir, "plan.json")
	save := func() { b, _ := json.Marshal(p); os.WriteFile(file, b, 0600) }
	save()
	if _, e := readUpdatePlan(file); e != nil {
		t.Fatal(e)
	}
	receipt, _ := json.Marshal(app.UpdateReceipt{Build: p.Build, Version: p.Version})
	if err := app.WriteUpdateReceipt(dir, receipt); err != nil {
		t.Fatal(err)
	}
	if _, err := readUpdatePlan(file); err == nil {
		t.Fatal("staged plan remained installable after a newer installation")
	}
	if err := os.Remove(filepath.Join(dir, "update-receipt.json")); err != nil {
		t.Fatal(err)
	}
	p.ConfigDir = "relative"
	save()
	if _, e := readUpdatePlan(file); e == nil {
		t.Fatal("relative path accepted")
	}
	p.ConfigDir = dir
	save()
	os.WriteFile(staged, []byte("changed"), 0600)
	if _, e := readUpdatePlan(file); e == nil {
		t.Fatal("modified download accepted")
	}
}
