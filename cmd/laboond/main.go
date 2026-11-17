// Command laboond serves the Docker Engine API, backed by laboon: a docker
// client (or any Moby client) drives it, and each container runs as a laboon
// sandbox rather than through a second kernel. It is a separate module so its
// client-facing dependencies stay out of the core laboon library.
package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
)

func main() {
	host := flag.String("host", "unix:///run/laboon/laboond.sock", "address to serve the Docker API on (unix://path or tcp://host:port)")
	net_ := flag.Bool("net", false, "allow containers to use the network")
	flag.Parse()

	ln, err := listen(*host)
	if err != nil {
		log.Fatalf("laboond: listen %s: %v", *host, err)
	}
	d := newDaemon(*net_)
	srv := &http.Server{Handler: d.router()}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-stop
		srv.Close()
	}()

	log.Printf("laboond: Docker API on %s", *host)
	if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
		log.Fatalf("laboond: serve: %v", err)
	}
}

// listen opens the API listener for a unix:// or tcp:// address.
func listen(host string) (net.Listener, error) {
	switch {
	case strings.HasPrefix(host, "unix://"):
		path := strings.TrimPrefix(host, "unix://")
		if err := os.MkdirAll(dirOf(path), 0o750); err != nil {
			return nil, err
		}
		os.Remove(path)
		ln, err := net.Listen("unix", path)
		if err != nil {
			return nil, err
		}
		os.Chmod(path, 0o660)
		return ln, nil
	case strings.HasPrefix(host, "tcp://"):
		return net.Listen("tcp", strings.TrimPrefix(host, "tcp://"))
	default:
		return nil, fmt.Errorf("address must be unix://path or tcp://host:port")
	}
}

func dirOf(p string) string {
	if i := strings.LastIndexByte(p, '/'); i > 0 {
		return p[:i]
	}
	return "."
}
