# Loaded by --init-command after Fish's ordinary configuration. All functions
# and variables belong only to this shell; no universal/config changes.
set -g __dengshell_nonce '@DENGSHELL_NONCE@'
set -g __dengshell_last_cwd ''
set -g __dengshell_pending_command ''

function __dengshell_before_command --on-event fish_preexec
    builtin printf '\033]777;DengShell;busy;%s\007' "$__dengshell_nonce"
    set -l text "$argv[1]"
    set -g __dengshell_pending_command ''
    # Only a command accepted by Fish's history policy is eligible. Never
    # inspect keypresses, a program's input, or history predating this session.
    set -q fish_private_mode; and return
    if set -q fish_history; and test -z "$fish_history"
        return
    end
    test -n "$text"; or return
    test (string length -- "$text") -le 16384; or return
    string match -q ' *' -- "$text"; and return
    set -g __dengshell_pending_command "$text"
end

function __dengshell_capture_history
    set -l text "$__dengshell_pending_command"
    set -g __dengshell_pending_command ''
    test -n "$text"; or return
    # Fish hides its pending history entry in both preexec and postexec. It is
    # committed before the next prompt (or fish_exit), including custom filters.
    test (count $history) -gt 0; or return
    test "$history[1]" = "$text"; or return
    command -q base64; or return
    set -l encoded (builtin printf '%s' "$text" | command base64 | string join '')
    test -n "$encoded"; or return
    builtin printf '\033]777;DengShell;command;%s;%s\007' "$__dengshell_nonce" "$encoded"
end

function __dengshell_apply_prompt_style
end
# @DENGSHELL_PROMPT_STYLE@

function __dengshell_prompt --on-event fish_prompt
    __dengshell_capture_history
    __dengshell_apply_prompt_style
    if test "$__dengshell_last_cwd" != "$PWD"; and command -q base64
        set -l encoded (builtin printf '%s' "$PWD" | command base64 | string join '')
        if test -n "$encoded"
            builtin printf '\033]777;DengShell;cwd;%s;%s\007' "$__dengshell_nonce" "$encoded"
            set -g __dengshell_last_cwd "$PWD"
        end
    end
    builtin printf '\033]777;DengShell;prompt;%s\007' "$__dengshell_nonce"
end

function __dengshell_exit_history --on-event fish_exit
    __dengshell_capture_history
end

# @DENGSHELL_CLEANUP@
builtin printf '\033]777;DengShell;ready;%s;fish\007' "$__dengshell_nonce"
