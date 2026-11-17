//go:build (windows || darwin) && arm64

package sandbox

import (
	"encoding/binary"

	"github.com/samyfodil/laboon/internal/rewrite"
)

// anonAnalyze finds the svc sites of a page of code made at run time
// (anoncode.go) and the targets of its branches out of it.
func anonAnalyze(code []byte, entries []int) ([]int, []int) {
	return rewrite.A64AnonSites(code, entries)
}

// trapSiteIn and untrapSite turn the svc at off in code into the stub's trap
// (udf #0) and back.
func trapSiteIn(code []byte, off int) {
	if off%4 == 0 && off+4 <= len(code) && binary.LittleEndian.Uint32(code[off:]) == 0xd4000001 {
		binary.LittleEndian.PutUint32(code[off:], 0)
	}
}

func untrapSite(code []byte, off int) {
	if off%4 == 0 && off+4 <= len(code) && binary.LittleEndian.Uint32(code[off:]) == 0 {
		binary.LittleEndian.PutUint32(code[off:], 0xd4000001)
	}
}
