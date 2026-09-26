package app

import (
	"errors"
	"sort"
)

const defaultUIFontID = "builtin:ui-ibm-plex-sans-sc"

func builtinUIFont(id string) bool {
	return id == defaultUIFontID
}

func retiredBuiltinUIFont(id string) bool {
	switch id {
	case "builtin:ui-noto-sans", "builtin:ui-noto-serif", "builtin:ui-sarasa", "builtin:ui-wenkai", "builtin:ui-maple":
		return true
	}
	return false
}

func builtinTerminalFont(id string) bool {
	switch id {
	case "builtin:jetbrains-mono", "builtin:fira-code", "builtin:source-code-pro", "builtin:ibm-plex-mono", "builtin:maple-mono-cn":
		return true
	}
	return false
}

func validateUIAppearance(value Appearance, assets []ManagedAsset) error {
	if !builtinUIFont(value.UIFontID) {
		found := false
		for _, asset := range assets {
			if asset.ID == value.UIFontID && asset.Kind == "ui-font" {
				found = true
				break
			}
		}
		if !found {
			return errors.New("所选界面字体不存在，请重新选择")
		}
	}
	for theme, color := range value.UITextColors {
		if (theme != "light" && theme != "dark") || !validTerminalColor(color) {
			return errors.New("界面文字颜色只支持 light、dark 的 #RRGGBB 值")
		}
	}
	return nil
}

type uiFontState struct {
	AvailableIDs []string `json:"availableIds"`
	ActiveID     string   `json:"activeId"`
	PendingID    string   `json:"pendingId,omitempty"`
}

// Registration belongs to the backend process. Manual imports still require
// restart. Verified catalog downloads can be explicitly registered at runtime.
// Refreshing a WebView or opening a detached window cannot bypass this boundary.
func (a *App) initializeUIFonts() {
	a.uiFontsAtStartup = map[string]bool{}
	for _, asset := range a.store.List().Assets {
		if asset.Kind == "ui-font" {
			a.uiFontsAtStartup[asset.ID] = true
		}
	}
	a.activeUIFontID = defaultUIFontID
	a.uiFontRuntime()
}

func (a *App) uiFontRuntime() uiFontState {
	a.uiFontMu.Lock()
	defer a.uiFontMu.Unlock()
	config := a.store.List()
	available := map[string]bool{}
	state := uiFontState{AvailableIDs: []string{}}
	for _, asset := range config.Assets {
		if asset.Kind == "ui-font" && a.uiFontsAtStartup[asset.ID] {
			available[asset.ID] = true
			state.AvailableIDs = append(state.AvailableIDs, asset.ID)
		}
	}
	sort.Strings(state.AvailableIDs)
	desired := config.Appearance.UIFontID
	if builtinUIFont(desired) || available[desired] {
		a.activeUIFontID = desired
	} else {
		state.PendingID = desired
	}
	if !builtinUIFont(a.activeUIFontID) && !available[a.activeUIFontID] {
		a.activeUIFontID = defaultUIFontID
	}
	state.ActiveID = a.activeUIFontID
	return state
}
