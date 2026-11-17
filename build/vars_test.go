package build

import "testing"

func TestExpandVars(t *testing.T) {
	scope := map[string]string{"FOO": "bar", "EMPTY": "", "V": "1.2"}
	cases := []struct{ in, want string }{
		{"$FOO", "bar"},
		{"${FOO}", "bar"},
		{"x$FOO/y", "xbar/y"},
		{"x${FOO}y", "xbary"},
		{"$MISSING", ""},
		{"${MISSING}", ""},
		{"${MISSING:-def}", "def"},
		{"${FOO:-def}", "bar"},
		{"${EMPTY:-def}", "def"},
		{"${FOO:+set}", "set"},
		{"${MISSING:+set}", ""},
		{"${EMPTY:+set}", ""},
		{`\$FOO`, "$FOO"},
		{"app-${V}", "app-1.2"},
		{"$", "$"},
		{"${unterminated", "${unterminated"},
		{"a$b_c", "a"}, // $b_c is one name, unset -> empty
	}
	for _, c := range cases {
		if got := expandVars(c.in, scope); got != c.want {
			t.Errorf("expandVars(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestParseArg(t *testing.T) {
	n, d, ok := parseArg("NAME=val")
	if n != "NAME" || d != "val" || !ok {
		t.Errorf("parseArg(NAME=val) = %q,%q,%v", n, d, ok)
	}
	n, d, ok = parseArg("NAME")
	if n != "NAME" || d != "" || ok {
		t.Errorf("parseArg(NAME) = %q,%q,%v", n, d, ok)
	}
	n, d, ok = parseArg("NAME=")
	if n != "NAME" || d != "" || !ok {
		t.Errorf("parseArg(NAME=) = %q,%q,%v (empty default is still a default)", n, d, ok)
	}
}

func TestArgScope(t *testing.T) {
	global := []Instruction{
		{Cmd: "ARG", Args: []string{"BASE=alpine:3.20"}},
		{Cmd: "ARG", Args: []string{"UNSET"}},
	}
	s := argScope(global, map[string]string{"UNSET": "overridden", "UNDECLARED": "ignored"})
	if s["BASE"] != "alpine:3.20" {
		t.Errorf("BASE = %q, want the default", s["BASE"])
	}
	if s["UNSET"] != "overridden" {
		t.Errorf("UNSET = %q, want the --build-arg value", s["UNSET"])
	}
	if _, ok := s["UNDECLARED"]; ok {
		t.Error("an undeclared --build-arg leaked into the scope")
	}
}

// TestSplitStagesGlobalArg proves a pre-FROM ARG is accepted and returned as a
// global, and a non-ARG before FROM is rejected.
func TestSplitStagesGlobalArg(t *testing.T) {
	ins := []Instruction{
		{Cmd: "ARG", Args: []string{"BASE=alpine"}, Line: 1},
		{Cmd: "FROM", Args: []string{"${BASE}"}, Line: 2},
		{Cmd: "RUN", Args: []string{"true"}, Line: 3},
	}
	global, stages, err := splitStages(ins)
	if err != nil {
		t.Fatal(err)
	}
	if len(global) != 1 || global[0].Args[0] != "BASE=alpine" {
		t.Fatalf("global = %+v", global)
	}
	if len(stages) != 1 || len(stages[0].instr) != 1 {
		t.Fatalf("stages = %+v", stages)
	}
	bad := []Instruction{{Cmd: "RUN", Args: []string{"true"}, Line: 1}}
	if _, _, err := splitStages(bad); err == nil {
		t.Error("a non-ARG before FROM was accepted; want an error")
	}
}

func TestUnquote(t *testing.T) {
	cases := map[string]string{
		`"1.27"`:   "1.27",
		`'alpine'`: "alpine",
		`plain`:    "plain",
		`"x`:       `"x`,    // unbalanced: left as-is
		`K="v"`:    `K="v"`, // inner quotes (not surrounding) left for the value parser
		`""`:       "",
	}
	for in, want := range cases {
		if got := unquote(in); got != want {
			t.Errorf("unquote(%q) = %q, want %q", in, got, want)
		}
	}
}
