package app

import "cloudshell/internal/syncvault"

type syncVariant struct {
	entry syncvault.Entry
	label string
}

// Keep the causal frontier across all devices. Resolving pairwise conflicts
// while more heads remain can offer obsolete values and omit a newer value.
func addSyncVariant(frontier []syncVariant, incoming syncVariant) []syncVariant {
	for _, old := range frontier {
		x, y := syncvault.Dominates(old.entry.Clock, incoming.entry.Clock), syncvault.Dominates(incoming.entry.Clock, old.entry.Clock)
		if x && (!y || syncvault.EqualJSON(old.entry.Value, incoming.entry.Value)) {
			return frontier
		}
	}
	next := frontier[:0]
	for _, old := range frontier {
		if syncvault.Dominates(incoming.entry.Clock, old.entry.Clock) && !syncvault.Dominates(old.entry.Clock, incoming.entry.Clock) {
			continue
		}
		next = append(next, old)
	}
	return append(next, incoming)
}

// Equal values can share a clock only after obsolete versions are discarded:
// doing it sooner invents a causal relationship with heads not yet processed.
func coalesceSyncVariants(frontier []syncVariant) []syncVariant {
	next := []syncVariant{}
	for _, v := range frontier {
		found := false
		for i := range next {
			if syncvault.EqualJSON(next[i].entry.Value, v.entry.Value) {
				next[i].entry.Clock = syncvault.Union(next[i].entry.Clock, v.entry.Clock)
				found = true
				break
			}
		}
		if !found {
			next = append(next, v)
		}
	}
	return next
}
