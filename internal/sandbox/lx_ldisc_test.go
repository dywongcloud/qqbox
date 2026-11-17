package sandbox

import (
	"bytes"
	"testing"
)

func readAll(l *ldisc) string {
	var b []byte
	for {
		p, eof := l.take(1 << 16)
		if eof {
			return string(b) + "<EOF>"
		}
		if len(p) == 0 {
			return string(b)
		}
		b = append(b, p...)
	}
}

func TestLdiscCanonical(t *testing.T) {
	cases := []struct {
		name, in, read, echo string
	}{
		{"line", "hello\r", "hello\n", "hello\r\n"},
		{"partial", "abc", "", "abc"},
		{"erase", "abX\x7fc\r", "abc\n", "abX\b \bc\r\n"},
		{"erase-ctl", "a\x01\x7f\r", "a\n", "a^A\b \b\b \b\r\n"},
		{"werase", "one two\x17three\r", "one three\n", "one two\b \b\b \b\b \bthree\r\n"},
		{"kill", "abc\x15d\r", "d\n", "abc\b \b\b \b\b \bd\r\n"},
		{"eof", "ab\x04\x04", "ab<EOF>", "ab"},
		{"lnext", "a\x16\x03b\r", "a\x03b\n", "a^\b^Cb\r\n"},
		{"utf8-erase", "é\x7f\r", "\n", "é\b \b\r\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			l := newLdisc()
			if c.name == "utf8-erase" {
				l.t.iflag |= lxIUTF8
			}
			if sigs := l.receive([]byte(c.in)); len(sigs) != 0 {
				t.Fatalf("signals %v", sigs)
			}
			if got := readAll(l); got != c.read {
				t.Errorf("read %q, want %q", got, c.read)
			}
			if got := string(l.takeOut(1 << 16)); got != c.echo {
				t.Errorf("echo %q, want %q", got, c.echo)
			}
		})
	}
}

func TestLdiscOneLinePerRead(t *testing.T) {
	l := newLdisc()
	l.receive([]byte("a\nbc\n"))
	if b, _ := l.take(100); string(b) != "a\n" {
		t.Fatalf("first read %q", b)
	}
	if b, _ := l.take(1); string(b) != "b" {
		t.Fatalf("partial read %q", b)
	}
	if b, _ := l.take(100); string(b) != "c\n" {
		t.Fatalf("rest %q", b)
	}
	if l.readable() {
		t.Fatal("still readable")
	}
}

func TestLdiscSignals(t *testing.T) {
	l := newLdisc()
	sigs := l.receive([]byte("abc\x03"))
	if len(sigs) != 1 || sigs[0] != ldSigINT {
		t.Fatalf("sigs %v", sigs)
	}
	if l.readable() || len(l.line) != 0 {
		t.Fatal("input not flushed")
	}
	if got := string(l.takeOut(100)); got != "^C" {
		t.Fatalf("echo %q", got)
	}
	if s := l.receive([]byte{0x1a, 0x1c}); len(s) != 2 || s[0] != ldSigTSTP || s[1] != ldSigQUIT {
		t.Fatalf("sigs %v", s)
	}
	l.t.lflag &^= lxISIG
	if s := l.receive([]byte{3}); len(s) != 0 {
		t.Fatalf("ISIG off: %v", s)
	}
}

func TestLdiscRaw(t *testing.T) {
	l := newLdisc()
	l.receive([]byte("pend"))
	raw := l.t
	raw.lflag &^= lxICANON | lxECHO | lxISIG
	raw.iflag &^= lxICRNL | lxIXON
	raw.oflag &^= lxOPOST
	l.setTermios(raw)
	l.takeOut(100)
	l.receive([]byte("x\r\x03"))
	if got := readAll(l); got != "pendx\r\x03" {
		t.Fatalf("raw read %q", got)
	}
	l.output([]byte("a\nb"))
	if got := l.takeOut(100); !bytes.Equal(got, []byte("a\nb")) {
		t.Fatalf("raw output %q", got)
	}
}

func TestLdiscOutput(t *testing.T) {
	l := newLdisc()
	l.output([]byte("ab\tc\n"))
	if got := string(l.takeOut(100)); got != "ab\tc\r\n" {
		t.Fatalf("output %q", got)
	}
	l.t.oflag |= lxXTABS
	l.output([]byte("ab\tc"))
	if got := string(l.takeOut(100)); got != "ab      c" {
		t.Fatalf("xtabs %q", got)
	}
	l.receive([]byte{0x13})
	l.output([]byte("x"))
	if got := l.takeOut(100); len(got) != 0 {
		t.Fatalf("stopped output %q", got)
	}
	l.receive([]byte{0x11})
	if got := string(l.takeOut(100)); got != "x" {
		t.Fatalf("restarted %q", got)
	}
}

func TestTermiosSpeeds(t *testing.T) {
	var cur lxTermios
	n := stdTermios()
	n.cflag = n.cflag&^lxCBAUD | 0x1002 // B115200
	cur.setFrom(&n, false)
	if cur.ospeed != 115200 || cur.ispeed != 115200 {
		t.Fatalf("speeds %d %d", cur.ispeed, cur.ospeed)
	}
	n.cflag = n.cflag&^lxCBAUD | lxBOTHER
	n.ospeed, n.ispeed = 31250, 31250
	cur.setFrom(&n, true)
	if cur.ospeed != 31250 {
		t.Fatalf("custom %d", cur.ospeed)
	}
	if got := decodeLxTermios(n.encode(true)); *got != n {
		t.Fatalf("round trip %+v", got)
	}
}
