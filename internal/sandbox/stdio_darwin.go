//go:build darwin

package sandbox

import (
	"fmt"
	"os"
	"strings"

	"golang.org/x/sys/unix"
)

// kickSignal has a stub route its pending signals to its threads.
const kickSignal = unix.SIGUSR1

const (
	ioctlGetTermios  = unix.TIOCGETA
	ioctlSetTermiosW = unix.TIOCSETAW
)

func isHostTTY(fd int) bool {
	_, err := unix.IoctlGetTermios(fd, unix.TIOCGETA)
	return err == nil
}

// hostTTYName returns the host device path of the terminal open at fd.
func hostTTYName(fd int) (string, error) {
	if err := initTTYLibc(); err != nil {
		return "", err
	}
	var buf [128]byte
	if r := libcTtynameR(int32(fd), &buf[0], uint64(len(buf))); r != 0 {
		return "", unix.Errno(r)
	}
	return ttyCStr(buf[:]), nil
}

func ttyCStr(b []byte) string {
	if i := indexByte(b, 0); i >= 0 {
		return string(b[:i])
	}
	return string(b)
}

// hostPtsPath is the host path of pty slave n.
func hostPtsPath(n int) string { return fmt.Sprintf("/dev/ttys%03d", n) }

func hostPtsNum(host string) (int, bool) {
	s, ok := strings.CutPrefix(host, "/dev/ttys")
	if !ok || s == "" {
		return 0, false
	}
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + int(c-'0')
	}
	return n, true
}

// ttyRdev is the Linux st_rdev of a host terminal or well-known device node
// (Darwin numbers them differently), or false for any other device.
func ttyRdev(fi os.FileInfo) (uint64, bool) {
	if n, ok := hostPtsNum("/dev/" + fi.Name()); ok {
		return lxMakedev(136, uint64(n)), true
	}
	switch fi.Name() {
	case "null":
		return lxMakedev(1, 3), true
	case "zero":
		return lxMakedev(1, 5), true
	case "random":
		return lxMakedev(1, 8), true
	case "urandom":
		return lxMakedev(1, 9), true
	case "tty":
		return lxMakedev(5, 0), true
	case "ptmx":
		return lxMakedev(5, 2), true
	}
	return 0, false
}

// hostPtyOpen opens a host pty master, unlocked, and names its slave.
func hostPtyOpen() (*os.File, string, lxErrno) {
	if err := initTTYLibc(); err != nil {
		return nil, "", lxENODEV
	}
	f, err := os.OpenFile("/dev/ptmx", os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		return nil, "", osErrno(err)
	}
	fd := hostFd(f)
	var buf [128]byte
	if libcGrantpt(int32(fd)) != 0 || libcUnlockpt(int32(fd)) != 0 || libcPtsnameR(int32(fd), &buf[0], uint64(len(buf))) != 0 {
		f.Close()
		return nil, "", lxEIO
	}
	return f, ttyCStr(buf[:]), 0
}
