//go:build windows

package sandbox

import (
	"os"
	"syscall"
)

// linuxSignal maps an os.Signal to the guest's Linux signal number. Windows
// defines only a subset of the signal constants; the rest are not expressible
// as an os.Signal there.
func linuxSignal(sig os.Signal) (int, error) {
	s, ok := sig.(syscall.Signal)
	if !ok {
		return 0, syscall.EINVAL
	}
	switch s {
	case 0:
		return 0, nil
	case syscall.SIGHUP:
		return 1, nil
	case syscall.SIGINT:
		return 2, nil
	case syscall.SIGQUIT:
		return 3, nil
	case syscall.SIGABRT:
		return 6, nil
	case syscall.SIGKILL:
		return 9, nil
	case syscall.SIGSEGV:
		return 11, nil
	case syscall.SIGPIPE:
		return 13, nil
	case syscall.SIGALRM:
		return 14, nil
	case syscall.SIGTERM:
		return 15, nil
	}
	return 0, syscall.EINVAL
}
