//go:build linux || windows

package sandbox

import (
	"io"
	"os"
)

// execSrc is the program an execve runs: a guest path read through the
// overlay, or the open file execveat(AT_EMPTY_PATH) names.
type execSrc struct {
	gp  string
	f   *os.File
	own bool // f is a reopened handle the loader must close once it has dup'd it
}

func (g *dguest) execSource(gp string, f *os.File) (execSrc, lxErrno) {
	if f != nil {
		return execSrc{f: f}, 0
	}
	fi, e := g.lstatGuest(gp)
	if e != 0 {
		return execSrc{}, e
	}
	if !fi.Mode().IsRegular() {
		return execSrc{}, lxEACCES
	}
	return execSrc{gp: gp}, 0
}

func (g *dguest) execSrcHead(s execSrc) ([]byte, lxErrno) {
	b, e := g.execSrcRead(s, binprmBuf)
	return b, e
}

// execSrcRead reads up to n bytes of the program (all of it if n < 0).
func (g *dguest) execSrcRead(s execSrc, n int) ([]byte, lxErrno) {
	f := s.f
	if f == nil {
		rp, e := g.resolve(s.gp, true)
		if e != 0 {
			return nil, e
		}
		if f, e = g.openFile(rp); e != 0 {
			return nil, e
		}
		defer f.Close()
	}
	var r io.Reader = io.NewSectionReader(f, 0, 1<<62)
	if n >= 0 {
		r = io.LimitReader(r, int64(n))
	}
	b, err := io.ReadAll(r)
	if err != nil {
		return nil, osErrno(err)
	}
	return b, 0
}

// markSrcOwn records that s.f is a reopened handle the loader must close.
func markSrcOwn(s *execSrc, own bool) { s.own = own }
