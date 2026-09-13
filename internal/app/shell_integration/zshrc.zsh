# Loaded from the session's temporary ZDOTDIR after the user's ordinary rc file.
__dengshell_nonce='@DENGSHELL_NONCE@'
__dengshell_can_encode=0
command -v base64 >/dev/null 2>&1 && __dengshell_can_encode=1
__dengshell_before_command() {
    local ignore_space=${options[histignorespace]}
    emulate -L zsh
    local text=$1 encoded
    builtin printf '\033]777;DengShell;busy;%s\007' "$__dengshell_nonce"
    (( __dengshell_can_encode && HISTSIZE > 0 )) || return 0
    # Zsh temporarily retains ignored-space lines in memory, so respect this
    # preference explicitly in addition to the shell's normal history policy.
    [[ "$ignore_space" == on && "$text" == ' '* ]] && return 0
    [[ -n ${HISTORY_IGNORE-} && "$text" == ${~HISTORY_IGNORE} ]] && return 0
    [[ -n "$text" && ${#text} -le 16384 ]] || return 0
    encoded=$(builtin printf '%s' "$text" | command base64) || return 0
    encoded=${encoded//$'\n'/}; encoded=${encoded//$'\r'/}
    builtin printf '\033]777;DengShell;command;%s;%s\007' "$__dengshell_nonce" "$encoded"
}
__dengshell_apply_prompt_style() { :; }
# @DENGSHELL_PROMPT_STYLE@

__dengshell_prompt() {
    __dengshell_apply_prompt_style
    builtin printf '\033]777;DengShell;prompt;%s\007' "$__dengshell_nonce"
}
preexec_functions+=(__dengshell_before_command)
precmd_functions+=(__dengshell_prompt)
zle_highlight+=(paste:none)
# @DENGSHELL_CLEANUP@
builtin printf '\033]777;DengShell;ready;%s;zsh\007' "$__dengshell_nonce"
