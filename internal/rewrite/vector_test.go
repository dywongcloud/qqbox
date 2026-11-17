package rewrite

import (
	"runtime"
	"testing"
)

func TestVectorDetectAndToggle(t *testing.T) {
	lvl := VectorScanLevel()
	switch runtime.GOARCH {
	case "amd64":
		if lvl != "sse2" {
			t.Fatalf("amd64 host should be sse2, got %q", lvl)
		}
	case "arm64":
		if lvl != "neon" {
			t.Fatalf("arm64 host should be neon, got %q", lvl)
		}
	}
	SetVectorScan(false)
	if VectorScanLevel() != "scalar" {
		t.Fatalf("disabled should force scalar, got %q", VectorScanLevel())
	}
	SetVectorScan(true)
	if VectorScanLevel() != lvl {
		t.Fatalf("re-enabled should restore %q, got %q", lvl, VectorScanLevel())
	}
}
