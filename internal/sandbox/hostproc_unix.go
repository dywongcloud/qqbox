//go:build darwin || linux

package sandbox

import "golang.org/x/sys/unix"

// Host process control over a stub process by its OS pid.

// osKick has the stub route its pending signals to its threads.
func osKick(pid int) { unix.Kill(pid, kickSignal) }

func osKill(pid int) { unix.Kill(pid, unix.SIGKILL) }

// hostStop and hostCont stop and resume process p's stub, all its threads.
// A stopped stub cannot cool the shared code it runs: it cools it first,
// and heats again only once it goes on (codeStop, codeGo).
func (sr *sigRun) hostStop(p *sigProc) {
	if pid := sr.ospid(p); pid > 0 {
		sr.codeStop(p)
		unix.Kill(pid, unix.SIGSTOP)
	}
}

func (sr *sigRun) hostCont(p *sigProc) {
	if sr.isParked(p) {
		sr.setPark(p, sr.threadsOf(p), false, false)
		return
	}
	if pid := sr.ospid(p); pid > 0 {
		sr.codeGo(p, func() { unix.Kill(pid, unix.SIGCONT) })
	}
}

// hostContThread lets thread t of stopped process p go on alone. A host stop
// is the whole process's, so where one host thread can be kicked (threadKicks)
// the stop is handed to the threads: every other thread is asked to park and
// kicked while the host still holds it, then the host lets go. A stopped
// thread takes its pending kick before any instruction, so none runs guest
// code before it parks, and t takes its trap. Elsewhere only a SIGCONT lets t
// go, since the others would run on too.
func (sr *sigRun) hostContThread(p *sigProc, t *sigThread) bool {
	if !threadKicks {
		return false
	}
	if sr.isParked(p) {
		sr.setPark(p, []*sigThread{t}, false, true)
		return true
	}
	var others []*sigThread
	for _, u := range sr.threadsOf(p) {
		if u != t {
			others = append(others, u)
		}
	}
	sr.setPark(p, others, true, true)
	for _, u := range others {
		sr.wake(u)
	}
	if pid := sr.ospid(p); pid > 0 {
		unix.Kill(pid, unix.SIGCONT)
	}
	return true
}

// hostStopThread has thread t of stopped process p stop again: it parks when
// p's threads hold the stop, else the host stops the process.
func (sr *sigRun) hostStopThread(p *sigProc, t *sigThread) {
	if sr.isParked(p) {
		sr.setPark(p, []*sigThread{t}, true, true)
		sr.wake(t)
		return
	}
	sr.hostStop(p)
}
