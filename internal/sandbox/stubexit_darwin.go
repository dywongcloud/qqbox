package sandbox

import "golang.org/x/sys/unix"

// A stub process's end runs stubGone, whatever the cause. A stub the
// supervisor started (the main one, an exec's, a pooled one) is its child,
// and cmd.Wait reports the end (stubEnded); a forked stub is its parent
// stub's child, and a kqueue NOTE_EXIT does (watchStub). fs.exited[pid] is
// closed once it has ended.

// noteStub registers stub process pid before anything can report its end. It
// reports false if pid is already registered and has not ended; a closed
// entry is an earlier stub's that had the same pid.
func (fs *dfs) noteStub(pid int) bool {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	if gone := fs.exited[pid]; gone != nil {
		select {
		case <-gone:
		default:
			return false
		}
	}
	fs.exited[pid] = make(chan struct{})
	return true
}

// stubEnded records that stub process pid has ended and ends what ran in it.
func (fs *dfs) stubEnded(pid int) {
	fs.mu.Lock()
	gone := fs.exited[pid]
	if gone == nil {
		gone = make(chan struct{})
		fs.exited[pid] = gone
	}
	select {
	case <-gone:
	default:
		close(gone)
	}
	fs.mu.Unlock()
	if fs.run != nil && fs.run.broker != nil {
		fs.run.broker.ended(pid) // after the mark: a wait for its mailbox sees it gone
	}
	fs.stubGone(pid)
}

// stubDone reports whether stub process pid has ended.
func (fs *dfs) stubDone(pid int) bool {
	fs.mu.Lock()
	gone := fs.exited[pid]
	fs.mu.Unlock()
	if gone == nil {
		return false
	}
	select {
	case <-gone:
		return true
	default:
		return false
	}
}

// bindStub is called once the registry names pid for a process. If the stub
// ended before that, stubEnded's sweep missed the process: sweep again. (The
// end is recorded before its sweep, and the name before this check, so one
// of the two sweeps finds the process.)
func (fs *dfs) bindStub(pid int) {
	fs.mu.Lock()
	gone := fs.exited[pid]
	fs.mu.Unlock()
	if gone == nil {
		return
	}
	select {
	case <-gone:
		fs.stubGone(pid)
	default:
	}
}

// watchStub watches a forked stub process, which the registry already names,
// for its end.
func (fs *dfs) watchStub(pid int) {
	if !fs.noteStub(pid) {
		return // watched already
	}
	kq, err := unix.Kqueue()
	if err == nil {
		ev := []unix.Kevent_t{{Ident: uint64(pid), Filter: unix.EVFILT_PROC, Flags: unix.EV_ADD | unix.EV_ONESHOT, Fflags: unix.NOTE_EXIT}}
		if _, err = unix.Kevent(kq, ev, nil, nil); err != nil {
			unix.Close(kq)
		}
	}
	if err != nil && err != unix.ESRCH {
		osKill(pid) // unwatchable: end it, so stubEnded below is true
	}
	fs.wg.Add(1)
	go func() {
		defer fs.wg.Done()
		if err == nil {
			out := make([]unix.Kevent_t, 1)
			for {
				if n, err := unix.Kevent(kq, nil, out, nil); n > 0 || err != unix.EINTR {
					break
				}
			}
			unix.Close(kq)
		}
		fs.stubEnded(pid)
	}()
}

// awaitStubGone waits for an exiting stub process to be gone from the host.
// After the supervisor replies its exit, the stub still reads the reply from
// its channel slot on the way into the exit call; freeing the slot before
// then would let a new process take it over while the dying stub reads (and
// acks into) it. It is also when Linux would show the zombie.
func (fs *dfs) awaitStubGone(ospid int) {
	if ospid <= 0 {
		return
	}
	fs.mu.Lock()
	gone := fs.exited[ospid]
	fs.mu.Unlock()
	if gone != nil {
		<-gone
	}
}
