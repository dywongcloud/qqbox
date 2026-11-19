//go:build darwin || linux || windows

package sandbox

// The kernel keyring: a container runtime joins a fresh session keyring for the
// container (crun does this before start). qqbox has no keyring subsystem, so
// it fakes just enough for that to succeed: a join returns a keyring id and the
// operations a runtime then performs on it are accepted. No keys are stored.

const (
	lxKeyctl = 219

	keyctlJoinSessionKeyring = 1
	keyctlSetperm            = 5
	keyctlDescribe           = 6
	keyctlClear              = 7
	keyctlSetTimeout         = 15

	fakeKeyring = 0x3fffffff // a plausible, stable keyring id
)

func init() {
	syscalls[lxKeyctl] = func(g *dguest, a [6]uint64) int64 { return g.keyctl(a[0], a[1], a[2], a[3]) }
}

func (g *dguest) keyctl(cmd, key, buf, size uint64) int64 {
	switch cmd {
	case keyctlJoinSessionKeyring:
		return fakeKeyring // a runtime joins a new session keyring for the container
	case keyctlDescribe:
		// Describe the virtual keyring as the kernel would: a NUL-terminated
		// "type;uid;gid;perm;description". runc (docker's runtime) reads this,
		// parses the perm field, and re-sets it; without a real string its
		// KeyctlString helper slices buffer[:len-1] and panics on len 0. Return
		// the full length (so the caller sizes its buffer) and fill it when given
		// one. The perm is the usual session-keyring value (possessor + user rwx).
		desc := []byte("keyring;0;0;3f1f0000;_ses\x00")
		if buf != 0 && size > 0 {
			n := uint64(len(desc))
			if n > size {
				n = size
			}
			if err := g.writeMem(buf, desc[:n]); err != nil {
				return sErr(lxEFAULT)
			}
		}
		return int64(len(desc))
	case keyctlSetperm, keyctlClear, keyctlSetTimeout:
		// Attribute operations on the keyring itself: they need no stored keys, so
		// they legitimately succeed on the (empty) virtual session keyring. These
		// are the ones container runtimes use after joining.
		return 0
	}
	// Any other operation would act on keys that this virtual keyring does not
	// hold. Report it unsupported rather than fake success -- a guest must not be
	// told a key operation worked when nothing happened.
	return sErr(lxEOPNOTSUPP)
}
