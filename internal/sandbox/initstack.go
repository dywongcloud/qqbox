package sandbox

import "crypto/rand"

// layoutStack lays out argc, argv, envp and auxv below top, as the kernel
// does for execve, and returns the stack pointer and the bytes from it to top.
// auxv holds the caller's entries; AT_PLATFORM, AT_RANDOM and AT_EXECFN,
// which point into the block, are added here.
func layoutStack(top uint64, args, env []string, plat string, auxv []uint64) (uint64, []byte) {
	var strs []byte
	var argOff, envOff []int
	for _, a := range args {
		argOff = append(argOff, len(strs))
		strs = append(append(strs, a...), 0)
	}
	for _, e := range env {
		envOff = append(envOff, len(strs))
		strs = append(append(strs, e...), 0)
	}
	platOff := len(strs)
	strs = append(append(strs, plat...), 0)

	strBase := (top - uint64(len(strs))) &^ 15
	random := strBase - 16

	var vec []uint64
	vec = append(vec, uint64(len(argOff)))
	for _, o := range argOff {
		vec = append(vec, strBase+uint64(o))
	}
	vec = append(vec, 0)
	for _, o := range envOff {
		vec = append(vec, strBase+uint64(o))
	}
	vec = append(vec, 0)
	vec = append(vec, auxv...)
	vec = append(vec,
		15, strBase+uint64(platOff), // AT_PLATFORM
		25, random, // AT_RANDOM
		31, strBase+uint64(argOff[0]), // AT_EXECFN
		0, 0) // AT_NULL

	sp := (random - uint64(len(vec))*8) &^ 15
	buf := make([]byte, top-sp)
	for i, v := range vec {
		putU64(buf[i*8:], v)
	}
	rand.Read(buf[random-sp : random-sp+16])
	copy(buf[strBase-sp:], strs)
	return sp, buf
}

func putU64(b []byte, v uint64) {
	for i := range 8 {
		b[i] = byte(v >> (8 * i))
	}
}
