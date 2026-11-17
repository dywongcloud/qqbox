//go:build darwin || linux

package sandbox

import (
	"os"
	"syscall"
)

// linuxSignal maps an os.Signal to the guest's Linux signal number. The guest
// runs the Linux ABI whatever the host, so the mapping is by signal identity,
// not the host's numeric value (which differs for e.g. SIGUSR1 on Darwin).
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
	case syscall.SIGUSR1:
		return 10, nil
	case syscall.SIGSEGV:
		return 11, nil
	case syscall.SIGUSR2:
		return 12, nil
	case syscall.SIGPIPE:
		return 13, nil
	case syscall.SIGALRM:
		return 14, nil
	case syscall.SIGTERM:
		return 15, nil
	case syscall.SIGCHLD:
		return 17, nil
	case syscall.SIGCONT:
		return 18, nil
	case syscall.SIGSTOP:
		return 19, nil
	case syscall.SIGTSTP:
		return 20, nil
	case syscall.SIGWINCH:
		return 28, nil
	}
	return 0, syscall.EINVAL
}
