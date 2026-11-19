# Examples

## `e2b/` — an E2B-style sandbox SDK on qqbox

A small clone of [E2B](https://e2b.dev)'s sandbox API, built on `qqbox` — **no
VM, no daemon, no cloud**. Two sandboxes:

### Code sandbox — `e2b.Sandbox`

Like E2B's code-interpreter `Sandbox`: run code and shell commands and keep a
filesystem across runs. Each call runs in its own fresh qqbox sandbox (per-call
isolation); a host directory mounted at `/workspace` gives the filesystem
continuity E2B gets from a long-lived VM.

| E2B | here |
|---|---|
| `sbx.run_code(code)` | `sbx.RunCode(ctx, code)` |
| `sbx.commands.run(cmd)` | `sbx.RunCommand(ctx, name, args...)` |
| `sbx.files.write(path, data)` | `sbx.WriteFile(name, data)` |
| `sbx.files.read(path)` | `sbx.ReadFile(name)` |

```sh
go run ./examples/e2b/cmd/codeinterpreter
```

### Desktop sandbox — `e2b.Desktop`

Like [`e2b-desktop`](https://github.com/e2b-dev/desktop): a full **XFCE** desktop
on a virtual framebuffer (`Xvfb`), with no VM. Screenshot it and drive it with
mouse/keyboard actions (`xdotool`) — the computer-use primitives an agent needs.

| E2B Desktop | here |
|---|---|
| `desktop.screenshot()` | `d.Screenshot(ctx)` |
| `desktop.left_click(x, y)` | `e2b.LeftClick(x, y)` |
| `desktop.write(text)` | `e2b.TypeText(text)` |
| `desktop.press(key)` | `e2b.PressKey(key)` |

```sh
go run ./examples/e2b/cmd/desktop desktop.png
```

The desktop image defaults to a prebuilt XFCE image (`accetto/ubuntu-vnc-xfce-g3`),
so it boots without installing a desktop. It runs as root and installs a few small
capture tools (`xvfb`, `imagemagick`, `xdotool`) if the image lacks them. Point
`Desktop.Image` at any XFCE image.

## `terminal/` — a confined terminal over a host directory

An interactive shell confined by qqbox over a host directory mounted
**read-only**: a program can read and run against it but cannot modify it, and
nothing escapes the sandbox to the rest of the host.

```sh
go run ./examples/terminal ~/some/project             # alpine userland
go run ./examples/terminal ~/some/project golang:1.25 # with a toolchain
```

The directory is at `/work` (read-only, the working directory). Two public-API
pieces do the work:

- `WithMountReadOnly(host, guest)` — a read-only bind: the guest reads the
  directory, but create, modify, delete and rename all fail with `EROFS`.
- `WithTerminal(t)` — qqbox runs the program on a pseudo-terminal and bridges it
  to `t`, any `io.ReadWriteCloser`, so an app drives a confined shell through its
  own transport (a `net.Conn`, a websocket adapter) without touching a pty:

  ```go
  qqbox.RunImage(ctx, "golang:1.25",
      qqbox.WithMountReadOnly(repo, "/work"),
      qqbox.WithDir("/work"),
      qqbox.WithArgs("/bin/sh"),
      qqbox.WithTerminal(conn), // conn is any io.ReadWriteCloser
  )
  ```

  If `t` also implements `Resizer` (`Resizes() <-chan qqbox.WinSize`), qqbox
  applies window sizes. Interactive job control (`^Z`/`fg`) is limited on this
  path.

## `device/` — a direct host↔guest channel

Attach a character device backed by the host: the host and the guest exchange
bytes over `/dev/<name>`, with no network.

```sh
go run ./examples/device
```

```go
dev := &qqbox.Device{Path: "/dev/lbserial", Kind: qqbox.DeviceSerial}
qqbox.RunImage(ctx, "alpine:3.20", qqbox.WithDevice(dev) /* , ... */)
<-dev.Ready()                 // the host end is live
fmt.Fprintln(dev.Host(), "…") // write to the guest; it reads /dev/lbserial
```
