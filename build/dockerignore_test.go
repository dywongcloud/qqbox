package build

import "testing"

func TestIgnoreMatcher(t *testing.T) {
	m := &ignoreMatcher{}
	for _, line := range []string{"secret.txt", "*.tmp", "node_modules", "build/**", "logs/*.log", "!logs/keep.log"} {
		neg := line[0] == '!'
		if neg {
			line = line[1:]
		}
		m.patterns = append(m.patterns, ignorePattern{segs: split(line), negate: neg})
	}
	cases := []struct {
		path string
		want bool
	}{
		{"secret.txt", true},
		{"other.txt", false},
		{"a.tmp", true},
		{"sub/a.tmp", false}, // *.tmp is a single segment at the root
		{"node_modules", true},
		{"node_modules/pkg/index.js", true}, // a matched directory excludes its subtree
		{"build/x/y", true},                 // build/** matches any depth
		{"build", true},                     // ** matches zero segments, so build/** covers build itself
		{"logs/app.log", true},
		{"logs/keep.log", false}, // negation re-includes
		{"", false},              // the context root
		{".", false},
	}
	for _, c := range cases {
		if got := m.match(c.path); got != c.want {
			t.Errorf("match(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}

func split(s string) []string {
	out := []string{}
	cur := ""
	for _, r := range s {
		if r == '/' {
			out = append(out, cur)
			cur = ""
		} else {
			cur += string(r)
		}
	}
	return append(out, cur)
}
