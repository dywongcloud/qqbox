//go:build !linux

package main

import (
	"fmt"
	"os"
)

func setupConsole(socket string) (*os.File, error) {
	return nil, fmt.Errorf("terminal containers need Linux")
}
