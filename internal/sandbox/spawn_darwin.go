//go:build darwin

package sandbox

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"sync"
	"syscall"
	"unsafe"

	"github.com/go-webgpu/goffi/types"
)

// stubCmd starts a stub process with posix_spawn rather than os/exec, for what
// a fork and exec cannot ask of the new process: no ASLR, so every stub's text
// lies at the same addresses and a stub started as a fork child runs its
// parent's islands (which branch to the stub's trampoline at an absolute
// address).
type stubCmd struct {
	Path      string
	Args      []string
	Env       []string
	Files     []*os.File // the stub's fds 0, 1, 2, ...: stdio, the fdlink pairing, the lifeline
	Pgid      int        // the process group to join; 0 starts a new one
	Bootstrap uint32     // a send right for the task's bootstrap port (the broker's), if any
	Process   *os.Process
}

const (
	spawnSetpgroup      = 0x0002
	spawnSetsigdef      = 0x0004
	spawnSetsigmask     = 0x0008
	spawnDisableASLR    = 0x0100
	spawnCloexecDefault = 0x4000
)

var (
	spawnOnce                                                         sync.Once
	spawnErr                                                          error
	saInit, saDestroy, saFlags, saPgroup, saSigmask, saSigdef, saPort *ffiCall
	faInit, faDestroy, faDup2, faClose, psSpawn                       *ffiCall
)

func initSpawn() error {
	spawnOnce.Do(func() {
		lib, err := ffiOpenLibSystem()
		if err != nil {
			spawnErr = err
			return
		}
		for _, p := range []struct {
			c    **ffiCall
			name string
			args []*types.TypeDescriptor
		}{
			{&saInit, "posix_spawnattr_init", []*types.TypeDescriptor{ffiPtr}},
			{&saDestroy, "posix_spawnattr_destroy", []*types.TypeDescriptor{ffiPtr}},
			{&saFlags, "posix_spawnattr_setflags", []*types.TypeDescriptor{ffiPtr, ffiS16}},
			{&saPgroup, "posix_spawnattr_setpgroup", []*types.TypeDescriptor{ffiPtr, ffiS32}},
			{&saSigmask, "posix_spawnattr_setsigmask", []*types.TypeDescriptor{ffiPtr, ffiPtr}},
			{&saSigdef, "posix_spawnattr_setsigdefault", []*types.TypeDescriptor{ffiPtr, ffiPtr}},
			{&saPort, "posix_spawnattr_setspecialport_np", []*types.TypeDescriptor{ffiPtr, ffiU32, ffiS32}},
			{&faInit, "posix_spawn_file_actions_init", []*types.TypeDescriptor{ffiPtr}},
			{&faDestroy, "posix_spawn_file_actions_destroy", []*types.TypeDescriptor{ffiPtr}},
			{&faDup2, "posix_spawn_file_actions_adddup2", []*types.TypeDescriptor{ffiPtr, ffiS32, ffiS32}},
			{&faClose, "posix_spawn_file_actions_addclose", []*types.TypeDescriptor{ffiPtr, ffiS32}},
			{&psSpawn, "posix_spawn", []*types.TypeDescriptor{ffiPtr, ffiPtr, ffiPtr, ffiPtr, ffiPtr, ffiPtr}},
		} {
			if *p.c, spawnErr = ffiPrep(lib, p.name, ffiS32, p.args...); spawnErr != nil {
				return
			}
		}
	})
	return spawnErr
}

// cstrings is a NULL-terminated array of C strings for posix_spawn's argv or
// envp; keep holds the bytes alive.
func cstrings(ss []string) (arr []*byte, keep [][]byte) {
	for _, s := range ss {
		b := append([]byte(s), 0)
		keep = append(keep, b)
		arr = append(arr, &b[0])
	}
	return append(arr, nil), keep
}

// Start starts the stub. Each of Files lands on its own index in the child
// through a high number first, so one source that is another's target
// number is not overwritten before it is moved; nothing else is inherited.
func (c *stubCmd) Start() error {
	if err := initSpawn(); err != nil {
		return err
	}
	// posix_spawnattr_t and posix_spawn_file_actions_t are pointers the
	// calls fill in; each call takes the address of one (ap, fp).
	var attr, fa unsafe.Pointer
	ap, fp := unsafe.Pointer(&attr), unsafe.Pointer(&fa)
	var rc int32
	saInit.call(unsafe.Pointer(&rc), unsafe.Pointer(&ap))
	if rc != 0 {
		return fmt.Errorf("posix_spawnattr_init: %w", syscall.Errno(rc))
	}
	defer saDestroy.call(unsafe.Pointer(&rc), unsafe.Pointer(&ap))
	faInit.call(unsafe.Pointer(&rc), unsafe.Pointer(&fp))
	if rc != 0 {
		return fmt.Errorf("posix_spawn_file_actions_init: %w", syscall.Errno(rc))
	}
	defer faDestroy.call(unsafe.Pointer(&rc), unsafe.Pointer(&fp))

	flags := int16(spawnSetpgroup | spawnSetsigdef | spawnSetsigmask | spawnDisableASLR | spawnCloexecDefault)
	saFlags.call(unsafe.Pointer(&rc), unsafe.Pointer(&ap), unsafe.Pointer(&flags))
	pgid := int32(c.Pgid)
	saPgroup.call(unsafe.Pointer(&rc), unsafe.Pointer(&ap), unsafe.Pointer(&pgid))
	var none, all uint32 = 0, ^uint32(0)
	sigmask, sigdef := unsafe.Pointer(&none), unsafe.Pointer(&all)
	saSigmask.call(unsafe.Pointer(&rc), unsafe.Pointer(&ap), unsafe.Pointer(&sigmask))
	saSigdef.call(unsafe.Pointer(&rc), unsafe.Pointer(&ap), unsafe.Pointer(&sigdef))
	if c.Bootstrap != 0 {
		port, which := c.Bootstrap, int32(4) // TASK_BOOTSTRAP_PORT
		saPort.call(unsafe.Pointer(&rc), unsafe.Pointer(&ap), unsafe.Pointer(&port), unsafe.Pointer(&which))
		if rc != 0 {
			return fmt.Errorf("posix_spawnattr_setspecialport_np: %w", syscall.Errno(rc))
		}
	}

	top := int32(len(c.Files))
	for _, f := range c.Files {
		top = max(top, int32(f.Fd())+1)
	}
	for i, f := range c.Files {
		src, tmp := int32(f.Fd()), top+int32(i)
		faDup2.call(unsafe.Pointer(&rc), unsafe.Pointer(&fp), unsafe.Pointer(&src), unsafe.Pointer(&tmp))
	}
	for i := range c.Files {
		tmp, dst := top+int32(i), int32(i)
		faDup2.call(unsafe.Pointer(&rc), unsafe.Pointer(&fp), unsafe.Pointer(&tmp), unsafe.Pointer(&dst))
		faClose.call(unsafe.Pointer(&rc), unsafe.Pointer(&fp), unsafe.Pointer(&tmp))
	}

	path := append([]byte(c.Path), 0)
	argv, ka := cstrings(c.Args)
	envp, ke := cstrings(c.Env)
	var pid int32
	pp, pathp, argvp, envpp := unsafe.Pointer(&pid), unsafe.Pointer(&path[0]), unsafe.Pointer(&argv[0]), unsafe.Pointer(&envp[0])
	psSpawn.call(unsafe.Pointer(&rc), unsafe.Pointer(&pp), unsafe.Pointer(&pathp), unsafe.Pointer(&fp), unsafe.Pointer(&ap), unsafe.Pointer(&argvp), unsafe.Pointer(&envpp))
	runtime.KeepAlive(path)
	runtime.KeepAlive(ka)
	runtime.KeepAlive(ke)
	runtime.KeepAlive(c.Files)
	if rc != 0 {
		return &os.PathError{Op: "posix_spawn", Path: c.Path, Err: syscall.Errno(rc)}
	}
	p, err := os.FindProcess(int(pid))
	if err != nil {
		return err
	}
	c.Process = p
	return nil
}

// Wait waits for the stub to end. A status other than a clean exit 0 is an
// *exec.ExitError, as exec.Cmd.Wait gives.
func (c *stubCmd) Wait() error {
	st, err := c.Process.Wait()
	if err != nil {
		return err
	}
	if !st.Success() {
		return &exec.ExitError{ProcessState: st}
	}
	return nil
}
