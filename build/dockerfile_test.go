package build

import (
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	const df = `# a comment
FROM alpine:3.20 AS base
ENV KEY=value PATH=/usr/bin
RUN apk add --no-cache \
    curl \
    git
COPY --chown=1000:1000 src/ /app/
CMD ["/app/run", "--flag"]
`
	ins, err := Parse(strings.NewReader(df))
	if err != nil {
		t.Fatal(err)
	}
	if len(ins) != 5 {
		t.Fatalf("got %d instructions, want 5: %+v", len(ins), ins)
	}
	if ins[0].Cmd != "FROM" || ins[0].Args[0] != "alpine:3.20" {
		t.Errorf("FROM: %+v", ins[0])
	}
	if ins[1].Cmd != "ENV" || len(ins[1].Args) != 2 || ins[1].Args[0] != "KEY=value" {
		t.Errorf("ENV: %+v", ins[1])
	}
	// The continued RUN joins into one shell-form command string.
	if ins[2].Cmd != "RUN" || ins[2].Exec || len(ins[2].Args) != 1 ||
		!strings.Contains(ins[2].Args[0], "apk add") || !strings.Contains(ins[2].Args[0], "git") {
		t.Errorf("RUN: %+v", ins[2])
	}
	if ins[3].Cmd != "COPY" || ins[3].Flags["chown"] != "1000:1000" ||
		len(ins[3].Args) != 2 || ins[3].Args[1] != "/app/" {
		t.Errorf("COPY: %+v", ins[3])
	}
	if ins[4].Cmd != "CMD" || !ins[4].Exec || len(ins[4].Args) != 2 || ins[4].Args[1] != "--flag" {
		t.Errorf("CMD: %+v", ins[4])
	}
}

// TestParseShellTestNotJSON checks a RUN whose command starts with a shell test
// `[ ... ]` is treated as shell form, not a malformed JSON exec array.
func TestParseShellTestNotJSON(t *testing.T) {
	ins, err := Parse(strings.NewReader("FROM scratch\nRUN [ -f /x ] && echo ok\n"))
	if err != nil {
		t.Fatal(err)
	}
	run := ins[1]
	if run.Cmd != "RUN" || run.Exec || len(run.Args) != 1 || !strings.HasPrefix(run.Args[0], "[ -f /x ]") {
		t.Errorf("RUN shell-test: %+v", run)
	}
}

func TestParseHeredoc(t *testing.T) {
	ins, err := Parse(strings.NewReader("FROM alpine\nRUN <<EOF\necho hi\necho bye\nEOF\nCMD [\"true\"]\n"))
	if err != nil {
		t.Fatal(err)
	}
	// FROM, RUN, CMD
	if len(ins) != 3 || ins[1].Cmd != "RUN" {
		t.Fatalf("instructions = %+v", ins)
	}
	if ins[1].Exec || len(ins[1].Args) != 1 || ins[1].Args[0] != "echo hi\necho bye" {
		t.Fatalf("RUN heredoc body = %+v", ins[1].Args)
	}
	if ins[2].Cmd != "CMD" {
		t.Fatalf("instruction after heredoc = %+v", ins[2])
	}
}

func TestParseHeredocFalsePositive(t *testing.T) {
	// "<<" with no matching terminator line is an ordinary command, not a heredoc.
	ins, err := Parse(strings.NewReader("FROM alpine\nRUN echo \"a << b\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(ins) != 2 || ins[1].Cmd != "RUN" || ins[1].Args[0] != `echo "a << b"` {
		t.Fatalf("not a heredoc, want the raw command: %+v", ins[1])
	}
}

func TestParseCopyHeredoc(t *testing.T) {
	ins, err := Parse(strings.NewReader("FROM alpine\nCOPY <<EOF /etc/motd\nhello\nworld\nEOF\n"))
	if err != nil {
		t.Fatal(err)
	}
	c := ins[1]
	if c.Cmd != "COPY" || len(c.Args) != 1 || c.Args[0] != "/etc/motd" {
		t.Fatalf("COPY heredoc args = %+v", c)
	}
	if c.Heredoc != "hello\nworld\n" {
		t.Fatalf("COPY heredoc body = %q", c.Heredoc)
	}
}

func TestPredefinedArgs(t *testing.T) {
	m := predefinedArgs("linux/arm64")
	if m["TARGETARCH"] != "arm64" || m["TARGETOS"] != "linux" || m["TARGETPLATFORM"] != "linux/arm64" {
		t.Fatalf("predefined = %+v", m)
	}
	if m["BUILDOS"] != "linux" {
		t.Fatalf("BUILDOS = %q", m["BUILDOS"])
	}
}
