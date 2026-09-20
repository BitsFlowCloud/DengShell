package app

import (
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"
)

const profileNotesMaxLineWidth = 40
const profileNotesMaxLines = 3

func normalizeProfileNoteLines(value string) string {
	return strings.ReplaceAll(strings.ReplaceAll(value, "\r\n", "\n"), "\r", "\n")
}

// Use the same predictable units as the editor: ASCII is one, Chinese and
// other non-ASCII characters are two; tabs reserve four spaces.
func profileNoteLineWidth(value string) int {
	width := 0
	for _, r := range value {
		switch {
		case r == '\t':
			width += 4
		case r <= 0x7f:
			width++
		default:
			width += 2
		}
	}
	return width
}

// Keep the storage/import/sync limit compatible with existing notes. New or
// edited notes have the tighter UI limit; older content is never truncated.
func validateProfileNotes(value string) error {
	if !utf8.ValidString(value) || utf8.RuneCountInString(value) > 4000 || strings.ContainsFunc(value, func(r rune) bool {
		return unicode.IsControl(r) && r != '\n' && r != '\r' && r != '\t'
	}) {
		return errors.New("备注最多 4000 个字符，不能包含无效控制字符")
	}
	return nil
}

func validateEditedProfileNotes(value string) error {
	if err := validateProfileNotes(value); err != nil {
		return err
	}
	value = normalizeProfileNoteLines(value)
	lines := strings.Split(value, "\n")
	if len(lines) > profileNotesMaxLines {
		return errors.New("备注正文最多 3 行，每行最多 20 个中文或 40 个英文字符")
	}
	for _, line := range lines {
		if profileNoteLineWidth(line) > profileNotesMaxLineWidth {
			return errors.New("备注每行最多 20 个中文或 40 个英文字符（中文计 2，英文计 1）")
		}
	}
	return nil
}
