//go:build darwin

package sandbox

import "golang.org/x/sys/unix"

// Host socket details the shared socket code needs named.
const (
	hostSIOCATMARK    = 0x40047307
	hostTCPKeepIdle   = unix.TCP_KEEPALIVE
	hostIPRecvPktinfo = unix.IP_RECVPKTINFO
	hostSoLinger      = 0x1080 // SO_LINGER_SEC: seconds, as Linux's (SO_LINGER counts ticks)
)

// sockNoSigpipe and pipeNoSigpipe make a write to a gone peer fail with
// EPIPE instead of raising SIGPIPE in the supervisor.
func sockNoSigpipe(fd int) { unix.SetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_NOSIGPIPE, 1) }

func pipeNoSigpipe(fd int) { unix.FcntlInt(uintptr(fd), unix.F_SETNOSIGPIPE, 1) }

// icmpPingSock gives an AF_INET SOCK_DGRAM IPPROTO_ICMP host socket Linux
// ping-socket receive semantics. Darwin otherwise prepends the IPv4 header to
// each received ICMP message, which Linux ping(8) discards as a non-echo type;
// IP_STRIPHDR drops that header and leaves the ICMP message (checksum included)
// untouched.
func icmpPingSock(fd int) { unix.SetsockoptInt(fd, unix.IPPROTO_IP, unix.IP_STRIPHDR, 1) }

// tcpClosed reports whether TCP socket fd is in the CLOSED state (not still
// connecting).
func tcpClosed(fd int) bool {
	ti, err := unix.GetsockoptTCPConnectionInfo(fd, unix.IPPROTO_TCP, unix.TCP_CONNECTION_INFO)
	return err == nil && ti.State == 0
}
