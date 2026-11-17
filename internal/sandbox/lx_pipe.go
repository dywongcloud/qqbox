//go:build darwin || linux || windows

package sandbox

// pipeVector makes a vector one operation, rather than waiting separately on
// its elements. In particular a <= PIPE_BUF writev is indivisible.
func (g *dguest) pipeVector(d *dfile, v [][2]uint64, write bool) int64 {
	if d.fx.opath {
		return sErr(lxEBADF)
	}
	want := iovTotal(v)
	buf := make([]byte, min(want, ioChunk))
	if write {
		var e lxErrno
		buf, e = g.gather(v, 0, len(buf))
		if e != 0 {
			return sErr(e)
		}
	}
	var n int
	var err error
	if write {
		n, err = d.pipe.io.write(g, d, buf, d.nonblock.Load())
	} else {
		// Where the guest's buffer stops being writable, Linux's copy takes
		// the pipe's next chunk whole or not at all: here, what it holds.
		whole := false
		if p := g.writableVecPrefix(v, len(buf)); p < len(buf) {
			buf, whole = buf[:p], true
		}
		n, err = d.pipe.io.read(g, d, buf, d.nonblock.Load(), whole)
	}
	if n == 0 && err != nil {
		if err == errSigIntr {
			return restartSys
		}
		return sErr(osErrno(err))
	}
	if !write {
		if e := g.scatter(v, 0, buf[:n]); e != 0 {
			return sErr(e)
		}
	}
	return int64(n)
}
