//go:build !amd64

package rewrite

func strayVec(code []byte) ([]int, bool) { return nil, false }
