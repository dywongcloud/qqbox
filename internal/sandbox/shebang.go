package sandbox

// splitShebang parses a shebang line into the interpreter and at most one
// argument (Linux passes the remainder as a single argument).
func splitShebang(s string) (interp, arg string) {
	for len(s) > 0 && (s[0] == ' ' || s[0] == '\t') {
		s = s[1:]
	}
	i := 0
	for i < len(s) && s[i] != ' ' && s[i] != '\t' {
		i++
	}
	interp = s[:i]
	s = s[i:]
	for len(s) > 0 && (s[0] == ' ' || s[0] == '\t') {
		s = s[1:]
	}
	// trailing whitespace trim
	for len(s) > 0 && (s[len(s)-1] == ' ' || s[len(s)-1] == '\t' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	return interp, s
}
