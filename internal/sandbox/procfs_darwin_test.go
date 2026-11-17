//go:build darwin

package sandbox

import (
	"strings"
	"testing"
)

func TestCutVmas(t *testing.T) {
	vs := []vma{{lo: 0x1000, hi: 0x5000, path: "/lib/x", off: 0}}
	got := cutVmas(vs, 0x2000, 0x3000)
	if len(got) != 2 || got[0].lo != 0x1000 || got[0].hi != 0x2000 || got[1].lo != 0x3000 || got[1].hi != 0x5000 || got[1].off != 0x2000 {
		t.Fatalf("split: %+v", got)
	}
	if got := cutVmas(vs, 0, 0x10000); len(got) != 0 {
		t.Fatalf("cover: %+v", got)
	}
	if got := cutVmas(vs, 0x5000, 0x6000); len(got) != 1 || got[0].hi != 0x5000 {
		t.Fatalf("disjoint: %+v", got)
	}
}

func TestLxMakedev(t *testing.T) {
	for _, c := range []struct{ major, minor, want uint64 }{
		{1, 3, 0x103}, {5, 0, 0x500}, {136, 0, 0x8800}, {259, 0x12345, 0x45 | 259<<8 | 0x12300<<12},
	} {
		if got := lxMakedev(c.major, c.minor); got != c.want {
			t.Errorf("makedev(%d,%d) = %#x, want %#x", c.major, c.minor, got, c.want)
		}
	}
	if got := darwinRdev(3<<24 | 2); got != lxMakedev(3, 2) {
		t.Errorf("darwinRdev = %#x", got)
	}
}

func TestCpuinfoFormat(t *testing.T) {
	s := string(procCpuinfo())
	h := hostCPUInfo()
	if strings.Count(s, "processor\t: ") != h.n {
		t.Fatalf("want %d processor blocks:\n%s", h.n, s)
	}
	for _, k := range []string{"BogoMIPS\t: ", "Features\t: fp asimd", "CPU implementer\t: 0x61\n", "CPU architecture: 8\n", "CPU part\t: 0x", "CPU revision\t: 0\n\n"} {
		if !strings.Contains(s, k) {
			t.Errorf("cpuinfo lacks %q", k)
		}
	}
}

func TestHostFigures(t *testing.T) {
	m := memStats()
	if m.total == 0 || m.free == 0 || m.avail < m.free || m.avail > m.total {
		t.Errorf("memStats: %+v", m)
	}
	if c := cpuTicks(); len(c) != hostCPUInfo().n {
		t.Errorf("cpuTicks: %d cpus, want %d", len(c), hostCPUInfo().n)
	}
	if l := loadAvg(); l[0] < 0 || l[0] > 1000 {
		t.Errorf("loadAvg: %v", l)
	}
	if !strings.HasPrefix(string(procMeminfo()), "MemTotal:       ") {
		t.Errorf("meminfo format")
	}
}
