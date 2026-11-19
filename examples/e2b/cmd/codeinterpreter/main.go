// Demonstrates the E2B-style code sandbox on qqbox: run Python, run a shell
// command, and persist a file across runs — in disposable sandboxes, no VM.
//
//	go run ./examples/e2b/cmd/codeinterpreter
package main

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/tencent/qqbox/examples/e2b"
)

func main() {
	ctx := context.Background()
	sbx := e2b.New("") // defaults to python:3.12-slim
	defer sbx.Close()

	ex, err := sbx.RunCode(ctx, "print(sum(range(100)))")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("run_code      -> %q (exit %d)\n", strings.TrimSpace(ex.Stdout), ex.ExitCode)

	if err := sbx.WriteFile("hello.py", []byte("print('from a file in /workspace')\n")); err != nil {
		log.Fatal(err)
	}
	ex, err = sbx.RunCommand(ctx, "python3", "/workspace/hello.py")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("run file      -> %q (exit %d)\n", strings.TrimSpace(ex.Stdout), ex.ExitCode)

	ex, err = sbx.RunCommand(ctx, "sh", "-c", "uname -sm; echo saved > /workspace/out.txt")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("commands.run  -> %q\n", strings.TrimSpace(ex.Stdout))

	data, err := sbx.ReadFile("out.txt")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("files.read    -> %q\n", strings.TrimSpace(string(data)))
}
