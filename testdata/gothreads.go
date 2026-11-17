// A Go program whose runtime starts several threads.
package main

import (
	"fmt"
	"runtime"
	"sync"
)

func main() {
	var wg sync.WaitGroup
	sums := make([]int, 8)
	for i := range sums {
		wg.Add(1)
		go func() {
			defer wg.Done()
			runtime.LockOSThread()
			for j := range 100000 {
				sums[i] += j % (i + 1)
			}
		}()
	}
	wg.Wait()
	fmt.Println("go ok", sums[0] == 0 && sums[1] == 50000)
}
