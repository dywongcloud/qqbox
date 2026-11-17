// Demonstrates WithDevice: a serial character device gives the host and the
// guest a direct byte channel with no network. Here they exchange a line over
// /dev/lbserial.
//
//	go run ./examples/device
package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"sync"

	"github.com/samyfodil/laboon"
)

func main() {
	dev := &laboon.Device{Path: "/dev/lbserial", Kind: laboon.DeviceSerial}

	var got string
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-dev.Ready() // the host end is available once the run starts
		h := dev.Host()
		fmt.Fprint(h, "FROM_HOST\n")
		got, _ = bufio.NewReader(h).ReadString('\n')
	}()

	code, err := laboon.RunImage(context.Background(), "alpine:3.20",
		laboon.WithDevice(dev),
		laboon.WithArgs("sh", "-c", `read x < /dev/lbserial; printf 'guest-got:%s\n' "$x" > /dev/lbserial`),
		laboon.WithStderr(os.Stderr),
	)
	wg.Wait()
	if err != nil {
		fmt.Printf("run error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("guest exit: %d\n", code)
	fmt.Printf("host received over /dev/lbserial: %q\n", got)
}
