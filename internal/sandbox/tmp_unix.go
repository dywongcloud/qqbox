//go:build darwin || linux

package sandbox

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"golang.org/x/sys/unix"
)

// A run's temp files (channel, site lists, upper layer, memfds, ...) live in
// a directory of its own inside lc-run-<pid> under TMPDIR, the supervisor
// process's, and its socket directories are /tmp/lcn.<pid>-*. The stubs'
// Seatbelt profile admits reads of their run's directory only. A supervisor
// killed outright cannot clean up after itself, so the first run in a later
// one removes what dead supervisors left behind, in the background: a /tmp
// with many entries takes a while to read.
var runTmp struct {
	once sync.Once
	dir  string
}

// tmpDir is the directory for this process's temp files ("" if it could not
// be made, which os.CreateTemp reads as TMPDIR).
func tmpDir() string {
	runTmp.once.Do(func() {
		go func() {
			sweepDead(os.TempDir(), "lc-run-")
			sweepDead("/tmp", "lcn.")
		}()
		d := filepath.Join(os.TempDir(), "lc-run-"+strconv.Itoa(os.Getpid()))
		if err := os.Mkdir(d, 0o700); err == nil || os.IsExist(err) {
			runTmp.dir = d
			// The stubs' Seatbelt profile admits resolved paths only: TMPDIR
			// is under the /var symlink.
			if r, err := filepath.EvalSymlinks(d); err == nil {
				runTmp.dir = r
			}
		}
	})
	return runTmp.dir
}

// newRunTmp makes a run's temp directory (mode 0700), resolved since the
// stubs' Seatbelt profile admits resolved paths only.
func newRunTmp() (string, error) {
	d, err := os.MkdirTemp(tmpDir(), "r-")
	if err != nil {
		return "", err
	}
	r, err := filepath.EvalSymlinks(d)
	if err != nil {
		os.RemoveAll(d)
		return "", err
	}
	return r, nil
}

// sweepDead removes the entries of dir named prefix<pid>[-...] whose
// process is gone.
func sweepDead(dir, prefix string) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range ents {
		rest, ok := strings.CutPrefix(e.Name(), prefix)
		if !ok {
			continue
		}
		pid, err := strconv.Atoi(strings.SplitN(rest, "-", 2)[0])
		if err != nil || pid == os.Getpid() || unix.Kill(pid, 0) != unix.ESRCH {
			continue
		}
		os.RemoveAll(filepath.Join(dir, e.Name()))
	}
}

// netTmpPrefix names this process's socket directories under /tmp.
func netTmpPrefix() string {
	return fmt.Sprintf("lcn.%d-", os.Getpid())
}
