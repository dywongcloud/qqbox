//go:build darwin

package sandbox

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"

	"golang.org/x/sys/unix"
)

// stubPool keeps stubs started ahead of need, already set up and
// sandboxed and parked on a slot of its own, so an execve that needs a fresh
// stub (execRespawn) skips process creation, dyld and Seatbelt setup. The
// pool fills on the first such exec and refills after each one it serves; two
// parked stubs cover back-to-back execs, as in a shell loop.
const poolSize = 2

type stubPool struct {
	mu       sync.Mutex
	ready    []*pooledStub
	starting bool
}

type pooledStub struct {
	cmd  *stubCmd
	mp   *memProc // its fdlink pairing, for the process it is assigned to
	slot int
	ch   *channel
	stop chan struct{}

	mu       sync.Mutex
	released bool // its pool slot went back to the run (assign): not ours to end
}

// fillPool tops the pool up to poolSize parked stubs in the background.
func (fs *dfs) fillPool() {
	pool := &fs.run.pool
	pool.mu.Lock()
	if pool.starting || len(pool.ready) >= poolSize {
		pool.mu.Unlock()
		return
	}
	pool.starting = true
	pool.mu.Unlock()
	fs.wg.Add(1)
	go func() {
		defer fs.wg.Done()
		for {
			p := fs.startPooled()
			pool.mu.Lock()
			if p != nil {
				pool.ready = append(pool.ready, p)
			}
			if p == nil || len(pool.ready) >= poolSize {
				pool.starting = false
				pool.mu.Unlock()
				return
			}
			pool.mu.Unlock()
		}
	}()
}

// startPooled starts a pooled stub and waits for it to park, or returns nil.
func (fs *dfs) startPooled() *pooledStub {
	idx := fs.allocSlot()
	if idx < 0 {
		return nil
	}
	ch := newChannel(fs.mem, idx, 0, fs.done, nil)
	pg := ch.page
	pg.state, pg.kind, pg.stubSleep, pg.supSleep, pg.rip = 0, 0, 0, 0, 0
	stop := make(chan struct{})
	ch.stop = stop
	cmd, mp, err := fs.run.spawn(fs.run.stub, []string{fmt.Sprintf("lc-stub@pool@%x", idx*slotSize), fs.run.chanPath, "", "", "", ""}, &fs.mu)
	if err != nil {
		fs.freeSlot(idx)
		return nil
	}
	p := &pooledStub{cmd: cmd, mp: mp, slot: idx, ch: ch, stop: stop}
	fs.noteStub(cmd.Process.Pid)
	go func() {
		// The run's teardown kills its process group; a stub in another
		// group (started before pid 1 moved to a new one) goes with the run.
		select {
		case <-fs.done:
			unix.Kill(cmd.Process.Pid, unix.SIGKILL)
		case <-stop:
		}
	}()
	fs.wg.Add(1)
	go func() {
		defer fs.wg.Done()
		err := cmd.Wait()
		closeStop(stop)
		p.mu.Lock()
		if !p.released {
			ch.end() // a wait on the pool slot (for its ready, or an assign's ack) returns
		}
		p.mu.Unlock()
		pool := &fs.run.pool
		pool.mu.Lock()
		parked := false
		for i, q := range pool.ready {
			if q == p {
				pool.ready = append(pool.ready[:i], pool.ready[i+1:]...)
				parked = true
				break
			}
		}
		pool.mu.Unlock()
		if parked {
			unix.Close(mp.sock)
			fs.freeSlot(idx) // died waiting for work
			return
		}
		fs.stubEnded(cmd.Process.Pid)
		if fs.isMainStub(cmd.Process.Pid) {
			fs.run.mainGone(err)
		}
	}()
	if err := ch.wait(); err != nil || ch.page.kind != msgReady {
		// It died before parking; nothing else will use the slot.
		unix.Close(mp.sock)
		select {
		case <-stop:
			fs.freeSlot(idx)
		default:
			unix.Kill(cmd.Process.Pid, unix.SIGKILL)
			<-stop
			fs.freeSlot(idx)
		}
		return nil
	}
	return p
}

// takePooled hands out a parked stub (nil if none) and starts its successor.
func (fs *dfs) takePooled() *pooledStub {
	pool := &fs.run.pool
	pool.mu.Lock()
	var p *pooledStub
	if n := len(pool.ready); n > 0 {
		p = pool.ready[n-1]
		pool.ready = pool.ready[:n-1]
	}
	pool.mu.Unlock()
	fs.fillPool()
	return p
}

// assignFork makes a parked stub the fork child on slot idx of the process
// whose stub is parent: it names them in the pool slot's scratch and waits for
// the ack, after which the stub works on slot idx and the pool slot is free.
// It returns false if the stub died first.
func (p *pooledStub) assignFork(fs *dfs, idx, parent, gpid int) bool {
	sc := fs.mem[p.slot*slotSize+scratchOff : p.slot*slotSize+scratchOff+24]
	put64(sc, uint64(idx*slotSize))
	put64(sc[8:], uint64(parent))
	put64(sc[16:], uint64(gpid))
	p.ch.reply(repForkChild)
	err := p.ch.wait()
	p.mu.Lock()
	p.released = true
	p.mu.Unlock()
	fs.freeSlot(p.slot)
	return err == nil && p.ch.page.kind == msgHostRet
}

var jobSeq atomic.Uint64

// assign gives a parked stub its program: it writes the job file, names it in
// the pool slot's scratch and waits for the stub's ack, after which the stub
// works on the target slot and the pool slot is free. It returns false if the
// stub died first.
func (p *pooledStub) assign(fs *dfs, fields []string) bool {
	f, err := os.CreateTemp(fs.tmpDir, fmt.Sprintf("lc-job-%d-*", jobSeq.Add(1)))
	if err != nil {
		return false
	}
	path := f.Name()
	defer os.Remove(path)
	_, werr := f.WriteString(strings.Join(fields, "\x00") + "\x00")
	if cerr := f.Close(); werr != nil || cerr != nil {
		return false
	}
	sc := fs.mem[p.slot*slotSize+scratchOff : p.slot*slotSize+scratchOff+scratchLen]
	if len(path)+1 > len(sc) {
		return false
	}
	copy(sc, path)
	sc[len(path)] = 0
	p.ch.reply(repExec)
	err = p.ch.wait()
	p.mu.Lock()
	p.released = true
	p.mu.Unlock()
	fs.freeSlot(p.slot)
	return err == nil && p.ch.page.kind == msgHostRet
}
