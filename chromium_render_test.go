//go:build linux && chromium

// Chromium render smoke, behind the `chromium` build tag so the default suite
// is unaffected (it pulls a browser image). Run it where the image is cached:
//
//	go test -tags chromium -run TestChromiumRender .
//
// It is the goal's "headless Chromium render green" check: headless Chromium
// renders a page and writes a PNG, all through the vfs Namespace + overlay
// backend (the screenshot lands in the kept upper). --virtual-time-budget makes
// it wait for the page before capturing, so the check is deterministic.
package laboon

import (
	"context"
	"os"
	"testing"

	"github.com/samyfodil/laboon/internal/sandbox"
)

func TestChromiumRender(t *testing.T) {
	prog, cfg, err := imageRun(context.Background(), "zenika/alpine-chrome", newOptions([]Option{
		WithArgs("--headless", "--no-sandbox", "--disable-gpu", "--virtual-time-budget=6000",
			"--screenshot=/tmp/s.png", "--window-size=320,200",
			`data:text/html,<h1>vfs render ok</h1>`),
	}))
	if err != nil {
		t.Skipf("chromium image unavailable: %v", err)
	}
	up := t.TempDir()
	cfg.Upper = up
	code, err := sandbox.Run(context.Background(), prog, cfg)
	if err != nil || code != 0 {
		t.Fatalf("chromium run: code=%d err=%v", code, err)
	}
	b, err := os.ReadFile(up + "/tmp/s.png")
	if err != nil || len(b) < 24 || string(b[1:4]) != "PNG" {
		t.Fatalf("no PNG rendered through the vfs: %d bytes, err=%v", len(b), err)
	}
	t.Logf("rendered %d-byte PNG through the vfs", len(b))
}
