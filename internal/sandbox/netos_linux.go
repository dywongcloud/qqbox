package sandbox

import "golang.org/x/sys/unix"

// Host socket details the shared socket code needs named.
const (
	hostSIOCATMARK    = unix.SIOCATMARK
	hostTCPKeepIdle   = unix.TCP_KEEPIDLE
	hostIPRecvPktinfo = unix.IP_PKTINFO
	hostSoLinger      = unix.SO_LINGER
)

// A write to a gone peer already fails with EPIPE: the Go runtime does not
// let SIGPIPE end the supervisor for a descriptor other than its stdout and
// stderr.
func sockNoSigpipe(int) {}

func pipeNoSigpipe(int) {}

// Linux ping sockets already deliver the bare ICMP message.
func icmpPingSock(int) {}

// tcpClosed reports whether TCP socket fd is in the CLOSED state (not still
// connecting).
func tcpClosed(fd int) bool {
	ti, err := unix.GetsockoptTCPInfo(fd, unix.IPPROTO_TCP, unix.TCP_INFO)
	return err == nil && ti.State == 7 // TCP_CLOSE
}
