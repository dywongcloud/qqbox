//go:build !amd64

package sandbox

// No hardware event-wait wired up on this arch yet (arm64 WFE is a candidate),
// so the waiter uses the busy spin. waitStore is never called while hasUmwait
// is false; it exists only to satisfy the one reference in the shared wait path.
const hasUmwait = false

func waitStore(addr *uint32, want1, want2 uint32, deadline uint64) uint32 { return 0 }
