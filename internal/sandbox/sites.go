//go:build linux

package sandbox

import (
	"maps"
	"slices"
)

// siteSet is the set of trap sites a process's code holds. A loaded image's
// sites never change, so they are kept as the image's sorted list, shared
// with forks and with later loads of the same image; code made executable at
// run time adds to a map of the process's own.
type siteSet struct {
	lists [][]uint64 // sorted, immutable
	more  map[uint64]bool
}

func (s *siteSet) has(a uint64) bool {
	for _, l := range s.lists {
		if _, ok := slices.BinarySearch(l, a); ok {
			return true
		}
	}
	return s.more[a]
}

// addList adds sorted, immutable sites.
func (s *siteSet) addList(l []uint64) {
	if len(l) > 0 {
		s.lists = append(s.lists, l)
	}
}

func (s *siteSet) add(sites []uint64) {
	if len(sites) == 0 {
		return
	}
	if s.more == nil {
		s.more = map[uint64]bool{}
	}
	for _, a := range sites {
		s.more[a] = true
	}
}

// clone returns a copy that changes independently.
func (s *siteSet) clone() siteSet {
	return siteSet{lists: slices.Clip(s.lists), more: maps.Clone(s.more)}
}
