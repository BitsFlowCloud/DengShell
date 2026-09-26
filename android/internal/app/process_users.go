package app

import (
	"context"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
)

type processUserValue struct {
	user  string
	uid   uint64
	ready bool
	at    time.Time
}
type processUserCache struct {
	mu     sync.Mutex
	runner monitorCommandRunner
	values map[processMemoryKey]processUserValue
}

// User names are optional decoration. Read only the visible page, cache failures
// too, and never delay the core/network monitors or open a channel per PID.
func (s *Session) readProcessUsers(ctx context.Context, rows []Process) {
	c := &s.processUsers
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.values == nil {
		c.values = make(map[processMemoryKey]processUserValue)
	}
	wanted := make(map[int]processMemoryKey)
	for _, p := range rows {
		key := processMemoryKey{p.PID, p.startTicks, p.bootID}
		if p.PID > 0 && p.bootID != "" && time.Since(c.values[key].at) >= 30*time.Second && len(wanted) < processPageLimit {
			wanted[p.PID] = key
		}
	}
	if len(wanted) > 0 && ctx.Err() == nil {
		request, cancel := context.WithTimeout(ctx, 3*time.Second)
		data, err := c.runner.run(request, s, "process-users", processUsersCommand(wanted), 128<<10)
		cancel()
		values := map[processMemoryKey]processUserValue{}
		if err == nil {
			values = parseProcessUsers(data, wanted)
		}
		for _, key := range wanted {
			value := values[key]
			value.at = time.Now()
			c.values[key] = value
		}
	}
	for i := range rows {
		v := c.values[processMemoryKey{rows[i].PID, rows[i].startTicks, rows[i].bootID}]
		rows[i].User, rows[i].UID, rows[i].UserReady = v.user, v.uid, v.ready
	}
	for len(c.values) > 512 {
		var oldest processMemoryKey
		var at time.Time
		for key, v := range c.values {
			if at.IsZero() || v.at.Before(at) {
				oldest, at = key, v.at
			}
		}
		delete(c.values, oldest)
	}
}

func parseProcessUsers(data []byte, wanted map[int]processMemoryKey) map[processMemoryKey]processUserValue {
	values := make(map[processMemoryKey]processUserValue)
	boot := ""
	for _, line := range strings.Split(string(data), "\n") {
		f := strings.Split(line, "\t")
		if len(f) == 2 && f[0] == "K" {
			boot = f[1]
			continue
		}
		if len(f) != 5 || f[0] != "P" {
			continue
		}
		pid, e1 := strconv.Atoi(f[1])
		start, e2 := strconv.ParseUint(f[2], 10, 64)
		uid, e3 := strconv.ParseUint(f[3], 10, 32)
		name, e4 := hex.DecodeString(f[4])
		key, ok := wanted[pid]
		if !ok || e1 != nil || e2 != nil || e3 != nil || e4 != nil || len(name) > 256 || key.start != start || key.boot != boot {
			continue
		}
		user := string(name)
		if user == "" {
			user = strconv.FormatUint(uid, 10)
		}
		values[key] = processUserValue{user: user, uid: uid, ready: true}
	}
	return values
}

func processUsersCommand(wanted map[int]processMemoryKey) string {
	pids := make([]string, 0, min(len(wanted), processPageLimit))
	for pid := range wanted {
		if pid > 0 && len(pids) < processPageLimit {
			pids = append(pids, strconv.Itoa(pid))
		}
	}
	return fmt.Sprintf(`export LC_ALL=C
dengshell_user_file() {
    dengshell_line=
    while IFS= read -r dengshell_line || [ -n "$dengshell_line" ]; do
        printf '%%s\t%%s\n' "$1" "$dengshell_line"
        dengshell_line=
    done < "$2"
}
{
    if [ -r /proc/sys/kernel/random/boot_id ]; then
        dengshell_user_file K /proc/sys/kernel/random/boot_id
    else
        dengshell_user_file K /proc/stat
    fi
    for dengshell_pid in %s; do
        printf 'I\t%%s\n' "$dengshell_pid"
        dengshell_user_file A "/proc/$dengshell_pid/stat" 2>/dev/null
        while read -r dengshell_key dengshell_real dengshell_effective dengshell_rest; do
            if [ "$dengshell_key" = "Uid:" ]; then
                printf 'U\t%%s\n' "$dengshell_effective"
                break
            fi
        done < "/proc/$dengshell_pid/status" 2>/dev/null
        dengshell_user_file B "/proc/$dengshell_pid/stat" 2>/dev/null
        printf 'E\n'
    done
} | awk '
function start(text, fields, right,i) {
    right=0
    for(i=length(text);i>0;i--) if(substr(text,i,1)==")") {right=i;break}
    if(!right || split(substr(text,right+2),fields," ")<22 || fields[20]!~/^[0-9]+$/) return ""
    return fields[20]
}
function encode(text, i,result) {
    result=""
    for(i=1;i<=length(text);i++) result=result sprintf("%%02x",ord[substr(text,i,1)])
    return result
}
BEGIN {
    for(i=1;i<256;i++) ord[sprintf("%%c",i)]=i
    while((getline line < "/etc/passwd")>0) {split(line,parts,":");if(parts[3]~/^[0-9]+$/) users[parts[3]]=parts[1]}
    close("/etc/passwd")
}
/^K\t/ {value=substr($0,3);if(value~/^[0-9a-f-]+$/ || value~/^btime /) printf "K\t%%s\n",value;next}
/^I\t/ {pid=$2;first="";last="";uid="";next}
/^A\t/ {first=first (first==""?"":"\n") substr($0,3);next}
/^B\t/ {last=last (last==""?"":"\n") substr($0,3);next}
/^U\t/ {uid=$2;next}
/^E$/ {a=start(first);b=start(last);if(a!="" && a==b && uid~/^[0-9]+$/) printf "P\t%%s\t%%s\t%%s\t%%s\n",pid,a,uid,encode(users[uid])}
' 2>/dev/null
`, strings.Join(pids, " "))
}
