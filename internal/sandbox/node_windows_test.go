//go:build windows

package sandbox

import (
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// nodeRoot unpacks LC_WIN_NODE_TAR (a node:22-alpine filesystem tar with
// windows/m8's test files in /opt; windows/m8/run.sh builds it).
var nodeRoot = sync.OnceValues(func() (string, error) { return unpackTar("LC_WIN_NODE_TAR") })

func winNode(t *testing.T) string {
	root, err := nodeRoot()
	if err != nil {
		t.Fatal(err)
	}
	if root == "" {
		t.Skip("LC_WIN_NODE_TAR not set")
	}
	return root
}

// TestWindowsNode is M8's exit criterion: Node starts, runs JIT code, and
// passes the smoke suite (crypto, fs, timers, workers, child processes, http
// on loopback, an outbound fetch through the egress allowlist) and readline
// on a terminal.
func TestWindowsNode(t *testing.T) {
	root := winNode(t)
	node := "/usr/local/bin/node"
	smoke := []string{"node", "/opt/node-smoke.js"}
	if os.Getenv("LC_NET_OFFLINE") != "" {
		smoke = append(smoke, "--offline")
	}
	egress := func(c *Config) { c.Net, c.AllowDomains = true, []string{"example.com"} }
	cases := []struct {
		name string
		mod  func(*Config)
		prog string
		args []string
		want []string
	}{
		{"version", nil, node, []string{"node", "--version"}, []string{"v22."}},
		{"eval", nil, node, []string{"node", "-e", "console.log(1+1)"}, []string{"2\n"}},
		{"smoke", egress, node, smoke, []string{"PASS jit.loop", "PASS worker_threads", "PASS child.spawn.node", "PASS http.loopback", "ALL PASS"}},
		{"readline", nil, "/opt/ptyrun", []string{"ptyrun", "qqbox", "node", "/opt/readline.js"}, []string{"tty true true", "name? ", "hello qqbox"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			code, out, errb := winRunCfg(t, root, func(cfg *Config) {
				cfg.Env = []string{"PATH=/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin", "HOME=/root"}
				if c.mod != nil {
					c.mod(cfg)
				}
			}, c.prog, c.args...)
			if code != 0 {
				t.Errorf("exit %d\nstdout:\n%s\nstderr:\n%s", code, out, errb)
			}
			for _, w := range c.want {
				if !strings.Contains(out, w) {
					t.Errorf("stdout lacks %q:\n%s\nstderr:\n%s", w, out, errb)
				}
			}
			if testing.Verbose() {
				t.Logf("%s", out)
			}
		})
	}
}

// TestWindowsNodeStartup times node -e 0, warm.
func TestWindowsNodeStartup(t *testing.T) {
	root := winNode(t)
	winRunIn(t, root, "/usr/local/bin/node", "node", "-e", "0")
	var ds []time.Duration
	for range 7 {
		t0 := time.Now()
		if code, _, errb := winRunIn(t, root, "/usr/local/bin/node", "node", "-e", "0"); code != 0 {
			t.Fatalf("exit %d: %s", code, errb)
		}
		ds = append(ds, time.Since(t0))
	}
	slices.Sort(ds)
	t.Logf("node -e 0: min %v, median %v, max %v", ds[0].Round(time.Millisecond), ds[len(ds)/2].Round(time.Millisecond), ds[len(ds)-1].Round(time.Millisecond))
}
