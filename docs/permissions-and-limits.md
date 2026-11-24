# Permissions and limits

qqbox is confined by default; every capability below is an explicit opt-in.
Libraries take typed options, not ambient environment variables: the Rust API
is `Options` (`crates/qqbox/src/options.rs`) and the serialized daemon/OCI form
is `Config` (`crates/qqbox/src/config.rs`). The `With…` builder names below are
the **upstream Go API** and do not exist in this Rust-only tree.

## Networking

Networking is off by default: the guest has loopback only, like
`docker --network none`. `Options { net: true, .. }` (Rust CLI `--net`) enables egress
through supervisor-proxied host sockets; `Config { net: true, .. }` is the
daemon-side equivalent. Add `allow_domains`, `allow_cidrs` and `allow_ports` to
bound access; enabling networking alone does not create a restrictive allowlist.
Domain rules include subdomains, CIDRs add directly reachable addresses, and
port rules restrict destination ports.

`Options { no_net: true, .. }` removes loopback too when `net` is absent. The
Windows x86-64 launch contract requires `net` even for guest `AF_INET` loopback;
that contract is not ported to Rust in this tree.

## Filesystem

`Options::mounts` / `Config::mounts` take typed `Mount { host, guest,
read_only }` values: a read-write mount exposes a host directory confined to that
subtree, a read-only mount rejects create, modify, delete and rename with
`EROFS`. Resolution goes through `vfs::Namespace::resolve`; `vfs::path::confined`
rejects `..` escapes and symlinks that canonicalize outside the root. A mount can
also expose host Unix sockets beneath it, so a mount is an explicit grant of
access.

## Memory

Writable-plus-executable guest memory is allowed by default for JITs such as V8;
`reject_rwx: true` refuses it. There is no default memory cap. `max_memory`
limits mapped address space on Linux and stub-process committed memory on
Windows; **macOS does not enforce it yet**. In this Rust tree these are typed
control-plane values: the Linux seccomp/trap backend enforces W^X on the ELF
segments it maps, and the full guest-runtime memory accounting is not ported.

## Source

The typed surfaces are
[options.rs](../crates/qqbox/src/options.rs),
[config.rs](../crates/qqbox/src/config.rs),
[vfs/mod.rs](../crates/qqbox/src/vfs/mod.rs) and the backend boundary in
[NATIVE_BACKEND.md](NATIVE_BACKEND.md).
