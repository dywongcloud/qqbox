// Package build turns a Dockerfile into an OCI image by running each
// instruction under qqbox: RUN executes in the image's root filesystem and its
// changes become a layer, COPY/ADD bring in build-context files, and the
// metadata instructions update the image config. It is qqbox's `docker build`.
package build

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// Instruction is one Dockerfile instruction, parsed but not interpreted.
type Instruction struct {
	Cmd     string            // upper-case verb: FROM, RUN, COPY, ENV, ...
	Args    []string          // exec form: the JSON array; shell form: one element
	Exec    bool              // the arguments were given in JSON (exec) form
	Flags   map[string]string // leading --flags, e.g. --from, --chown, --platform
	Heredoc string            // a COPY/ADD heredoc body, written to the destination
	Line    int               // 1-based source line, for errors
}

// Parse reads a Dockerfile into its instructions. It handles comments, blank
// lines, line continuations and both the shell and JSON (exec) argument forms;
// parser directives (# syntax=, # escape=) are ignored and the escape
// character is the default backslash.
func Parse(r io.Reader) ([]Instruction, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	var lines []string
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}

	var out []Instruction
	for i := 0; i < len(lines); {
		raw := lines[i]
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			i++ // blank or comment (parser directives included) outside an instruction
			continue
		}
		start := i + 1
		var lb strings.Builder
		for { // join backslash-continued lines
			cont := strings.HasSuffix(raw, "\\")
			if cont {
				lb.WriteString(raw[:len(raw)-1])
			} else {
				lb.WriteString(raw)
			}
			i++
			if !cont {
				break
			}
			lb.WriteByte('\n')
			for i < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[i]), "#") {
				i++ // comment inside a continuation
			}
			if i >= len(lines) {
				break
			}
			raw = lines[i]
		}
		logical := lb.String()

		// A heredoc is active only when the line opens one and its terminator
		// appears below, so a stray "<<" in a command is not mistaken for one.
		var body []string
		marks := heredocMarkers(logical)
		marks = presentMarkers(marks, lines[i:])
		for _, mk := range marks {
			for i < len(lines) {
				l := lines[i]
				i++
				term := l
				if mk.dash {
					term = strings.TrimLeft(l, "\t")
				}
				if term == mk.delim {
					break
				}
				if mk.dash {
					l = strings.TrimLeft(l, "\t")
				}
				body = append(body, l)
			}
		}

		ins, err := parseInstruction(logical, start, marks, body)
		if err != nil {
			return nil, err
		}
		if ins != nil {
			out = append(out, *ins)
		}
	}
	return out, nil
}

// heredocMarker is one <<WORD opened on an instruction line.
type heredocMarker struct {
	delim string // the terminator word
	dash  bool   // <<- strips leading tabs from the body and terminator
}

// heredocMarkers finds the <<WORD / <<-WORD / <<'WORD' openers on a line, in order.
func heredocMarkers(s string) []heredocMarker {
	var out []heredocMarker
	for i := 0; i+2 <= len(s); {
		j := strings.Index(s[i:], "<<")
		if j < 0 {
			break
		}
		k := i + j + 2
		dash := false
		if k < len(s) && s[k] == '-' {
			dash = true
			k++
		}
		var quote byte
		if k < len(s) && (s[k] == '\'' || s[k] == '"') {
			quote = s[k]
			k++
		}
		d := k
		for k < len(s) && (isNameChar(s[k]) || s[k] == '.') {
			k++
		}
		delim := s[d:k]
		if quote != 0 && k < len(s) && s[k] == quote {
			k++
		}
		if delim != "" {
			out = append(out, heredocMarker{delim: delim, dash: dash})
		}
		i = k
	}
	return out
}

// presentMarkers keeps only the markers whose terminator actually appears in the
// lines below, so a "<<" that is not really a heredoc opener is ignored.
func presentMarkers(marks []heredocMarker, below []string) []heredocMarker {
	var out []heredocMarker
	for _, mk := range marks {
		for _, l := range below {
			t := l
			if mk.dash {
				t = strings.TrimLeft(l, "\t")
			}
			if t == mk.delim {
				out = append(out, mk)
				break
			}
		}
	}
	return out
}

// parseInstruction parses one instruction. With heredoc markers, a RUN's body
// becomes its shell command; other instructions fall back to the plain parse.
func parseInstruction(line string, lineNo int, marks []heredocMarker, body []string) (*Instruction, error) {
	if len(marks) == 0 {
		return parseLine(line, lineNo)
	}
	joined := strings.ReplaceAll(line, "\n", " ")
	verb, rest, _ := strings.Cut(strings.TrimSpace(joined), " ")
	cmd := strings.ToUpper(verb)
	ins := &Instruction{Cmd: cmd, Flags: map[string]string{}, Line: lineNo}
	rest = strings.TrimSpace(rest)
	for strings.HasPrefix(rest, "--") {
		var tok string
		tok, rest, _ = strings.Cut(rest, " ")
		rest = strings.TrimSpace(rest)
		k, v, _ := strings.Cut(strings.TrimPrefix(tok, "--"), "=")
		ins.Flags[k] = v
	}
	bodyStr := strings.Join(body, "\n")
	if cmd == "COPY" || cmd == "ADD" {
		// "COPY <<EOF /dst" writes the body to the destination. The non-marker
		// tokens are the destination (and any literal sources).
		for _, f := range strings.Fields(rest) {
			if heredocToken(f) != f {
				ins.Args = append(ins.Args, f)
			}
		}
		if len(body) > 0 {
			ins.Heredoc = bodyStr + "\n"
		}
		return ins, nil
	}
	if cmd != "RUN" {
		return nil, fmt.Errorf("line %d: heredoc is supported only on RUN, COPY and ADD", lineNo)
	}
	ins.Exec = false
	// "RUN <<EOF" (nothing but the opener) runs the body as the script; a form
	// with a command, like "RUN cat <<EOF", is reconstructed so the shell runs it.
	if rest == heredocToken(rest) {
		ins.Args = []string{bodyStr}
	} else {
		ins.Args = []string{rest + "\n" + bodyStr + "\n" + marks[len(marks)-1].delim}
	}
	return ins, nil
}

// heredocToken returns the leading <<WORD opener of rest, or "" if rest does not
// begin with one; it is how parseInstruction tells "RUN <<EOF" from "RUN cmd <<EOF".
func heredocToken(rest string) string {
	if !strings.HasPrefix(rest, "<<") {
		return ""
	}
	k := 2
	if k < len(rest) && rest[k] == '-' {
		k++
	}
	var quote byte
	if k < len(rest) && (rest[k] == '\'' || rest[k] == '"') {
		quote = rest[k]
		k++
	}
	for k < len(rest) && (isNameChar(rest[k]) || rest[k] == '.') {
		k++
	}
	if quote != 0 && k < len(rest) && rest[k] == quote {
		k++
	}
	return rest[:k]
}

// shellWords are instructions whose shell form is a single command string;
// everything else splits its arguments on whitespace.
var shellForm = map[string]bool{"RUN": true, "CMD": true, "ENTRYPOINT": true}

func parseLine(s string, line int) (*Instruction, error) {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	if s == "" {
		return nil, nil
	}
	verb, rest, _ := strings.Cut(s, " ")
	cmd := strings.ToUpper(verb)
	rest = strings.TrimSpace(rest)
	ins := &Instruction{Cmd: cmd, Flags: map[string]string{}, Line: line}
	// Leading --flag[=value] options.
	for strings.HasPrefix(rest, "--") {
		var tok string
		tok, rest, _ = strings.Cut(rest, " ")
		rest = strings.TrimSpace(rest)
		k, v, _ := strings.Cut(strings.TrimPrefix(tok, "--"), "=")
		ins.Flags[k] = v
	}
	if strings.HasPrefix(rest, "[") {
		var arr []string
		if json.Unmarshal([]byte(rest), &arr) == nil {
			ins.Args, ins.Exec = arr, true
			return ins, nil
		}
		// Not a JSON exec array — e.g. a shell test `RUN [ -f x ] && ...`. Fall
		// through to shell form, as docker does when the JSON parse fails.
	}
	if shellForm[cmd] {
		ins.Args = []string{rest}
	} else {
		ins.Args = strings.Fields(rest)
	}
	return ins, nil
}
