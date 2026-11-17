//go:build darwin

package sandbox

import (
	"errors"

	"golang.org/x/sys/unix"
)

// Darwin and Linux share errno numbers 1-34 (EPERM..ERANGE) and diverge above.
// Every error the supervisor hands the guest goes through linuxErrno.
var darwinToLinuxErrno = map[unix.Errno]lxErrno{
	unix.EAGAIN:          11,
	unix.EINPROGRESS:     115,
	unix.EALREADY:        114,
	unix.ENOTSOCK:        88,
	unix.EDESTADDRREQ:    89,
	unix.EMSGSIZE:        90,
	unix.EPROTOTYPE:      91,
	unix.ENOPROTOOPT:     92,
	unix.EPROTONOSUPPORT: 93,
	unix.ESOCKTNOSUPPORT: 94,
	unix.ENOTSUP:         95,
	unix.EOPNOTSUPP:      95,
	unix.EPFNOSUPPORT:    96,
	unix.EAFNOSUPPORT:    97,
	unix.EADDRINUSE:      98,
	unix.EADDRNOTAVAIL:   99,
	unix.ENETDOWN:        100,
	unix.ENETUNREACH:     101,
	unix.ENETRESET:       102,
	unix.ECONNABORTED:    103,
	unix.ECONNRESET:      104,
	unix.ENOBUFS:         105,
	unix.EISCONN:         106,
	unix.ENOTCONN:        107,
	unix.ESHUTDOWN:       108,
	unix.ETOOMANYREFS:    109,
	unix.ETIMEDOUT:       110,
	unix.ECONNREFUSED:    111,
	unix.ELOOP:           40,
	unix.ENAMETOOLONG:    36,
	unix.EHOSTDOWN:       112,
	unix.EHOSTUNREACH:    113,
	unix.ENOTEMPTY:       39,
	unix.EUSERS:          87,
	unix.EDQUOT:          122,
	unix.ESTALE:          116,
	unix.ENOLCK:          37,
	unix.ENOSYS:          38,
	unix.EOVERFLOW:       75,
	unix.ECANCELED:       125,
	unix.EIDRM:           43,
	unix.ENOMSG:          42,
	unix.EILSEQ:          84,
	unix.EBADMSG:         74,
	unix.EMULTIHOP:       72,
	unix.ENODATA:         61,
	unix.ENOLINK:         67,
	unix.ENOSR:           63,
	unix.ENOSTR:          60,
	unix.EPROTO:          71,
	unix.ETIME:           62,
	unix.ENOTRECOVERABLE: 131,
	unix.EOWNERDEAD:      130,
}

// hostErrno maps a Darwin errno to the guest's Linux number. Darwin-only
// errnos with no Linux counterpart fall back to EIO.
func hostErrno(e unix.Errno) lxErrno {
	if e <= unix.ERANGE || e == unix.Errno(errRestart) || e == unix.Errno(errRestartNoHand) {
		return lxErrno(e)
	}
	if l, ok := darwinToLinuxErrno[e]; ok {
		return l
	}
	return lxEIO
}

// hostErrnoOf is the Linux errno of a host error that carries one.
func hostErrnoOf(err error) (lxErrno, bool) {
	var e unix.Errno
	if errors.As(err, &e) {
		return hostErrno(e), true
	}
	return 0, false
}

// linuxRet translates a host syscall result (Darwin convention after the
// stub's negation: -errno on failure) into the guest's Linux convention.
func linuxRet(r uint64) uint64 {
	if v := int64(r); v < 0 && v > -4096 {
		return uint64(-hostErrno(unix.Errno(-v)))
	}
	return r
}
