package sandbox

import (
	"errors"

	"golang.org/x/sys/unix"
)

// The host is Linux: its errno numbers are the guest's.

func hostErrno(e unix.Errno) lxErrno { return lxErrno(e) }

// hostErrnoOf extracts the host errno from err, if it carries one.
func hostErrnoOf(err error) (lxErrno, bool) {
	var e unix.Errno
	if errors.As(err, &e) {
		return lxErrno(e), true
	}
	return 0, false
}
