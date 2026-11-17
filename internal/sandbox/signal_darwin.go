//go:build darwin

package sandbox

// threadKicks is false: a kick is the process's, which its stub routes to the
// thread once it runs, so one thread of a stopped process cannot be had to
// take its signals before the others run on (hostContThread).
const threadKicks = false

// wakeThread reaches a thread blocked or running in the stub. On darwin the
// kick is process-wide (osKick) and the only per-thread wake is the pipe.
func (sr *sigRun) wakeThread(t *sigThread) {
	t.wakeWrite()
}
