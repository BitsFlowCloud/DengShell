//go:build desktop

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDetachedCacheLeaseProtectsActiveAndSweepRemovesExited(t *testing.T) {
	config := t.TempDir()
	nonce := strings.Repeat("a", 32)
	release, err := acquireDetachedWebviewCache(config, nonce)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(config, "window-cache", nonce)
	cache := filepath.Join(dir, "webview", "Default")
	if err = os.MkdirAll(cache, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(cache, "CacheData"), []byte("cache"), 0600); err != nil {
		t.Fatal(err)
	}
	sweepDetachedWebviewCaches(config)
	if _, err = os.Stat(cache); err != nil {
		t.Fatal("active cache was removed", err)
	}
	if _, err = acquireDetachedWebviewCache(config, nonce); err == nil {
		t.Fatal("active nonce was reused")
	}
	release()
	sweepDetachedWebviewCaches(config)
	if _, err = os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("exited cache remains", err)
	}
}

func TestDetachedCacheSweepPreservesUnknownAndLinkedUserData(t *testing.T) {
	config := t.TempDir()
	root := filepath.Join(config, "window-cache")
	nonce := strings.Repeat("b", 32)
	dir := filepath.Join(root, nonce)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	user := filepath.Join(config, "custom-font.ttf")
	if err := os.WriteFile(user, []byte("user asset"), 0600); err != nil {
		t.Fatal(err)
	}
	sweepDetachedWebviewCaches(config)
	if _, err := os.Stat(dir); err != nil {
		t.Fatal("unmarked legacy cache was removed")
	}
	linked := filepath.Join(root, strings.Repeat("c", 32))
	if err := os.Symlink(config, linked); err == nil {
		sweepDetachedWebviewCaches(config)
		if _, err := os.Lstat(linked); err != nil {
			t.Fatal("linked directory was touched")
		}
	}
	if content, err := os.ReadFile(user); err != nil || string(content) != "user asset" {
		t.Fatal("user asset changed")
	}
}

func TestDetachedCacheRejectsTraversalAndLinkedRoot(t *testing.T) {
	config := t.TempDir()
	if _, err := acquireDetachedWebviewCache(config, "../custom-fonts"); err == nil {
		t.Fatal("traversal accepted")
	}
	other := t.TempDir()
	_ = os.Remove(filepath.Join(config, "window-cache"))
	if err := os.Symlink(other, filepath.Join(config, "window-cache")); err != nil {
		t.Skip("symlinks unavailable")
	}
	if _, err := acquireDetachedWebviewCache(config, strings.Repeat("d", 32)); err == nil {
		t.Fatal("linked root accepted")
	}
	entries, err := os.ReadDir(other)
	if err != nil || len(entries) != 0 {
		t.Fatal("linked target modified")
	}
}
