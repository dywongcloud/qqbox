package qqbox_test

import (
	"context"
	"fmt"
	"os"

	"github.com/tencent/qqbox"
)

// Runs a command in an Alpine container with networking limited to one
// domain, the way `qqbox -image alpine:3.20 -net -allow-domains example.com
// wget -qO- https://example.com` does.
func Example() {
	code, err := qqbox.RunImage(context.Background(), "alpine:3.20",
		qqbox.WithArgs("wget", "-qO-", "https://example.com"),
		qqbox.WithStdio(os.Stdin, os.Stdout, os.Stderr),
		qqbox.WithNet(),
		qqbox.WithAllowDomains("example.com"),
		qqbox.WithMaxMemory(512<<20),
	)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return
	}
	fmt.Println("exit status", code)
}
