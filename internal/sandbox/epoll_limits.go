//go:build darwin || linux

package sandbox

// epMaxEvents bounds the maxevents argument to epoll_wait/epoll_pwait, matching
// the Windows backend. The Linux kernel's own ceiling (INT_MAX/sizeof) is far
// larger, but this is already beyond any real fd set. The earlier limit was
// 1024, which wrongly returned EINVAL to servers that size maxevents to their
// open-file limit — redis passes ~10000 and aborted on the EINVAL.
const epMaxEvents = 1 << 20

// epollBufMax caps the host event buffer a single wait reads into, so a large
// maxevents does not grow the per-call allocation. The host epoll/kqueue keeps
// events not taken this round for the next wait, so reading in chunks of this
// size loses none. At or below this, the buffer is the old 2*maxEvents, so
// existing callers are unchanged.
const epollBufMax = 2048
