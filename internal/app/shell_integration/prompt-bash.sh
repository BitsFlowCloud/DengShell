# A private data file, never executed as a shell script. No subprocess runs on
# each prompt; the existing PS1 is restored when both colors are cleared.
__dengshell_style_file='@DENGSHELL_STYLE_FILE@'
__dengshell_original_ps1=$PS1
__dengshell_styled_ps1=''
__dengshell_has_styled_prompt=0
__dengshell_apply_prompt_style() {
    local user_color='' host_color='' base user_prefix='' host_prefix='' user_reset='' host_reset=''
    [[ -r "$__dengshell_style_file" ]] || return 0
    { IFS= read -r user_color; IFS= read -r host_color; } < "$__dengshell_style_file"
    [[ -z "$user_color" || "$user_color" =~ ^#[0-9a-fA-F]{6}$ ]] || return 0
    [[ -z "$host_color" || "$host_color" =~ ^#[0-9a-fA-F]{6}$ ]] || return 0
    if [[ "$__dengshell_has_styled_prompt" != 1 || "$PS1" != "$__dengshell_styled_ps1" ]]; then
        __dengshell_original_ps1=$PS1
    fi
    if [[ -z "$user_color$host_color" ]]; then
        if [[ "$__dengshell_has_styled_prompt" == 1 ]]; then PS1=$__dengshell_original_ps1; fi
        __dengshell_has_styled_prompt=0
        return 0
    fi
    if [[ -n "$user_color" ]]; then
        builtin printf -v user_prefix '\\[\\e[38;2;%d;%d;%dm\\]' "$((16#${user_color:1:2}))" "$((16#${user_color:3:2}))" "$((16#${user_color:5:2}))"
        user_reset='\[\e[39m\]'
    fi
    if [[ -n "$host_color" ]]; then
        builtin printf -v host_prefix '\\[\\e[38;2;%d;%d;%dm\\]' "$((16#${host_color:1:2}))" "$((16#${host_color:3:2}))" "$((16#${host_color:5:2}))"
        host_reset='\[\e[39m\]'
    fi
    base=$__dengshell_original_ps1
    if [[ "$base" != *'\u'* || ( "$base" != *'\h'* && "$base" != *'\H'* ) ]]; then
        # Arbitrary theme output has no reliable username/hostname boundaries.
        # Use the shell's own dynamic identity/path escapes for custom styling.
        base='\u@\h:\w\$ '
    fi
    base=${base//\\u/${user_prefix}\\u${user_reset}}
    base=${base//\\h/${host_prefix}\\h${host_reset}}
    base=${base//\\H/${host_prefix}\\H${host_reset}}
    PS1=$base
    __dengshell_styled_ps1=$PS1
    __dengshell_has_styled_prompt=1
    return 0
}
__dengshell_remove_prompt_style() { command rm -f -- "$__dengshell_style_file" "$__dengshell_style_file.new" 2>/dev/null; }
# Do not overwrite a user's existing EXIT trap. Backend disconnect also removes
# this session's file, with a bounded wait before closing its SSH transport.
if [[ -z "$(trap -p EXIT)" ]]; then trap '__dengshell_remove_prompt_style' EXIT; fi
