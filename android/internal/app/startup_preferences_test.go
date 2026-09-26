package app

import (
	"encoding/json"
	"testing"
)

func TestStartupAnimationDefaultsAndExplicitOptOutSurviveRestart(t *testing.T) {
	for _, scenario := range []struct {
		name string
		json string
		want bool
	}{
		{"old configuration", `{"fontId":"builtin:fira-code"}`, true},
		{"enabled", `{"startupAnimation":true}`, true},
		{"disabled", `{"startupAnimation":false}`, false},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			var value Appearance
			if err := json.Unmarshal([]byte(scenario.json), &value); err != nil {
				t.Fatal(err)
			}
			if value.StartupAnimation != scenario.want {
				t.Fatalf("startupAnimation=%v, want %v", value.StartupAnimation, scenario.want)
			}
			store, err := OpenStore(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			if !store.List().Appearance.StartupAnimation {
				t.Fatal("new installations should show the startup animation")
			}
			if _, err := store.SaveAppearance(value); err != nil {
				t.Fatal(err)
			}
			reopened, err := OpenStore(store.dir)
			if err != nil {
				t.Fatal(err)
			}
			if reopened.List().Appearance.StartupAnimation != scenario.want {
				t.Fatal("saved startup preference changed on restart")
			}
		})
	}
}
