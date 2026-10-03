package main

import (
	"io/fs"
	"path"
	"strings"
	"testing"
)

// The portable client embeds web recursively. Local credentials must never
// become downloadable assets or travel inside a compiled client.
func TestEmbeddedWebExcludesRuntimeCredentials(t *testing.T) {
	err := fs.WalkDir(assets, "web", func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		base := strings.ToLower(path.Base(name))
		private := base == "config.json" || base == "dengshell.config.json" || base == "cloudshell.config.json" || base == "id_rsa" || base == "id_ed25519" || base == ".env" || strings.HasPrefix(base, ".env.") && base != ".env.example"
		for _, suffix := range []string{".enc", ".key", ".pem", ".keystore", ".jks"} {
			private = private || strings.HasSuffix(base, suffix) || strings.Contains(base, suffix+".") || strings.HasSuffix(base, suffix+"~")
		}
		if private {
			t.Errorf("runtime credential file must not be embedded: %s", name)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
