package build

import (
	"os"
	"path"
	"path/filepath"
	"strings"
)

// ignoreMatcher decides which context paths a COPY/ADD from the build context
// excludes, from the context's .dockerignore. Patterns are matched against the
// slash path relative to the context root; "*" and "?" match within a path
// segment, "**" matches across segments, a matched directory excludes its
// subtree, and a leading "!" re-includes. The last matching pattern wins.
type ignoreMatcher struct{ patterns []ignorePattern }

type ignorePattern struct {
	segs   []string
	negate bool
}

// loadDockerignore reads <contextDir>/.dockerignore. A missing file yields a nil
// matcher (which excludes nothing).
func loadDockerignore(contextDir string) (*ignoreMatcher, error) {
	data, err := os.ReadFile(filepath.Join(contextDir, ".dockerignore"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	m := &ignoreMatcher{}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		neg := strings.HasPrefix(line, "!")
		if neg {
			line = strings.TrimSpace(line[1:])
		}
		line = path.Clean(strings.TrimPrefix(line, "/"))
		if line == "." || line == "" {
			continue
		}
		m.patterns = append(m.patterns, ignorePattern{segs: strings.Split(line, "/"), negate: neg})
	}
	if len(m.patterns) == 0 {
		return nil, nil
	}
	return m, nil
}

// match reports whether the context-relative slash path p is excluded.
func (m *ignoreMatcher) match(p string) bool {
	if m == nil {
		return false
	}
	p = strings.TrimPrefix(path.Clean("/"+p), "/")
	if p == "" {
		return false // the context root is never excluded
	}
	name := strings.Split(p, "/")
	excluded := false
	for _, pat := range m.patterns {
		if matchSegs(pat.segs, name) {
			excluded = !pat.negate
		}
	}
	return excluded
}

// matchSegs reports whether the pattern segments match name or a leading prefix
// of it (so a matched directory excludes everything beneath it). "**" matches
// any run of segments, including none.
func matchSegs(pat, name []string) bool {
	if len(pat) == 0 {
		return true // pattern consumed: name (or this prefix of it) matched
	}
	if pat[0] == "**" {
		for i := 0; i <= len(name); i++ {
			if matchSegs(pat[1:], name[i:]) {
				return true
			}
		}
		return false
	}
	if len(name) == 0 {
		return false
	}
	if ok, err := path.Match(pat[0], name[0]); err != nil || !ok {
		return false
	}
	return matchSegs(pat[1:], name[1:])
}
