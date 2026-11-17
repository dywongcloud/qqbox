//go:build (windows || darwin) && amd64

package sandbox

import "github.com/samyfodil/laboon/internal/rewrite"

// anonAnalyze finds the syscall sites of a page of code made at run time
// (anoncode.go) and the targets of its branches out of it.
func anonAnalyze(code []byte, entries []int) ([]int, []int) { return rewrite.AnonSites(code, entries) }

// trapSiteIn and untrapSite turn the syscall at off in code into the stub's
// trap (ud2) and back.
func trapSiteIn(code []byte, off int) {
	if off+2 <= len(code) && code[off] == 0x0f && code[off+1] == 0x05 {
		code[off+1] = 0x0b
	}
}

func untrapSite(code []byte, off int) {
	if off+2 <= len(code) && code[off] == 0x0f && code[off+1] == 0x0b {
		code[off+1] = 0x05
	}
}
