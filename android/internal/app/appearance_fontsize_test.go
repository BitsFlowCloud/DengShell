package app

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
)

func TestTerminalHalfPixelFontSizePersistence(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, size := range []float64{8, 8.5, 14, 14.5, 23.5, 39.5, 40} {
		value := defaultAppearance()
		value.TerminalFontSize = size
		if _, err := s.SaveAppearance(value); err != nil {
			t.Fatalf("size %v: %v", size, err)
		}
		reopened, err := OpenStore(dir)
		if err != nil {
			t.Fatal(err)
		}
		if got := reopened.List().Appearance.TerminalFontSize; got != size {
			t.Fatalf("reopened %v, want %v", got, size)
		}
	}
	for _, size := range []float64{0, 7.5, 40.5, 14.25, 14.75, math.NaN(), math.Inf(1), math.Inf(-1)} {
		value := defaultAppearance()
		value.TerminalFontSize = size
		if _, err := s.SaveAppearance(value); err == nil || !strings.Contains(err.Error(), "0.5") {
			t.Fatalf("invalid size %v accepted or unexplained: %v", size, err)
		}
		if got := s.List().Appearance.TerminalFontSize; got != 40 {
			t.Fatalf("invalid save changed existing size to %v", got)
		}
	}
}

func TestTerminalHalfPixelFontSizeJSONCompatibility(t *testing.T) {
	for _, test := range []struct {
		data string
		size float64
	}{
		{`{}`, 14}, {`{"terminalFontSize":14}`, 14}, {`{"terminalFontSize":14.5}`, 14.5},
	} {
		var value Appearance
		if err := json.Unmarshal([]byte(test.data), &value); err != nil {
			t.Fatal(err)
		}
		if value.TerminalFontSize != test.size {
			t.Fatalf("legacy/fractional JSON gave %v, want %v", value.TerminalFontSize, test.size)
		}
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		var roundtrip Appearance
		if err := json.Unmarshal(data, &roundtrip); err != nil || roundtrip.TerminalFontSize != test.size {
			t.Fatalf("roundtrip changed font size: %s / %v", data, err)
		}
	}
}
