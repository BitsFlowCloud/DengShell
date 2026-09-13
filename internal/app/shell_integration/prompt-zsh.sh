__dengshell_style_file='@DENGSHELL_STYLE_FILE@'
__dengshell_original_ps1=$PS1
__dengshell_styled_ps1=''
__dengshell_has_styled_prompt=0
__dengshell_apply_prompt_style() {
    emulate -L zsh
    local user_color='' host_color='' base user_prefix='' host_prefix='' user_reset='' host_reset=''
    [[ -r "$__dengshell_style_file" ]] || return 0
    { IFS= read -r user_color; IFS= read -r host_color; } < "$__dengshell_style_file"
    [[ -z "$user_color" || "$user_color" =~ '^#[0-9a-fA-F]{6}$' ]] || return 0
    [[ -z "$host_color" || "$host_color" =~ '^#[0-9a-fA-F]{6}$' ]] || return 0
    if [[ "$__dengshell_has_styled_prompt" != 1 || "$PS1" != "$__dengshell_styled_ps1" ]]; then __dengshell_original_ps1=$PS1; fi
    if [[ -z "$user_color$host_color" ]]; then
        if [[ "$__dengshell_has_styled_prompt" == 1 ]]; then PS1=$__dengshell_original_ps1; fi
        __dengshell_has_styled_prompt=0
        return 0
    fi
    if [[ -n "$user_color" ]]; then user_prefix="%F{$user_color}"; user_reset='%f'; fi
    if [[ -n "$host_color" ]]; then host_prefix="%F{$host_color}"; host_reset='%f'; fi
    base=$__dengshell_original_ps1
    if [[ "$base" != *'%n'* || ( "$base" != *'%m'* && "$base" != *'%M'* ) ]]; then base='%n@%m:%~%# '; fi
    base=${base//\%n/${user_prefix}%n${user_reset}}
    base=${base//\%m/${host_prefix}%m${host_reset}}
    base=${base//\%M/${host_prefix}%M${host_reset}}
    PS1=$base
    __dengshell_styled_ps1=$PS1
    __dengshell_has_styled_prompt=1
    return 0
}
__dengshell_remove_prompt_style() { command rm -f -- "$__dengshell_style_file" "$__dengshell_style_file.new" 2>/dev/null; }
zshexit_functions+=(__dengshell_remove_prompt_style)
