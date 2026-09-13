set -g __dengshell_style_file '@DENGSHELL_STYLE_FILE@'
set -g __dengshell_has_styled_prompt 0
function __dengshell_fish_color
    set -l color "$argv[1]"
    test -n "$color"; or return
    # The DengShell terminal supports RGB. Fish 3.x can cache a 256-color
    # capability decision before init-command, so emit RGB directly here.
    builtin printf '\033[38;2;%d;%d;%dm' 0x(string sub -s 2 -l 2 -- "$color") 0x(string sub -s 4 -l 2 -- "$color") 0x(string sub -s 6 -l 2 -- "$color")
end
function __dengshell_apply_prompt_style
    test -r "$__dengshell_style_file"; or return
    set -l user_color ''
    set -l host_color ''
    begin
        read user_color
        read host_color
    end < "$__dengshell_style_file"
    for color in "$user_color" "$host_color"
        if test -n "$color"; and not string match -rq '^#[0-9a-fA-F]{6}$' -- "$color"
            return
        end
    end
    if test -z "$user_color$host_color"
        if test "$__dengshell_has_styled_prompt" = 1
            functions --erase fish_prompt
            functions --copy __dengshell_original_fish_prompt fish_prompt
            functions --erase __dengshell_original_fish_prompt
            set -g __dengshell_has_styled_prompt 0
        end
        return
    end
    if test "$__dengshell_has_styled_prompt" != 1
        functions --copy fish_prompt __dengshell_original_fish_prompt; or return
        function fish_prompt
            __dengshell_fish_color "$__dengshell_user_color"
            builtin printf '%s' "$USER"
            builtin printf '\033[39m'
            builtin printf '@'
            __dengshell_fish_color "$__dengshell_host_color"
            builtin printf '%s' (string split -m 1 . -- "$hostname")[1]
            builtin printf '\033[39m'
            builtin printf ':%s' (prompt_pwd)
            if fish_is_root_user
                builtin printf '# '
            else
                builtin printf '$ '
            end
        end
        set -g __dengshell_has_styled_prompt 1
    end
    set -g __dengshell_user_color "$user_color"
    set -g __dengshell_host_color "$host_color"
end
function __dengshell_remove_prompt_style --on-event fish_exit
    command rm -f -- "$__dengshell_style_file" "$__dengshell_style_file.new" 2>/dev/null
end
