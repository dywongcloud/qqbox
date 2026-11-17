// A Go program that needs signals: asynchronous preemption (SIGURG), a
// nil dereference recovered as a panic (SIGSEGV), os/signal delivery and a
// child process.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"syscall"
	"time"
)

var sink int

func spin() {
	for i := 0; ; i++ {
		sink = i
	}
}

func main() {
	runtime.GOMAXPROCS(1)
	go spin()
	time.Sleep(20 * time.Millisecond)
	fmt.Println("preempt ok")

	func() {
		defer func() { fmt.Println("nil-deref recovered", recover() != nil) }()
		var p *int
		sink = *p
	}()

	c := make(chan os.Signal, 1)
	signal.Notify(c, syscall.SIGUSR1)
	syscall.Kill(os.Getpid(), syscall.SIGUSR1)
	select {
	case s := <-c:
		fmt.Println("notify", s)
	case <-time.After(5 * time.Second):
		fmt.Println("notify timeout")
	}

	if len(os.Args) > 1 {
		out, err := exec.Command(os.Args[1], "-c", "echo child-ok").Output()
		fmt.Printf("exec %q %v\n", out, err)
	}
}
