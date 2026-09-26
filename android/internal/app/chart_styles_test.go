package app

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
)

func TestChartStylesIndependentPatchesAndRestart(t *testing.T) {
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	patches := []string{
		`{"chartStyles":{"upload":{"color":"#ff1234","width":4,"dashed":true}}}`,
		`{"chartStyles":{"download":{"color":"#1234ff","width":1,"dashed":false}}}`,
		`{"chartStyles":{"latency":{"color":"#12ff34","width":2.5,"dashed":false}}}`,
	}
	var wg sync.WaitGroup
	for _, raw := range patches {
		patch := preferencePatch(t, raw)
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := s.PatchAppearance(patch); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	reopened, err := OpenStore(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	styles := reopened.List().Appearance.ChartStyles
	if len(styles) != 3 || styles["upload"].Width != 4 || !styles["upload"].Dashed || styles["download"].Dashed || styles["latency"].Color != "#12ff34" {
		t.Fatal("styles not independent or not persisted", styles)
	}
	styles["upload"] = ChartLineStyle{}
	if reopened.List().Appearance.ChartStyles["upload"].Width != 4 {
		t.Fatal("public map aliases configuration")
	}
	if _, err := reopened.PatchAppearance(preferencePatch(t, `{"chartStyles":{"upload":null}}`)); err != nil {
		t.Fatal(err)
	}
	if len(reopened.List().Appearance.ChartStyles) != 2 {
		t.Fatal("reset one line removed others")
	}
}

func TestChartStylesValidationAndFailureRollback(t *testing.T) {
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	before := s.List().Appearance
	for _, raw := range []string{
		`{"chartStyles":null}`,
		`{"chartStyles":{"other":{"width":2}}}`,
		`{"chartStyles":{"upload":{"width":0}}}`,
		`{"chartStyles":{"upload":{"width":7}}}`,
		`{"chartStyles":{"upload":{"width":2,"color":"url(https://invalid/)"}}}`,
		`{"chartStyles":{"latency":{"width":2,"dashed":true}}}`,
	} {
		if _, err := s.PatchAppearance(preferencePatch(t, raw)); err == nil {
			t.Fatal("accepted invalid style", raw)
		}
		if !reflect.DeepEqual(before, s.List().Appearance) {
			t.Fatal("invalid style changed settings")
		}
	}
	if err := os.WriteFile(filepath.Join(s.dir, ConfigKeyName), bytes.Repeat([]byte{1}, 32), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PatchAppearance(preferencePatch(t, `{"chartStyles":{"upload":{"width":3,"color":"#123456"}}}`)); err == nil {
		t.Fatal("ignored write failure")
	}
	if !reflect.DeepEqual(before, s.List().Appearance) {
		t.Fatal("failed write changed settings")
	}
}
