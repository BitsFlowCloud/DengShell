package app

import (
	"cloudshell/internal/syncvault"
	"encoding/json"
	"sort"
	"strings"
)

// Independently created same-name groups keep all their servers and settings.
// Suffix only the duplicate name; never guess that two distinct IDs are the
// same group or silently discard one group's color/emoji or descendants.
func disambiguateSyncGroups(s *syncvault.Snapshot, clock syncvault.Clock) bool {
	keys := []string{}
	for k, v := range s.Entries {
		if strings.HasPrefix(k, "group/") && !syncvault.EqualJSON(v.Value, []byte("null")) {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	used := map[string]bool{}
	changed := false
	for _, k := range keys {
		entry := s.Entries[k]
		var g ServerGroup
		if json.Unmarshal(entry.Value, &g) != nil || !safeSyncID(g.ID) {
			continue
		}
		nameKey := g.ParentID + "\x00" + g.Name
		if used[nameKey] {
			base := g.Name
			for len(base) > 72 {
				r := []rune(base)
				base = string(r[:len(r)-1])
			}
			suffix := g.ID[:8]
			g.Name = base + " · " + suffix
			for used[g.ParentID+"\x00"+g.Name] {
				g.Name += "·"
			}
			entry.Value = syncRaw(g)
			entry.Clock = syncvault.Union(s.Clock, clock)
			s.Entries[k] = entry
			s.Clock = syncvault.Union(s.Clock, entry.Clock)
			changed = true
		}
		used[g.ParentID+"\x00"+g.Name] = true
	}
	return changed
}
