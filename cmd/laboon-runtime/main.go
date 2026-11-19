// Command qqbox-runtime is an OCI runtime (a runc/crun drop-in) backed by
// qqbox: it runs an OCI bundle as a qqbox sandbox, so an engine that speaks
// the OCI runtime spec -- docker --runtime=qqbox, containerd, podman, CRI-O --
// runs containers on qqbox without a second kernel. Separate module so its
// surface stays out of the core library.
package main

import (
	"flag"
	"fmt"
	"os"
)

const version = "0.1.0"

func main() {
	// Version is handled before flag parsing: an engine calls `<runtime>
	// --version` to check compatibility, and it is not a global flag.
	if len(os.Args) >= 2 {
		switch os.Args[1] {
		case "--version", "-v", "version":
			fmt.Printf("qqbox-runtime version %s\ncommit: none\nspec: 1.0.2\n", version)
			return
		}
	}
	// runc-style global flags come before the subcommand; accept and ignore the
	// ones an engine passes (state lives under --root).
	g := flag.NewFlagSet("qqbox-runtime", flag.ExitOnError)
	root := g.String("root", defaultStateRoot, "root directory for container state")
	g.String("log", "", "log file (accepted, ignored)")
	g.String("log-format", "", "log format (accepted, ignored)")
	g.Bool("debug", false, "debug (accepted, ignored)")
	g.Bool("systemd-cgroup", false, "systemd cgroup (accepted, ignored)")
	g.Usage = usage
	g.Parse(os.Args[1:])

	args := g.Args()
	if len(args) == 0 {
		usage()
		os.Exit(1)
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "__monitor": // internal: the detached container monitor forked by create
		os.Exit(cmdMonitor(rest))
	case "__exec": // internal: the detached exec process forked by exec
		os.Exit(cmdExecRun(rest))
	case "run":
		os.Exit(cmdRun(rest))
	case "exec":
		os.Exit(cmdExec(*root, rest))
	case "create":
		os.Exit(cmdCreate(*root, rest))
	case "start":
		os.Exit(cmdStart(*root, rest))
	case "state":
		os.Exit(cmdState(*root, rest))
	case "kill":
		os.Exit(cmdKill(*root, rest))
	case "pause":
		os.Exit(cmdPause(*root, rest))
	case "resume", "unpause":
		os.Exit(cmdResume(*root, rest))
	case "delete":
		os.Exit(cmdDelete(*root, rest))
	case "features":
		os.Exit(cmdFeatures())
	case "--version", "-v", "version":
		fmt.Printf("qqbox-runtime version %s\nspec: 1.0.2\n", version)
	default:
		fmt.Fprintf(os.Stderr, "qqbox-runtime: unknown command %q\n", cmd)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, `qqbox-runtime - an OCI runtime backed by qqbox

usage: qqbox-runtime [global flags] <command> [args]

commands:
  run    <id> --bundle <dir>   run a bundle to completion
  create <id> --bundle <dir>   create a container (held until start)
  start  <id>                  start a created container
  state  <id>                  print container state as JSON
  kill   <id> [signal]         signal the container (default SIGTERM)
  delete <id>                  remove a stopped container
  features                     print runtime features as JSON
`)
}

func die(err error) {
	fmt.Fprintln(os.Stderr, "qqbox-runtime:", err)
	os.Exit(1)
}
