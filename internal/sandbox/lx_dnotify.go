//go:build darwin || linux || windows

package sandbox

import (
	"sync"
)

// dnotify (fcntl F_NOTIFY) rides on the inotify machinery: a directory fd's
// notification is a private inotify instance with one watch on the directory,
// whose events signal the process that asked (SIGIO, or the F_SETSIG signal)
// instead of queueing records.

const (
	lxFNotify = 1026

	dnAccess    = 0x1
	dnModify    = 0x2
	dnCreate    = 0x4
	dnDelete    = 0x8
	dnRename    = 0x10
	dnAttrib    = 0x20
	dnMultishot = 0x80000000

	sigIO   = 29
	pollMsg = 3
)

type dnotify struct {
	tgid, fd int
	sig      int
}

func (g *dguest) fcntlNotify(d *dfile, fd int, arg uint64) int64 {
	arg = uint64(uint32(arg))
	dnotifyDrop(d)
	if arg&^dnMultishot == 0 {
		return 0
	}
	if !d.isDir {
		return sErr(lxENOTDIR)
	}
	var mask uint32
	for _, m := range [...]struct{ dn, in uint32 }{
		{dnAccess, inAccess},
		{dnModify, inModify},
		{dnCreate, inCreate | inMovedTo},
		{dnDelete, inDelete | inMovedFrom},
		{dnRename, inMovedFrom | inMovedTo},
		{dnAttrib, inAttrib},
	} {
		if uint32(arg)&m.dn != 0 {
			mask |= m.in
		}
	}
	if arg&dnMultishot == 0 {
		mask |= inOneshot
	}
	in := &inotify{watch: map[int32]*iwatch{}, fs: g.dfs, next: 2,
		dn: &dnotify{tgid: g.tgid, fd: fd, sig: int(d.fx.sigio.Load())}}
	in.cond = sync.NewCond(&in.mu)
	in.watch[1] = &iwatch{wd: 1, gp: d.name(), mask: mask}
	g.ov.nwatch.Add(1)
	g.ov.inotifyMu.Lock()
	g.ov.watchers = append(g.ov.watchers, in)
	g.ov.inotifyMu.Unlock()
	if old := d.fx.dn.Swap(in); old != nil {
		old.close()
	}
	return 0
}

func dnotifyDrop(d *dfile) {
	if in := d.fx.dn.Swap(nil); in != nil {
		in.close()
	}
}

func (dn *dnotify) send(fs *dfs) {
	sr := fs.sigs()
	if sr == nil {
		return
	}
	p := sr.proc(dn.tgid)
	if p == nil {
		return
	}
	si := sigInfo{code: siKernel}
	sig := dn.sig
	if sig == 0 {
		sig = sigIO
	} else {
		si = sigInfo{code: pollMsg, b: uint64(dn.fd)}
	}
	sr.send(p, nil, sig, si, nil)
}
