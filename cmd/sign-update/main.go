// Offline publisher utility. The private key must never enter a web directory,
// source archive, or application build. Only its public key belongs in the app.
package main

import (
	"cloudshell/internal/updatetrust"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	key := flag.String("key", "", "private key file outside web/source directories")
	generate := flag.Bool("generate", false, "create a new private key without overwriting; print public key only")
	in := flag.String("in", "", "input update JSON")
	out := flag.String("out", "", "output signed update JSON")
	days := flag.Int("days", 90, "metadata validity in days (1..180)")
	flag.Parse()
	if *key == "" {
		return errors.New("-key is required")
	}
	if *generate {
		public, private, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return err
		}
		if err = os.MkdirAll(filepath.Dir(*key), 0700); err != nil {
			return err
		}
		f, err := os.OpenFile(*key, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return err
		}
		_, err = f.Write(private.Seed())
		if err == nil {
			err = f.Sync()
		}
		closeErr := f.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
		return json.NewEncoder(os.Stdout).Encode(map[string]string{"keyID": updatetrust.KeyID(public), "publicKey": base64.StdEncoding.EncodeToString(public)})
	}
	if *in == "" || *out == "" || *days < 1 || *days > 180 {
		return errors.New("-in, -out and validity of 1..180 days are required")
	}
	info, err := os.Lstat(*key)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return errors.New("private key must be a regular private file (chmod 600)")
	}
	seed, err := os.ReadFile(*key)
	if err != nil {
		return err
	}
	if len(seed) != ed25519.SeedSize {
		return errors.New("invalid private seed size")
	}
	f, err := os.Open(*in)
	if err != nil {
		return err
	}
	defer f.Close()
	decoder := json.NewDecoder(io.LimitReader(f, 65537))
	decoder.DisallowUnknownFields()
	var d updatetrust.Descriptor
	if err = decoder.Decode(&d); err != nil {
		return err
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return errors.New("trailing manifest data")
	}
	if d.SchemaVersion != 2 || d.Build == 0 || d.Product != "DengShell" || (d.Platform != "windows-amd64" && d.Platform != "linux-amd64") {
		return errors.New("invalid update identity")
	}
	for _, hash := range []string{d.SHA256, d.ExecutableSHA256} {
		b, err := hex.DecodeString(hash)
		if err != nil || len(b) != 32 {
			return errors.New("invalid SHA-256 in manifest")
		}
	}
	if d.Size <= 0 || d.Size > 1<<30 || len(d.Notes) > 16000 || len(d.Version) == 0 || len(d.Version) > 64 || (d.Platform == "windows-amd64" && d.SHA256 != d.ExecutableSHA256) {
		return errors.New("invalid manifest size, version or executable identity")
	}
	d, err = updatetrust.Sign(d, ed25519.NewKeyFromSeed(seed), time.Now(), time.Duration(*days)*24*time.Hour)
	if err != nil {
		return err
	}
	if err = updatetrust.Verify(d, updatetrust.PublisherKeys(), time.Now()); err != nil {
		return errors.New("signing key is not trusted by this source tree; update the compiled public key before publishing")
	}
	b, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return err
	}
	if len(b)+1 > 65536 {
		return errors.New("signed manifest exceeds the client's 64 KiB limit")
	}
	// Publish the completed descriptor atomically; never truncate an existing
	// manifest while a reader could observe it.
	tmp, err := os.CreateTemp(filepath.Dir(*out), ".signed-update-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err = tmp.Write(append(b, '\n')); err == nil {
		err = tmp.Sync()
	}
	closeErr := tmp.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err = os.Chmod(tmp.Name(), 0644); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), *out)
}
