//go:build linux

package main

import (
	"fmt"
	"net"
	"os"

	"golang.org/x/sys/unix"
)

// setupConsole opens a pty, sends the master end to the engine over the console
// socket (SCM_RIGHTS, as runc does for a terminal container), and returns the
// slave for the container's stdio. The engine drives the terminal through the
// master it now holds.
func setupConsole(socket string) (*os.File, error) {
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR|unix.O_NOCTTY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	if err := unix.IoctlSetPointerInt(int(master.Fd()), unix.TIOCSPTLCK, 0); err != nil { // unlockpt
		master.Close()
		return nil, err
	}
	n, err := unix.IoctlGetInt(int(master.Fd()), unix.TIOCGPTN)
	if err != nil {
		master.Close()
		return nil, err
	}
	slave, err := os.OpenFile(fmt.Sprintf("/dev/pts/%d", n), os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		master.Close()
		return nil, err
	}
	if err := sendFD(socket, master); err != nil {
		master.Close()
		slave.Close()
		return nil, err
	}
	master.Close() // the engine holds it now
	return slave, nil
}

// sendFD connects to the console unix socket and passes f's descriptor to the
// engine as a control message.
func sendFD(socket string, f *os.File) error {
	conn, err := net.Dial("unix", socket)
	if err != nil {
		return err
	}
	defer conn.Close()
	uf, err := conn.(*net.UnixConn).File()
	if err != nil {
		return err
	}
	defer uf.Close()
	return unix.Sendmsg(int(uf.Fd()), []byte("terminal"), unix.UnixRights(int(f.Fd())), nil, 0)
}
