package app

import (
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"strconv"
	"strings"
)

// Appearance is stored alongside connection settings, so native and browser
// windows use the same selection and custom assets survive application upgrades.
type Appearance struct {
	UIFontID            string                     `json:"uiFontId"`
	UITextColors        map[string]string          `json:"uiTextColors"`
	FontID              string                     `json:"fontId"`
	FontColors          map[string]string          `json:"fontColors,omitempty"`
	FontBold            map[string]bool            `json:"fontBold"`
	ChartStyles         map[string]ChartLineStyle  `json:"chartStyles"`
	BackgroundID        string                     `json:"backgroundId"`
	BackgroundOpacity   float64                    `json:"backgroundOpacity"`
	BackgroundVersion   int                        `json:"backgroundVersion"`
	UIScale             float64                    `json:"uiScale"`
	TerminalFontSize    float64                    `json:"terminalFontSize"`
	TerminalBold        bool                       `json:"terminalBold,omitempty"` // Legacy input only; migrated to the selected font.
	StartupAnimation    bool                       `json:"startupAnimation"`
	Theme               string                     `json:"theme"`
	WindowWidth         int                        `json:"windowWidth"`
	WindowHeight        int                        `json:"windowHeight"`
	WindowMaximised     bool                       `json:"windowMaximised"`
	OnboardingCompleted bool                       `json:"onboardingCompleted"`
	MonitorSide         string                     `json:"monitorSide"`
	FilesPosition       string                     `json:"filesPosition"`
	PromptUsernameColor string                     `json:"promptUsernameColor"`
	PromptHostnameColor string                     `json:"promptHostnameColor"`
	MinimizeAction      string                     `json:"minimizeAction"`
	ExternalEditor      string                     `json:"externalEditor"`
	Layout              map[string]json.RawMessage `json:"layout"`
}

func defaultAppearance() Appearance {
	return Appearance{UIFontID: defaultUIFontID, UITextColors: map[string]string{}, FontID: "builtin:jetbrains-mono", FontColors: map[string]string{}, FontBold: map[string]bool{}, ChartStyles: map[string]ChartLineStyle{}, BackgroundID: "builtin:none", BackgroundOpacity: .42, BackgroundVersion: 2, UIScale: 1, TerminalFontSize: 14, StartupAnimation: true, Theme: "system", MonitorSide: "left", FilesPosition: "bottom", MinimizeAction: "ask", Layout: map[string]json.RawMessage{}}
}

// Old releases combined 18% image opacity with a dark multiply tint. Keep
// explicit strengths, including zero, while replacing that old default once.
func (value *Appearance) UnmarshalJSON(data []byte) error {
	type plain Appearance
	next := plain(defaultAppearance())
	if err := json.Unmarshal(data, &next); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	if _, versioned := fields["backgroundVersion"]; !versioned || next.BackgroundVersion < 2 {
		if math.Abs(next.BackgroundOpacity-.18) < .000001 {
			next.BackgroundOpacity = .42
		}
		next.BackgroundVersion = 2
	}
	if next.UIFontID == "" {
		next.UIFontID = defaultUIFontID
	}
	if next.UITextColors == nil {
		next.UITextColors = map[string]string{}
	}
	if next.FontColors == nil {
		next.FontColors = map[string]string{}
	}
	if next.FontBold == nil {
		next.FontBold = map[string]bool{}
	}
	if next.ChartStyles == nil {
		next.ChartStyles = map[string]ChartLineStyle{}
	}
	if next.Layout == nil {
		next.Layout = map[string]json.RawMessage{}
	}
	if _, perFont := fields["fontBold"]; !perFont && next.TerminalBold {
		next.FontBold[next.FontID] = true
	}
	next.TerminalBold = false
	*value = Appearance(next)
	return nil
}

func cloneAppearance(value Appearance) Appearance {
	uiColors := make(map[string]string, len(value.UITextColors))
	for key, color := range value.UITextColors {
		uiColors[key] = color
	}
	value.UITextColors = uiColors
	styles := make(map[string]ChartLineStyle, len(value.ChartStyles))
	for id, style := range value.ChartStyles {
		styles[id] = style
	}
	value.ChartStyles = styles
	colors := make(map[string]string, len(value.FontColors))
	for id, color := range value.FontColors {
		colors[id] = color
	}
	value.FontColors = colors
	bold := make(map[string]bool, len(value.FontBold))
	for id, enabled := range value.FontBold {
		bold[id] = enabled
	}
	value.FontBold = bold
	layout := make(map[string]json.RawMessage, len(value.Layout))
	for key, item := range value.Layout {
		layout[key] = append(json.RawMessage(nil), item...)
	}
	value.Layout = layout
	return value
}

func validTerminalColor(value string) bool {
	if len(value) != 7 || value[0] != '#' {
		return false
	}
	_, err := strconv.ParseUint(value[1:], 16, 24)
	return err == nil
}

func (s *Store) SaveAppearance(value Appearance) (Appearance, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveAppearanceLocked(value)
}

// Caller holds s.mu across reading, validation and committing the new value.
func (s *Store) saveAppearanceLocked(value Appearance) (Appearance, error) {
	if value.UIFontID == "" {
		value.UIFontID = defaultUIFontID
	}
	if err := validateUIAppearance(value, s.config.Assets); err != nil {
		return Appearance{}, err
	}
	if err := validateChartStyles(value.ChartStyles); err != nil {
		return Appearance{}, err
	}
	if len(value.ExternalEditor) > 4096 || strings.ContainsAny(value.ExternalEditor, "\x00\r\n") {
		return Appearance{}, errors.New("外部编辑器路径无效")
	}
	if value.MonitorSide == "" {
		value.MonitorSide = "left"
	}
	if value.FilesPosition == "" {
		value.FilesPosition = "bottom"
	}
	if value.MinimizeAction == "" {
		value.MinimizeAction = "ask"
	}
	if value.MonitorSide != "left" && value.MonitorSide != "right" {
		return Appearance{}, errors.New("监控位置应为 left 或 right")
	}
	if value.FilesPosition != "top" && value.FilesPosition != "bottom" {
		return Appearance{}, errors.New("文件面板位置应为 top 或 bottom")
	}
	if value.MinimizeAction != "ask" && value.MinimizeAction != "minimize" && value.MinimizeAction != "tray" {
		return Appearance{}, errors.New("最小化行为应为 ask、minimize 或 tray")
	}
	for _, color := range []*string{&value.PromptUsernameColor, &value.PromptHostnameColor} {
		if *color != "" && !validTerminalColor(*color) {
			return Appearance{}, errors.New("提示符颜色应为空或 #RRGGBB 格式")
		}
		*color = strings.ToLower(*color)
	}
	if value.Theme == "" {
		value.Theme = "system"
	}
	if value.Theme != "system" && value.Theme != "light" && value.Theme != "dark" {
		return Appearance{}, errors.New("主题应为 system、light 或 dark")
	}
	if err := validateSavedLayout(value.Layout); err != nil {
		return Appearance{}, err
	}
	if math.IsNaN(value.UIScale) || math.IsInf(value.UIScale, 0) || value.UIScale < .5 || value.UIScale > 2 {
		return Appearance{}, errors.New("界面缩放应为 50%–200%")
	}
	if math.IsNaN(value.BackgroundOpacity) || math.IsInf(value.BackgroundOpacity, 0) || value.BackgroundOpacity < 0 || value.BackgroundOpacity > 1 {
		return Appearance{}, errors.New("背景强度应为 0%–100%")
	}
	if math.IsNaN(value.TerminalFontSize) || math.IsInf(value.TerminalFontSize, 0) || value.TerminalFontSize < 8 || value.TerminalFontSize > 40 || math.Trunc(value.TerminalFontSize*2) != value.TerminalFontSize*2 {
		return Appearance{}, errors.New("终端字号应为 8–40，步进为 0.5 px")
	}
	if len(value.FontColors) > 1024 {
		return Appearance{}, errors.New("已保存的字体颜色过多")
	}
	if len(value.FontBold) > 1024 {
		return Appearance{}, errors.New("已保存的字体样式过多")
	}
	value = cloneAppearance(value)
	for key, color := range value.UITextColors {
		value.UITextColors[key] = strings.ToLower(color)
	}
	value.TerminalBold = false
	value.BackgroundVersion = 2
	for id, color := range value.FontColors {
		if (!validBuiltinAssetID(id) && !validAssetID(id)) || !validTerminalColor(color) {
			return Appearance{}, errors.New("字体颜色应为 #RRGGBB 格式")
		}
		value.FontColors[id] = strings.ToLower(color)
	}
	for id := range value.FontBold {
		if !validBuiltinAssetID(id) && !validAssetID(id) {
			return Appearance{}, errors.New("字体样式引用了无效的字体")
		}
	}
	for kind, id := range map[string]string{"font": value.FontID, "background": value.BackgroundID} {
		if validBuiltinAssetID(id) {
			continue
		}
		found := false
		for _, asset := range s.config.Assets {
			if asset.ID == id && asset.Kind == kind {
				found = true
				break
			}
		}
		if !found {
			return Appearance{}, errors.New("所选字体或背景不存在，请重新选择")
		}
	}
	old := s.config.Appearance
	// Native resize callbacks are authoritative. A previously fetched frontend
	// snapshot must never revert more recently saved window geometry.
	value.WindowWidth, value.WindowHeight, value.WindowMaximised = old.WindowWidth, old.WindowHeight, old.WindowMaximised
	s.config.Appearance = value
	if err := s.writeLocked(); err != nil {
		s.config.Appearance = old
		return Appearance{}, err
	}
	return cloneAppearance(value), nil
}

func validSavedLayoutKey(key string) bool {
	switch key {
	case "cloudshell.layout", "dengshell.workspace", "dengshell.appearance-palette", "dengshell.server-groups.collapsed", "dengshell.server-manager":
		return true
	}
	for _, prefix := range []string{"dengshell.history.", "dengshell.nic."} {
		if strings.HasPrefix(key, prefix) && len(key) > len(prefix) && len(key) <= len(prefix)+128 {
			for _, c := range key[len(prefix):] {
				if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') && c != '_' && c != '-' {
					return false
				}
			}
			return true
		}
	}
	return false
}

func validateSavedLayout(layout map[string]json.RawMessage) error {
	if len(layout) > 4096 {
		return errors.New("界面偏好项目过多")
	}
	for key, value := range layout {
		if !validSavedLayoutKey(key) || !json.Valid(value) {
			return errors.New("界面偏好包含无效项目")
		}
	}
	data, err := json.Marshal(layout)
	if err != nil || len(data) > 1<<20 {
		return errors.New("界面偏好和命令历史合计不能超过 1 MiB")
	}
	return nil
}

func (s *Store) SaveWindowState(width, height int, maximised bool) error {
	if width < 320 || height < 240 || width > 16384 || height > 16384 {
		return errors.New("保存的窗口尺寸无效")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	old := s.config.Appearance
	if old.WindowWidth == width && old.WindowHeight == height && old.WindowMaximised == maximised {
		return nil
	}
	s.config.Appearance.WindowWidth, s.config.Appearance.WindowHeight, s.config.Appearance.WindowMaximised = width, height, maximised
	if err := s.writeLocked(); err != nil {
		s.config.Appearance = old
		return err
	}
	return nil
}

func (a *App) Appearance() Appearance { return a.store.List().Appearance }
func (a *App) SaveWindowState(width, height int, maximised bool) error {
	return a.store.SaveWindowState(width, height, maximised)
}

func validBuiltinAssetID(id string) bool {
	if !strings.HasPrefix(id, "builtin:") || len(id) <= 8 || len(id) > 100 {
		return false
	}
	for _, c := range id[8:] {
		if c != '-' && c != '_' && (c < 'a' || c > 'z') && (c < '0' || c > '9') {
			return false
		}
	}
	return true
}

func (a *App) registerSettingsHTTP(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/appearance/patch", a.patchAppearanceHTTP)
	mux.HandleFunc("POST /api/appearance", func(w http.ResponseWriter, r *http.Request) {
		var input Appearance
		if !decode(w, r, &input) {
			return
		}
		value, err := a.store.saveFrontendAppearance(input)
		if err == nil {
			a.uiFontRuntime()
		}
		respond(w, value, err)
	})
	mux.HandleFunc("POST /api/proxies", func(w http.ResponseWriter, r *http.Request) {
		var input ManagedProxy
		if !decode(w, r, &input) {
			return
		}
		value, err := a.store.SaveProxy(input)
		respond(w, value, err)
	})
	mux.HandleFunc("DELETE /api/proxies/{id}", func(w http.ResponseWriter, r *http.Request) {
		respond(w, map[string]bool{"ok": true}, a.store.DeleteProxy(r.PathValue("id")))
	})
	mux.HandleFunc("POST /api/assets", a.importAssetHTTP)
	mux.HandleFunc("POST /api/assets/{id}", func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Name string `json:"name"`
		}
		if !decode(w, r, &input) {
			return
		}
		value, err := a.store.RenameAsset(r.PathValue("id"), input.Name)
		respond(w, value, err)
	})
	mux.HandleFunc("DELETE /api/assets/{id}", func(w http.ResponseWriter, r *http.Request) {
		respond(w, map[string]bool{"ok": true}, a.store.DeleteAsset(r.PathValue("id")))
	})
	mux.HandleFunc("GET /api/assets/{id}/data", func(w http.ResponseWriter, r *http.Request) {
		value, err := a.store.AssetData(r.PathValue("id"))
		respond(w, value, err)
	})
}
