package app

import (
	"context"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/pkg/sftp"
)

type fileOwnerNames struct{ users, groups map[uint32]string }
type fileOwnerCache struct {
	mu      sync.Mutex
	loading chan struct{}
	expires time.Time
	names   fileOwnerNames
}

func (n fileOwnerNames) owner(info os.FileInfo) string {
	st, ok := info.Sys().(*sftp.FileStat)
	if !ok {
		return "—"
	}
	u, g := n.users[st.UID], n.groups[st.GID]
	if u == "" {
		u = "未知用户"
	}
	if g == "" {
		g = "未知用户组"
	}
	return u + " / " + g
}

// Resolve IDs on this server only. Cache successful and missing names briefly;
// concurrent directory/permissions requests share one lookup, without holding
// a mutex across network I/O. Returned maps are immutable snapshots.
func (s *Session) resolveFileOwners(ctx context.Context, infos []os.FileInfo) fileOwnerNames {
	c := &s.fileOwners
	for {
		c.mu.Lock()
		if pending := c.loading; pending != nil {
			c.mu.Unlock()
			select {
			case <-pending:
				continue
			case <-ctx.Done():
				return fileOwnerNames{}
			}
		}
		if time.Now().After(c.expires) {
			c.names = fileOwnerNames{map[uint32]string{}, map[uint32]string{}}
			c.expires = time.Now().Add(time.Minute)
		}
		users, groups := map[uint32]bool{}, map[uint32]bool{}
		for _, info := range infos {
			if st, ok := info.Sys().(*sftp.FileStat); ok {
				if _, found := c.names.users[st.UID]; !found && len(users) < 4096 {
					users[st.UID] = true
				}
				if _, found := c.names.groups[st.GID]; !found && len(groups) < 4096 {
					groups[st.GID] = true
				}
			}
		}
		previous := c.names
		if len(users)+len(groups) == 0 || ctx.Err() != nil {
			c.mu.Unlock()
			return previous
		}
		pending := make(chan struct{})
		c.loading = pending
		c.mu.Unlock()
		names := s.lookupFileOwners(ctx, users, groups)
		c.mu.Lock()
		// Bound cache growth when traversing many directories with different IDs.
		if len(previous.users)+len(previous.groups)+len(users)+len(groups) <= 8192 {
			for id, name := range previous.users {
				names.users[id] = name
			}
			for id, name := range previous.groups {
				names.groups[id] = name
			}
		}
		if ctx.Err() == nil {
			c.names = names
		}
		c.loading = nil
		close(pending)
		c.mu.Unlock()
		return names
	}
}

func ownerIDArguments(ids map[uint32]bool) string {
	values := make([]uint32, 0, len(ids))
	for id := range ids {
		values = append(values, id)
	}
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
	args := make([]string, len(values))
	for i, id := range values {
		args[i] = strconv.FormatUint(uint64(id), 10)
	}
	return strings.Join(args, " ")
}

// Never return password fields or group member lists to the UI. Only requested
// numeric IDs enter the shell command; remote names are treated as plain text.
func (s *Session) lookupFileOwners(ctx context.Context, users, groups map[uint32]bool) fileOwnerNames {
	names := fileOwnerNames{map[uint32]string{}, map[uint32]string{}}
	for id := range users {
		names.users[id] = ""
	}
	for id := range groups {
		names.groups[id] = ""
	}
	if s.client != nil {
		var commands []string
		for _, db := range []struct {
			name, prefix string
			ids          map[uint32]bool
		}{{"passwd", "u", users}, {"group", "g", groups}} {
			if len(db.ids) != 0 {
				commands = append(commands, "getent "+db.name+" "+ownerIDArguments(db.ids)+" 2>/dev/null | LC_ALL=C awk -F: 'NF >= 4 {print \""+db.prefix+":\" $3 \":\" $1}'")
			}
		}
		lookupCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		output, _ := s.runBoundedSSH(lookupCtx, strings.Join(commands, "; "), 1<<20)
		cancel()
		for _, line := range strings.Split(string(output), "\n") {
			parts := strings.Split(line, ":")
			if len(parts) != 3 {
				continue
			}
			switch parts[0] {
			case "u":
				setOwnerName(names.users, parts[1], parts[2])
			case "g":
				setOwnerName(names.groups, parts[1], parts[2])
			}
		}
	}
	// SFTP-only accounts and minimal systems may have no exec/getent support.
	// These are remote files, never the desktop's local account databases.
	for _, db := range []struct {
		path  string
		names map[uint32]string
	}{{"/etc/passwd", names.users}, {"/etc/group", names.groups}} {
		missing := false
		for _, name := range db.names {
			if name == "" {
				missing = true
				break
			}
		}
		if !missing || s.files == nil || ctx.Err() != nil {
			continue
		}
		f, err := s.files.Open(db.path)
		if err != nil {
			continue
		}
		data, _ := io.ReadAll(io.LimitReader(f, 1<<20))
		_ = f.Close()
		for _, line := range strings.Split(string(data), "\n") {
			parts := strings.Split(line, ":")
			if len(parts) >= 4 {
				setOwnerName(db.names, parts[2], parts[0])
			}
		}
	}
	return names
}

func setOwnerName(names map[uint32]string, idText, name string) {
	id, err := strconv.ParseUint(idText, 10, 32)
	if err != nil || name == "" || len(name) > 256 || strings.ContainsFunc(name, unicode.IsControl) {
		return
	}
	if old, wanted := names[uint32(id)]; wanted && old == "" {
		names[uint32(id)] = name
	}
}
