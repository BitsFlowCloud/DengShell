__dengshell_style_file='@DENGSHELL_STYLE_FILE@'
__dengshell_original_ps1=$PS1
__dengshell_styled_ps1=''
__dengshell_has_styled_prompt=0
__dengshell_apply_prompt_style() {
    emulate -L zsh
    local user_color='' host_color='' base user_prefix='' host_prefix='' user_reset='' host_reset=''
    local styled='' hidden=0 user_found=0 host_found=0 token
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
    # %{...%} may contain an OSC title using the same identity escapes as the
    # visible prompt. Keep it intact, including nested spans and literal %%.
    while [[ -n "$base" ]]; do
        token=${base:0:2}
        case "$token" in
            '%%') styled+=$token; base=${base:2} ;;
            '%{') hidden=$((hidden + 1)); styled+=$token; base=${base:2} ;;
            '%}')
                if (( hidden > 0 )); then hidden=$((hidden - 1)); fi
                styled+=$token; base=${base:2}
                ;;
            '%n')
                if (( hidden == 0 )); then styled+="${user_prefix}${token}${user_reset}"; user_found=1
                else styled+=$token; fi
                base=${base:2}
                ;;
            '%m'|'%M')
                if (( hidden == 0 )); then styled+="${host_prefix}${token}${host_reset}"; host_found=1
                else styled+=$token; fi
                base=${base:2}
                ;;
            *) styled+=${base:0:1}; base=${base:1} ;;
        esac
    done
    if (( user_found == 0 || host_found == 0 )); then styled="${user_prefix}%n${user_reset}@${host_prefix}%m${host_reset}:%~%# "; fi
    PS1=$styled
    __dengshell_styled_ps1=$PS1
    __dengshell_has_styled_prompt=1
    return 0
}
__dengshell_remove_prompt_style() { command rm -f -- "$__dengshell_style_file" "$__dengshell_style_file.new" 2>/dev/null; }
zshexit_functions+=(__dengshell_remove_prompt_style)
