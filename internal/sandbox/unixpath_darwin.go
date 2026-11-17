//go:build darwin

package sandbox

import (
	"os"

	"golang.org/x/sys/unix"
)

// bindUnix binds fd to a host path. Darwin's sun_path holds 103 bytes, and
// the overlay's upper layer lives under a long temp dir, so a longer path is
// bound at a short name and renamed into place (the socket stays reachable
// through its vnode).
func (n *dnet) bindUnix(fd int, host string) error {
	if len(host) < dwSunPathMax {
		return unix.Bind(fd, &unix.SockaddrUnix{Name: host})
	}
	if _, err := os.Lstat(host); err == nil {
		return unix.EADDRINUSE
	}
	tmp := n.tmpName()
	if err := unix.Bind(fd, &unix.SockaddrUnix{Name: tmp}); err != nil {
		return err
	}
	if err := os.Rename(tmp, host); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// viaShort runs op with a Darwin-sized name for host: the path itself, or a
// short symlink to it.
func (n *dnet) viaShort(host string, op func(sa *unix.SockaddrUnix) error) error {
	if len(host) < dwSunPathMax {
		return op(&unix.SockaddrUnix{Name: host})
	}
	tmp := n.tmpName()
	if err := os.Symlink(host, tmp); err != nil {
		return err
	}
	defer os.Remove(tmp)
	return op(&unix.SockaddrUnix{Name: tmp})
}
