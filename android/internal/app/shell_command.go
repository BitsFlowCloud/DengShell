package app

import "strings"

// loginShellQuote survives Bash, Zsh and Fish unchanged. Fish interprets \\
// and \' inside single quotes, unlike POSIX shells, so emit those characters
// as unquoted escapes between quoted spans. No substitution runs in the login
// shell, and no base64/printf helper is required to launch a background probe.
func loginShellQuote(value string) string {
	return "'" + strings.NewReplacer(`\`, `'\\'`, `'`, `'\''`).Replace(value) + "'"
}

// Only application-owned POSIX scripts go through this boundary. Interactive
// terminal input and user commands continue to use the selected login shell.
func posixShellCommand(script string) string {
	return "/bin/sh -c " + loginShellQuote(script)
}
