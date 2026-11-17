package rewrite

// straySites returns, in ascending order, the offset of every `0f 05` byte pair
// in code -- every syscall-shaped two bytes, whether real code or data. Apply
// uses it to report the sites the rewrite did not cover (w.Stray).
//
// It is a flat byte scan over the whole image, so on amd64 it takes an SSE2
// fast path (stray_amd64.s) that tests 16 bytes at a time; the scalar path here
// is always correct and is what non-amd64 builds use (an arm64 guest's stray
// syscalls are svc words, found by the arm64 scans, not this).
func straySites(code []byte) []int {
	if s, ok := strayVec(code); ok {
		return s
	}
	return strayScalar(code)
}

func strayScalar(code []byte) []int {
	var out []int
	for i := 0; i+1 < len(code); i++ {
		if code[i] == 0x0f && code[i+1] == 0x05 {
			out = append(out, i)
		}
	}
	return out
}
