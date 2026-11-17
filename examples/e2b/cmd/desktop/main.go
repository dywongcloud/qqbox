// Demonstrates the E2B-Desktop-style GUI sandbox on laboon: boot a full XFCE
// desktop on a virtual framebuffer (no VM), drive it with mouse/keyboard, and
// save a screenshot.
//
//	go run ./examples/e2b/cmd/desktop [out.png]
package main

import (
	"context"
	"log"
	"os"

	"github.com/samyfodil/laboon/examples/e2b"
)

func main() {
	ctx := context.Background()
	d := e2b.NewDesktop()

	png, err := d.Run(ctx, []e2b.Action{
		e2b.LeftClick(10, 10), // the Applications menu, top-left
		e2b.Wait(1),
	})
	if err != nil {
		log.Fatal(err)
	}

	out := "desktop.png"
	if len(os.Args) > 1 {
		out = os.Args[1]
	}
	if err := os.WriteFile(out, png, 0o644); err != nil {
		log.Fatal(err)
	}
	log.Printf("wrote %s (%d bytes)", out, len(png))
}
