package sandbox

import (
	"path/filepath"
	"strconv"

	"golang.org/x/sys/unix"
)

// A guest AF_UNIX path is bound and reached through descriptors opened with
// RESOLVE_NO_SYMLINKS, never by its host path: a symlink the guest swaps in
// between resolving the path and the host call cannot lead out of the
// layer, and sun_path stays short (/proc/self/fd/N[/name]).

func (n *dnet) bindUnix(fd int, host string) error {
	d, err := unix.Openat2(unix.AT_FDCWD, filepath.Dir(host), &unix.OpenHow{
		Flags: unix.O_PATH | unix.O_DIRECTORY | unix.O_CLOEXEC, Resolve: unix.RESOLVE_NO_SYMLINKS})
	if err != nil {
		return err
	}
	defer unix.Close(d)
	name := "/proc/self/fd/" + strconv.Itoa(d) + "/" + filepath.Base(host)
	if len(name) >= lxSunPathMax {
		return unix.ENAMETOOLONG
	}
	return unix.Bind(fd, &unix.SockaddrUnix{Name: name})
}

// viaShort runs op with an address reaching the socket file host.
func (n *dnet) viaShort(host string, op func(sa *unix.SockaddrUnix) error) error {
	f, err := unix.Openat2(unix.AT_FDCWD, host, &unix.OpenHow{
		Flags: unix.O_PATH | unix.O_NOFOLLOW | unix.O_CLOEXEC, Resolve: unix.RESOLVE_NO_SYMLINKS})
	if err != nil {
		return err
	}
	defer unix.Close(f)
	return op(&unix.SockaddrUnix{Name: "/proc/self/fd/" + strconv.Itoa(f)})
}
