// Command qqbox runs a Linux program in the sandbox, either from a host
// directory or from an OCI image. It is a thin front end to package qqbox:
// each flag is one of its options.
//
//	qqbox [-root dir] program [args...]
//	qqbox -image ref [command [args...]]
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/pprof"
	"strconv"
	"strings"
	"syscall"

	"github.com/tencent/qqbox"
	"github.com/tencent/qqbox/build"
)

// version is the released version, set by the linker at build time (see the
// release workflow); "dev" for an ordinary `go build`.
var version = "dev"

// buildCmd implements `qqbox build`: run a Dockerfile and write an image.
func buildCmd(args []string) {
	fs := flag.NewFlagSet("qqbox build", flag.ExitOnError)
	tag := fs.String("t", "qqbox-build:latest", "name:tag for the built image")
	file := fs.String("f", "", "Dockerfile to use (default <context>/Dockerfile)")
	out := fs.String("o", "image.tar", "write the image here as a docker-load tarball")
	platform := fs.String("platform", "", "image platform, linux/amd64 or linux/arm64 (default: the host's)")
	target := fs.String("target", "", "build only up to this named stage (default: the last stage)")
	net := fs.Bool("net", false, "allow RUN steps to use the network")
	var buildArgs mountList
	fs.Var(&buildArgs, "build-arg", "set a build-time ARG as NAME=VALUE (repeatable)")
	noCache := fs.Bool("no-cache", false, "do not reuse cached layers from previous builds")
	cacheDir := fs.String("cache", "", "build layer cache directory (default: <user cache>/qqbox/build)")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: qqbox build [-t name:tag] [-f Dockerfile] [-o image.tar] [--platform P] [--target stage] [--net] [--build-arg NAME=VALUE] [--no-cache] [context]")
		fs.PrintDefaults()
	}
	fs.Parse(args)
	ctxDir := fs.Arg(0)
	if ctxDir == "" {
		ctxDir = "."
	}
	df := *file
	if df == "" {
		df = filepath.Join(ctxDir, "Dockerfile")
	}
	f, err := os.Open(df)
	if err != nil {
		fmt.Fprintln(os.Stderr, "qqbox build:", err)
		os.Exit(1)
	}
	defer f.Close()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	var cache build.Cache
	if !*noCache {
		dir := *cacheDir
		if dir == "" {
			base, err := os.UserCacheDir()
			if err != nil {
				base = os.TempDir()
			}
			dir = filepath.Join(base, "qqbox", "build")
		}
		if c, err := build.NewDirCache(dir); err != nil {
			fmt.Fprintln(os.Stderr, "qqbox build: cache:", err)
		} else {
			cache = c
		}
	}
	bargs := map[string]string{}
	for _, kv := range buildArgs {
		k, v, _ := strings.Cut(kv, "=")
		bargs[k] = v
	}
	h, err := build.Build(ctx, f, build.Options{
		Context: ctxDir, Tag: *tag, Platform: *platform, Output: *out, Target: *target, Net: *net,
		Store: true, BuildArgs: bargs, Cache: cache, Stdout: os.Stderr, Stderr: os.Stderr,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "qqbox build:", err)
		os.Exit(1)
	}
	fmt.Printf("built %s\n%s\nrun it: qqbox -image %s\nwrote %s (docker load -i %s)\n", *tag, h, *tag, *out, *out)
}

// mountList collects repeated -mount/-v flags.
type mountList []string

func (m *mountList) String() string { return strings.Join(*m, ",") }
func (m *mountList) Set(s string) error {
	*m = append(*m, s)
	return nil
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "build" {
		buildCmd(os.Args[2:])
		return
	}
	if len(os.Args) > 1 && (os.Args[1] == "version" || os.Args[1] == "-version" || os.Args[1] == "--version") {
		fmt.Println("qqbox", version)
		return
	}
	root := flag.String("root", "", "directory the guest sees as / (read-only); the program path is inside it")
	image := flag.String("image", "", "OCI image to run, e.g. alpine:3.20; arguments replace its CMD")
	entrypoint := flag.String("entrypoint", "", "with -image: replace the image's ENTRYPOINT and drop its CMD, as docker run --entrypoint (empty clears it)")
	pull := flag.Bool("pull", false, "with -image: check the registry for a newer image")
	platform := flag.String("platform", "", "with -image: the image's platform, linux/amd64 or linux/arm64 (default: the host's architecture)")
	trace := flag.Bool("trace", false, "log guest syscalls to stderr")
	rejectRWX := flag.Bool("reject-rwx", false, "refuse writable+executable memory (W^X); JIT engines like V8 need it allowed")
	maxMem := flag.String("mem", "", "cap guest address space, e.g. 256M, 1G (0 = unlimited)")
	net := flag.Bool("net", false, "enable networking (proxy guest sockets to host); without it the guest has loopback only")
	noNet := flag.Bool("no-net", false, "no network at all, not even loopback (without -net)")
	runtime := flag.String("runtime", "standalone", "where to run: standalone (in-process, no daemon), qqbox (a qqbox daemon over ConnectRPC), or docker (a Docker API endpoint)")
	x11 := flag.Bool("x11", false, "sugar for -mount /tmp/.X11-unix:/tmp/.X11-unix and passing DISPLAY, so GUI programs display on the host X server")
	user := flag.String("user", "", "run as this user (uid, uid:gid, name or name:group), overriding the image's USER, as docker --user")
	flag.StringVar(user, "u", "", "alias for -user")
	var mounts mountList
	flag.Var(&mounts, "mount", "bind a host path into the guest as HOST:GUEST (repeatable); GUEST=/ is the rootfs (-root), others are socket binds")
	flag.Var(&mounts, "v", "alias for -mount")
	var envVars mountList
	flag.Var(&envVars, "env", "set a guest environment variable as KEY=VALUE (repeatable), on top of the image's environment")
	flag.Var(&envVars, "e", "alias for -env")
	allowDomains := flag.String("allow-domains", "", "with -net: comma-separated egress allowlist; the sandbox resolves only these domains (and subdomains) and lets the guest connect only to the addresses it resolved")
	allowCIDRs := flag.String("allow-cidrs", "", "with -net: comma-separated CIDR networks the guest may also connect to directly")
	allowPorts := flag.String("allow-ports", "", "with -net: comma-separated destination ports the guest may connect to (others refused)")
	noRewrite := flag.Bool("no-rewrite", false, "skip syscall rewriting (seccomp should kill the guest)")
	quiet := flag.Bool("q", false, "discard the guest's stdout and stderr")
	discardOut := flag.Bool("discard-stdout", false, "discard the guest's stdout")
	discardErr := flag.Bool("discard-stderr", false, "discard the guest's stderr")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: %s [flags] program [args...]\n       %s -image ref [command [args...]]\n", os.Args[0], os.Args[0])
		flag.PrintDefaults()
	}
	flag.Parse()
	switch *runtime {
	case "standalone":
		// in-process, the rest of main
	case "docker", "qqbox":
		fail(fmt.Errorf("-runtime %s submits the run to a daemon, not yet wired in this build; run qqboxd (it serves the Docker API) and point a docker client at it", *runtime))
	default:
		fail(fmt.Errorf("unknown -runtime %q: want standalone, qqbox or docker", *runtime))
	}
	if pf := os.Getenv("QQBOX_CPUPROFILE"); pf != "" {
		if f, err := os.Create(pf); err != nil {
			fmt.Fprintln(os.Stderr, "qqbox: cpuprofile:", err)
		} else {
			pprof.StartCPUProfile(f) // stopped before os.Exit, which skips defers
		}
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer stop()

	opts := []qqbox.Option{
		qqbox.WithStdio(os.Stdin, os.Stdout, os.Stderr),
		qqbox.WithAllowDomains(splitList(*allowDomains)...),
		qqbox.WithAllowCIDRs(splitList(*allowCIDRs)...),
	}
	for _, p := range splitList(*allowPorts) {
		n, err := strconv.Atoi(p)
		if err != nil {
			fail(fmt.Errorf("bad -allow-ports value %q: %w", p, err))
		}
		opts = append(opts, qqbox.WithAllowPorts(n))
	}
	if *maxMem != "" {
		n, err := parseSize(*maxMem)
		if err != nil {
			fail(err)
		}
		opts = append(opts, qqbox.WithMaxMemory(n))
	}
	for _, m := range mounts {
		host, guest, ok := strings.Cut(m, ":")
		if !ok || host == "" || guest == "" {
			fail(fmt.Errorf("bad -mount %q: want HOST:GUEST", m))
		}
		opts = append(opts, qqbox.WithMount(host, guest))
	}
	for _, kv := range envVars {
		key, val, ok := strings.Cut(kv, "=")
		if !ok || key == "" {
			fail(fmt.Errorf("bad -env %q: want KEY=VALUE", kv))
		}
		opts = append(opts, qqbox.WithEnvVar(key, val))
	}
	for _, f := range []struct {
		on  bool
		opt qqbox.Option
	}{
		{*root != "", qqbox.WithRoot(*root)},
		{*pull, qqbox.WithPull()},
		{*platform != "", qqbox.WithPlatform(*platform)},
		{*trace, qqbox.WithTrace(os.Stderr)},
		{*rejectRWX, qqbox.WithRejectRWX()},
		{*net, qqbox.WithNet()},
		{*noNet, qqbox.WithNoNet()},
		{*x11, qqbox.WithX11()},
		{*user != "", qqbox.WithUser(*user)},
		{*noRewrite, qqbox.WithNoRewrite()},
		{flagSet("entrypoint"), entrypointOpt(*entrypoint)},
		{*quiet || *discardOut, qqbox.WithDiscardStdout()},
		{*quiet || *discardErr, qqbox.WithDiscardStderr()},
	} {
		if f.on {
			opts = append(opts, f.opt)
		}
	}

	var code int
	var err error
	if *image != "" {
		if flag.NArg() > 0 {
			opts = append(opts, qqbox.WithArgs(flag.Args()...))
		}
		code, err = qqbox.RunImage(ctx, *image, opts...)
	} else {
		if flag.NArg() < 1 {
			flag.Usage()
			os.Exit(2)
		}
		code, err = qqbox.Run(ctx, flag.Arg(0), append(opts, qqbox.WithArgs(flag.Args()[1:]...))...)
	}
	if os.Getenv("QQBOX_CPUPROFILE") != "" {
		pprof.StopCPUProfile() // flush before any error exit, so a signal-stopped run still profiles
	}
	if err != nil {
		fail(err)
	}
	os.Exit(code)
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "qqbox:", err)
	os.Exit(125)
}

// parseSize parses a byte size with an optional K/M/G suffix.
func parseSize(s string) (uint64, error) {
	mult := uint64(1)
	switch s[len(s)-1] {
	case 'K', 'k':
		mult, s = 1<<10, s[:len(s)-1]
	case 'M', 'm':
		mult, s = 1<<20, s[:len(s)-1]
	case 'G', 'g':
		mult, s = 1<<30, s[:len(s)-1]
	}
	n, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("bad -mem size %q", s)
	}
	return n * mult, nil
}

// splitList parses a comma-separated flag value into trimmed, non-empty items.
func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// flagSet reports whether flag name was given, even as an empty value.
func flagSet(name string) bool {
	set := false
	flag.Visit(func(f *flag.Flag) { set = set || f.Name == name })
	return set
}

// entrypointOpt is -entrypoint's option: one program, or none when empty.
func entrypointOpt(e string) qqbox.Option {
	if e == "" {
		return qqbox.WithEntrypoint()
	}
	return qqbox.WithEntrypoint(e)
}
