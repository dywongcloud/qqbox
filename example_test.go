package laboon_test

import (
	"context"
	"fmt"
	"os"

	"github.com/samyfodil/laboon"
)

// Runs a command in an Alpine container with networking limited to one
// domain, the way `laboon -image alpine:3.20 -net -allow-domains example.com
// wget -qO- https://example.com` does.
func Example() {
	code, err := laboon.RunImage(context.Background(), "alpine:3.20",
		laboon.WithArgs("wget", "-qO-", "https://example.com"),
		laboon.WithStdio(os.Stdin, os.Stdout, os.Stderr),
		laboon.WithNet(),
		laboon.WithAllowDomains("example.com"),
		laboon.WithMaxMemory(512<<20),
	)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return
	}
	fmt.Println("exit status", code)
}
