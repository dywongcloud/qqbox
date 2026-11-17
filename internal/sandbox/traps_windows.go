//go:build windows

package sandbox

import (
	"slices"
	"sync"
)

// lcIsTrap asks whether a trap instruction at pc is one the rewriter placed
// for a syscall.
const lcIsTrap = 0x4c43002b // (pc) -> 1 or 0

// codeTraps is where the rewriter left traps in a process's file code and
// its trampolines: every site's, and the ones a restart resumes at. The stub
// serves a trap instruction as a syscall only if it is one of these or one it
// placed itself in code made executable at run time; any other is the
// guest's own, a SIGILL.
type codeTraps struct {
	mu sync.Mutex
	at []uint64 // sorted
}

// set replaces the traps in each of ranges by those of traps there.
func (t *codeTraps) set(ranges [][2]uint64, traps []uint64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.at = slices.DeleteFunc(t.at, func(a uint64) bool {
		for _, r := range ranges {
			if a >= r[0] && a < r[1] {
				return true
			}
		}
		return false
	})
	t.at = append(t.at, traps...)
	slices.Sort(t.at)
	t.at = slices.Compact(t.at)
}

func (t *codeTraps) has(pc uint64) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	_, ok := slices.BinarySearch(t.at, pc)
	return ok
}

// clone is a copy for a fork child, which runs the same code.
func (t *codeTraps) clone() *codeTraps {
	t.mu.Lock()
	defer t.mu.Unlock()
	return &codeTraps{at: slices.Clone(t.at)}
}

// isTrap serves lcIsTrap.
func (g *wguest) isTrap(pc uint64) int64 {
	if g.traps.has(pc) {
		return 1
	}
	return 0
}
