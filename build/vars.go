package build

import (
	"runtime"
	"strings"
)

// predefinedArgs are the build args docker provides automatically (usable in
// FROM, and in a stage once re-declared with ARG). TARGET* describe the image
// being built, BUILD* the builder's platform. Both are linux here.
func predefinedArgs(platform string) map[string]string {
	arch := runtime.GOARCH
	if _, a, ok := strings.Cut(platform, "/"); ok {
		arch = a
	}
	return map[string]string{
		"TARGETPLATFORM": "linux/" + arch, "TARGETOS": "linux", "TARGETARCH": arch, "TARGETVARIANT": "",
		"BUILDPLATFORM": "linux/" + runtime.GOARCH, "BUILDOS": "linux", "BUILDARCH": runtime.GOARCH, "BUILDVARIANT": "",
	}
}

// substituteIn names the instructions whose arguments take Dockerfile ${VAR}
// substitution (from ARG and ENV), matching docker. RUN, CMD and ENTRYPOINT are
// not here: their variables are expanded by the shell at run time from the
// environment, not by the Dockerfile.
var substituteIn = map[string]bool{
	"ADD": true, "COPY": true, "ENV": true, "EXPOSE": true, "FROM": true,
	"LABEL": true, "STOPSIGNAL": true, "USER": true, "VOLUME": true, "WORKDIR": true,
}

// parseArg splits an ARG token "name" or "name=default" into its parts, with the
// surrounding quotes docker strips from the default removed.
func parseArg(tok string) (name, def string, hasDef bool) {
	name, def, hasDef = strings.Cut(tok, "=")
	return name, unquote(def), hasDef
}

// unquote removes a single pair of matching surrounding quotes, as docker does
// for ARG and ENV values (ARG V="1.2" -> 1.2).
func unquote(s string) string {
	if len(s) >= 2 && (s[0] == '"' || s[0] == '\'') && s[len(s)-1] == s[0] {
		return s[1 : len(s)-1]
	}
	return s
}

// argScope resolves the pre-FROM (global) ARG instructions into a name->value
// map: a --build-arg value wins, else the declared default, else empty. Only
// declared args appear, so an undeclared --build-arg is ignored.
func argScope(global []Instruction, buildArgs map[string]string) map[string]string {
	m := map[string]string{}
	for _, in := range global {
		if in.Cmd != "ARG" || len(in.Args) == 0 {
			continue
		}
		name, def, hasDef := parseArg(in.Args[0])
		switch {
		case buildArgs[name] != "":
			m[name] = buildArgs[name]
		case hasDef:
			m[name] = def
		default:
			m[name] = ""
		}
	}
	return m
}

// expandVars substitutes $name and ${name} (with the ${name:-default} and
// ${name:+alt} forms) in s from scope. A backslash escapes a dollar (\$ -> $).
// An unknown variable expands to the empty string, as docker does.
func expandVars(s string, scope map[string]string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '\\' && i+1 < len(s) && s[i+1] == '$' {
			b.WriteByte('$')
			i++
			continue
		}
		if c != '$' {
			b.WriteByte(c)
			continue
		}
		if i+1 < len(s) && s[i+1] == '{' {
			end := strings.IndexByte(s[i+2:], '}')
			if end < 0 {
				b.WriteByte(c)
				continue
			}
			b.WriteString(resolveExpr(s[i+2:i+2+end], scope))
			i = i + 2 + end
			continue
		}
		j := i + 1
		if j < len(s) && (isNameStart(s[j])) {
			k := j + 1
			for k < len(s) && isNameChar(s[k]) {
				k++
			}
			b.WriteString(scope[s[j:k]])
			i = k - 1
			continue
		}
		b.WriteByte(c) // a lone $
	}
	return b.String()
}

// resolveExpr evaluates the inside of a ${...}: a bare name, name:-default
// (the default when unset or empty) or name:+alt (alt when set and non-empty).
func resolveExpr(expr string, scope map[string]string) string {
	if name, def, ok := strings.Cut(expr, ":-"); ok {
		if v := scope[name]; v != "" {
			return v
		}
		return def
	}
	if name, alt, ok := strings.Cut(expr, ":+"); ok {
		if scope[name] != "" {
			return alt
		}
		return ""
	}
	return scope[expr]
}

func isNameStart(c byte) bool {
	return c == '_' || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z')
}

func isNameChar(c byte) bool { return isNameStart(c) || (c >= '0' && c <= '9') }
