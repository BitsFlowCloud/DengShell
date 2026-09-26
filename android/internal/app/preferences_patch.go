package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// PatchAppearance changes only fields submitted by one frontend. Detached
// windows share this Store: merge, validate and write must use the same lock.
func (s *Store) PatchAppearance(patch map[string]json.RawMessage) (Appearance, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if patch == nil {
		return Appearance{}, errors.New("设置修改应为 JSON 对象")
	}
	value := cloneAppearance(s.config.Appearance)
	if len(patch) == 0 {
		return value, nil
	}
	fields := map[string]any{
		"uiFontId": &value.UIFontID, "fontId": &value.FontID, "backgroundId": &value.BackgroundID,
		"backgroundOpacity": &value.BackgroundOpacity, "backgroundVersion": &value.BackgroundVersion,
		"uiScale": &value.UIScale, "terminalFontSize": &value.TerminalFontSize,
		"startupAnimation": &value.StartupAnimation, "theme": &value.Theme,
		"onboardingCompleted": &value.OnboardingCompleted, "monitorSide": &value.MonitorSide,
		"filesPosition": &value.FilesPosition, "promptUsernameColor": &value.PromptUsernameColor,
		"promptHostnameColor": &value.PromptHostnameColor, "minimizeAction": &value.MinimizeAction,
		"externalEditor": &value.ExternalEditor,
	}
	for key, raw := range patch {
		var err error
		switch key {
		case "chartStyles":
			err = mergePreferenceMap(value.ChartStyles, raw)
		case "uiTextColors":
			err = mergePreferenceMap(value.UITextColors, raw)
		case "fontColors":
			err = mergePreferenceMap(value.FontColors, raw)
		case "fontBold":
			err = mergePreferenceMap(value.FontBold, raw)
		case "layout":
			var changes map[string]json.RawMessage
			if json.Unmarshal(raw, &changes) == nil {
				for key := range changes {
					if strings.HasPrefix(key, "dengshell.history.") {
						return Appearance{}, errors.New("命令历史请通过追加或清空接口修改")
					}
				}
			}
			err = mergePreferenceMap(value.Layout, raw)
		case "windowWidth", "windowHeight", "windowMaximised":
			return Appearance{}, errors.New("窗口尺寸由桌面窗口保存，不能通过界面设置覆盖")
		default:
			field, ok := fields[key]
			if !ok {
				return Appearance{}, fmt.Errorf("不支持的设置字段：%s", key)
			}
			if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
				return Appearance{}, fmt.Errorf("设置字段 %s 不能为 null", key)
			}
			err = json.Unmarshal(raw, field)
		}
		if err != nil {
			return Appearance{}, fmt.Errorf("设置字段 %s 的格式无效：%w", key, err)
		}
	}
	return s.saveAppearanceLocked(value)
}

// Null removes one map key. An omitted key always preserves the latest value
// from the Store, even when a different window last fetched an older map.
func mergePreferenceMap[T any](target map[string]T, raw json.RawMessage) error {
	if trimmed := bytes.TrimSpace(raw); len(trimmed) == 0 || trimmed[0] != '{' {
		return errors.New("应提交需要修改的键值对象；删除单项请将该项设为 null")
	}
	var changes map[string]json.RawMessage
	if err := json.Unmarshal(raw, &changes); err != nil {
		return err
	}
	for key, data := range changes {
		if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
			delete(target, key)
			continue
		}
		var value T
		if err := json.Unmarshal(data, &value); err != nil {
			return err
		}
		target[key] = value
	}
	return nil
}

func (a *App) patchAppearanceHTTP(w http.ResponseWriter, r *http.Request) {
	var patch map[string]json.RawMessage
	if !decode(w, r, &patch) {
		return
	}
	value, err := a.store.PatchAppearance(patch)
	if err == nil {
		a.uiFontRuntime()
	}
	respond(w, value, err)
}
