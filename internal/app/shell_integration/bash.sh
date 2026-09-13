# This file is a temporary --rcfile, never a user's shell configuration file.
# Match login-profile loading as closely as Bash's --rcfile interface permits.
if [ -r /etc/profile ]; then . /etc/profile; fi
for __dengshell_profile in "$HOME/.bash_profile" "$HOME/.bash_login" "$HOME/.profile"; do
    if [ -r "$__dengshell_profile" ]; then . "$__dengshell_profile"; break; fi
done
unset __dengshell_profile

__dengshell_nonce='@DENGSHELL_NONCE@'
__dengshell_at_prompt=0
__dengshell_initialized=0
__dengshell_last_entry=''
__dengshell_last_index=0
__dengshell_can_encode=0
command -v base64 >/dev/null 2>&1 && __dengshell_can_encode=1

__dengshell_command_event() {
    local text=$1 encoded
    [ "$__dengshell_can_encode" = 1 ] || return 0
    [ -n "$text" ] && [ "${#text}" -le 16384 ] || return 0
    encoded=$(builtin printf '%s' "$text" | command base64) || return 0
    encoded=${encoded//$'\n'/}
    encoded=${encoded//$'\r'/}
    builtin printf '\033]777;DengShell;command;%s;%s\007' "$__dengshell_nonce" "$encoded"
}

__dengshell_capture_history() {
    local HISTTIMEFORMAT='' entry index text
    [[ -o history ]] || return 0
    entry=$(builtin history 1)
    if [[ -z "$entry" ]]; then
        __dengshell_last_entry=''; __dengshell_last_index=0; return 0
    fi
    [[ "$entry" != "$__dengshell_last_entry" ]] || return 0
    __dengshell_last_entry=$entry
    if [[ "$entry" =~ ^[[:space:]]*([0-9]+)[[:space:]]+(.*)$ ]]; then
        index=${BASH_REMATCH[1]}; text=${BASH_REMATCH[2]}
        if (( index > __dengshell_last_index )); then __dengshell_command_event "$text"; fi
        __dengshell_last_index=$index
    fi
    return 0
}

__dengshell_before_command() {
    if [[ "$__dengshell_at_prompt" == 1 && "$BASH_COMMAND" != '__dengshell_prompt' ]]; then
        __dengshell_at_prompt=0
        __dengshell_capture_history
        builtin printf '\033]777;DengShell;busy;%s\007' "$__dengshell_nonce"
    fi
    return 0
}

__dengshell_apply_prompt_style() { :; }
# @DENGSHELL_PROMPT_STYLE@

__dengshell_prompt() {
    local previous_status=$?
    __dengshell_at_prompt=0
    if [[ "$__dengshell_initialized" == 0 ]]; then
        __dengshell_initialized=1
        __dengshell_last_entry=$(HISTTIMEFORMAT='' builtin history 1)
        if [[ "$__dengshell_last_entry" =~ ^[[:space:]]*([0-9]+) ]]; then
            __dengshell_last_index=${BASH_REMATCH[1]}
        fi
    else
        __dengshell_capture_history
    fi
    __dengshell_apply_prompt_style
    __dengshell_at_prompt=1
    builtin printf '\033]777;DengShell;prompt;%s\007' "$__dengshell_nonce"
    return "$previous_status"
}

# The first prompt primes history after Bash has loaded HISTFILE, without
# exporting any previous remote commands to this computer.

# Keep existing DEBUG traps intact. Such shells report completed commands from
# PROMPT_COMMAND; otherwise DEBUG also reports accepted long-running/exit lines.
if [[ -z "$(trap -p DEBUG)" ]] && ! shopt -q extdebug; then
    trap '__dengshell_before_command' DEBUG
fi
if (( BASH_VERSINFO[0] > 5 || (BASH_VERSINFO[0] == 5 && BASH_VERSINFO[1] >= 1) )); then
    PROMPT_COMMAND+=(__dengshell_prompt)
else
    PROMPT_COMMAND="${PROMPT_COMMAND:+$PROMPT_COMMAND; }__dengshell_prompt"
fi

# Readline's inverse paste region is unrelated to xterm's mouse selection.
# Disable only that visual treatment; bracketed paste remains enabled.
bind 'set enable-bracketed-paste on' 2>/dev/null || true
bind 'set enable-active-region off' 2>/dev/null || true
# @DENGSHELL_CLEANUP@
builtin printf '\033]777;DengShell;ready;%s;bash\007' "$__dengshell_nonce"
