//go:build darwin || linux || windows

package sandbox

import "testing"

// insn assembles one classic-BPF instruction into its 8 wire bytes.
func insn(code uint16, jt, jf uint8, k uint32) []byte {
	return []byte{
		byte(code), byte(code >> 8), jt, jf,
		byte(k), byte(k >> 8), byte(k >> 16), byte(k >> 24),
	}
}

func TestBpfRun(t *testing.T) {
	const nrGetpid = 39
	arch := auditArch()
	// Reject a foreign arch, allow getpid, EPERM everything else — the shape of
	// a real policy's prologue.
	prog := concat(
		insn(0x20, 0, 0, 4),    // A = arch
		insn(0x15, 1, 0, arch), // if A == arch: skip the kill
		insn(0x06, 0, 0, secRetKillProcess),
		insn(0x20, 0, 0, 0),        // A = nr
		insn(0x15, 0, 1, nrGetpid), // if A == getpid: allow
		insn(0x06, 0, 0, secRetAllow),
		insn(0x06, 0, 0, secRetErrno|1), // else EPERM
	)
	cases := []struct {
		name string
		d    seccompData
		want uint32
	}{
		{"getpid allowed", seccompData{nr: nrGetpid, arch: arch}, secRetAllow},
		{"other errno", seccompData{nr: 200, arch: arch}, secRetErrno | 1},
		{"foreign arch killed", seccompData{nr: nrGetpid, arch: 0xdead}, secRetKillProcess},
	}
	for _, c := range cases {
		if got := bpfRun(prog, &c.d); got != c.want {
			t.Errorf("%s: bpfRun = %#x, want %#x", c.name, got, c.want)
		}
	}
}

func TestBpfArgsAndJumps(t *testing.T) {
	arch := auditArch()
	// Allow ioctl only when arg1 (the request) has a bit set, as a policy that
	// screens an argument would: exercises a 64-bit arg's low word and JSET.
	const nrIoctl = 16
	prog := concat(
		insn(0x20, 0, 0, 0),       // A = nr
		insn(0x15, 0, 4, nrIoctl), // if nr != ioctl: jump to the trailing allow
		insn(0x20, 0, 0, 16),      // A = args[0] low word
		insn(0x45, 0, 1, 0x1),     // if A & 1: next; else errno
		insn(0x06, 0, 0, secRetAllow),
		insn(0x06, 0, 0, secRetErrno|13), // EACCES
		insn(0x06, 0, 0, secRetAllow),
	)
	allow := seccompData{nr: nrIoctl, arch: arch, args: [6]uint64{1}}
	if got := bpfRun(prog, &allow); got != secRetAllow {
		t.Errorf("ioctl arg0=1: got %#x, want allow", got)
	}
	deny := seccompData{nr: nrIoctl, arch: arch, args: [6]uint64{2}}
	if got := bpfRun(prog, &deny); got != secRetErrno|13 {
		t.Errorf("ioctl arg0=2: got %#x, want EACCES", got)
	}
	other := seccompData{nr: 99, arch: arch}
	if got := bpfRun(prog, &other); got != secRetAllow {
		t.Errorf("non-ioctl: got %#x, want allow", got)
	}
}

// TestBpfFullSet exercises instructions outside the old Chromium-only subset --
// scratch store/load (ST, LD|MEM), a register transfer (TAX) and a register
// multiply (MUL|X). Before the filter ran the real Linux set these silently fell
// through to "allow"; now they compute, so a policy built on them is enforced.
func TestBpfFullSet(t *testing.T) {
	arch := auditArch()
	// A = (nr stored to M[0], reloaded) * 2; allow iff that equals 80 (nr == 40).
	prog := concat(
		insn(0x20, 0, 0, 0),  // A = nr
		insn(0x02, 0, 0, 0),  // M[0] = A
		insn(0x00, 0, 0, 2),  // A = 2
		insn(0x07, 0, 0, 0),  // X = A  (TAX)
		insn(0x60, 0, 0, 0),  // A = M[0]  (== nr)
		insn(0x2c, 0, 0, 0),  // A = A * X  (MUL|X)
		insn(0x15, 0, 1, 80), // if A == 80: allow
		insn(0x06, 0, 0, secRetAllow),
		insn(0x06, 0, 0, secRetErrno|1), // else EPERM
	)
	if got := bpfRun(prog, &seccompData{nr: 40, arch: arch}); got != secRetAllow {
		t.Errorf("nr=40: got %#x, want allow", got)
	}
	if got := bpfRun(prog, &seccompData{nr: 39, arch: arch}); got != secRetErrno|1 {
		t.Errorf("nr=39: got %#x, want EPERM (was silently allowed before the fix)", got)
	}
}

// TestBpfFailClosed: an opcode the interpreter does not model must kill, never
// allow -- the opposite of the old default.
func TestBpfFailClosed(t *testing.T) {
	if got := bpfRun(insn(0x94, 0, 0, 0), &seccompData{}); got != secRetKillProcess {
		t.Errorf("unmodelled opcode: got %#x, want kill", got)
	}
}

// TestBpfValidate: accept every filter the kernel accepts, reject every one it
// rejects at install (EINVAL), so an unenforceable filter is never installed.
func TestBpfValidate(t *testing.T) {
	good := concat(
		insn(0x20, 0, 0, 0),           // LD nr
		insn(0x02, 0, 0, 15),          // ST M[15] (max slot)
		insn(0x2c, 0, 0, 0),           // MUL|X
		insn(0x05, 0, 0, 1),           // JA +1
		insn(0x06, 0, 0, secRetAllow), // (jump target)
		insn(0x06, 0, 0, secRetAllow),
	)
	if !bpfValidate(good) {
		t.Error("valid filter rejected")
	}
	bad := map[string][]byte{
		"unknown opcode":      insn(0xff, 0, 0, 0),
		"MOD not permitted":   insn(0x94, 0, 0, 0),
		"ABS load oob":        insn(0x20, 0, 0, seccompDataLen),
		"ABS load misaligned": insn(0x20, 0, 0, 2),
		"scratch slot oob":    insn(0x02, 0, 0, 16),
		"cond jump past end":  insn(0x15, 5, 0, 0),
		"JA past end":         insn(0x05, 0, 0, 9),
		"empty":               nil,
		"truncated":           insn(0x06, 0, 0, 0)[:7],
	}
	for name, p := range bad {
		if bpfValidate(p) {
			t.Errorf("%s: accepted, want rejected", name)
		}
	}
}

func concat(parts ...[]byte) []byte {
	var out []byte
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}
