# vnet — a virtual network layer (design)

Status: design + Phase 1 in progress. Motivation: give network namespaces real
isolation and a path to container-to-container networking without depending on
the host's bridge/veth/NAT (what podman's netavark needs). Analogous to the vfs
layer: a supervisor-owned chokepoint with pluggable realization, built in Go so
it works the same on linux/darwin/windows.

## Why

A network namespace is currently just an id (`dguest.ps.netns`; 0 = host net,
nonzero = isolated empty). INET sockets are refused at creation in a non-host
netns and are otherwise straight host passthrough with no per-op gating. That is
why `podman run --network=none` can't bring loopback up safely: crun's preferred
path is `socket(AF_INET)`+`ioctl(SIOCSIFFLAGS)`, and only when that socket fails
does it hit a buggy netlink fallback — our EAFNOSUPPORT triggers the bug. Simply
allowing AF_INET in an isolated netns would leak the host network in.

## Core decision: endpoint pairing, not a packet stack

vnet pairs socket *endpoints* (streams/datagrams), it does NOT implement IP/TCP.
This covers ordinary TCP/UDP apps (loopback, container-to-container, and later
external via host-socket proxying) indefinitely. It is deliberately NOT enough
for raw sockets, AF_PACKET/TUN, in-guest TCP, ICMP/ping, fragmentation, or
faithful firewall/conntrack — those need a packet backend and stay out of the
contract. "External/NAT" means socket proxying + port publishing, not packet NAT.

## Abstraction

- Each `dsock` has an **immutable owning netns** captured at `socket()`, and an
  explicit **backend**: host-passthrough | virtual-INET | unix | netlink. Guest
  `family/typ/proto` stay separate from the backing transport.
- **Dispatch by the socket's owning netns, not the caller's current ps.netns.**
  A socket keeps its network attachment across unshare/setns and fork/dup. (This
  is also a capability boundary: entering a netns does not revoke an already-open
  host-network socket — a stronger promise would need a descriptor-transfer
  policy, out of scope here.)
- A per-run **endpoint registry** on `dnet` owns port reservations, wildcard
  matching, listeners, and per-netns interface state (e.g. `lo.up`). Backing
  unix socket names are opaque implementation identifiers, never the authoritative
  address space, and live in a private directory a guest cannot reach through
  `unixTarget`/mount shortcuts (fail closed if it can't be created).

The boundary must cover, per owning-netns backend: socket/bind/listen/connect/
accept4/sockname/shutdown (net_unix.go); sendIov/recvInto + address and
control-message translation, incl. read/write (lx_net.go, msg_unix.go); sockopts
(sockopt_unix.go); poll/epoll readiness; interface ioctls; final-ref cleanup.

## Security invariants (must hold even in a spike)

1. No host-socket fallback for a virtual socket — ever.
2. No `SCM_RIGHTS` / unix credentials across virtual INET (gate `hostControl` by
   backend; never expose supervisor creds; `SO_PASSCRED` must not light up unix
   cred delivery).
3. DNS `:53` is dispatched by owning-netns routing BEFORE the egress/DNS special
   case: inside an isolated netns `127.0.0.1:53` reaches the netns-local server;
   an external resolver stays unreachable. `netState().eg.connectAllowed` stays
   strictly the external-egress boundary, not netns routing.
4. Backing transport names are private, distinct from guest AF_UNIX names.
5. Bound sockets / pending accepts / queued bytes / ephemeral reservations are
   capped, so isolation isn't a cheap supervisor resource-exhaustion path.

## Phases

- **Phase 1 (in progress): IPv4 loopback streams + the ioctl control socket.**
  Allow isolated `AF_INET`/`AF_INET6` `SOCK_DGRAM` *creation* for crun's
  interface ioctls, but reject UDP data ops (no host-INET fallback). Implement
  IPv4 `SOCK_STREAM` loopback over registry-controlled private unix listeners:
  bind (incl. `127/8`, `0.0.0.0` wildcard, `bind(:0)` ephemeral), listen,
  blocking/nonblocking connect (`ECONNREFUSED` with no listener) + accept,
  synthesized `sockaddr_in` on sockname/accept/peer, basic sockopts (`SO_ERROR`,
  cache `TCP_NODELAY`), readiness, half-close, final-ref cleanup. Add
  `SIOCSIFFLAGS` to `ifIoctl` backed by per-netns `lo.up` shared with
  `guestIfaces`/netlink (no blind no-op). Defer UDP data, IPv6 semantics,
  `SO_REUSEPORT`, raw, multicast, advanced TCP. Keep `Config.Net=false, netns==0`
  behavior unchanged. (Since changed: a run without `Net` gets this loopback
  too, as Linux does with no network; `Config.NoNet` keeps no INET socket.)
  Acceptance: real `podman run --network=none`; in-container client/server on
  127.0.0.1; same port bound in two different netns without collision;
  host-listener isolation; wildcard/port-0; nonblocking readiness; fork/dup
  lifetime; adversarial DNS/control-message bypass attempts — on all three hosts.
- **Phase 2 (done): cross-netns on the default network, driven by real podman.**
  The vnet registry is scoped: loopback (127/8) is per-namespace; the virtual
  subnet (vnetSubnet, 10.88.0.0/16, podman's default) is shared across the run's
  namespaces. podman's default network backend, netavark, is replaced by a shim
  (lx_netavark.go, hooked in execve): it answers netavark's protocol, reuses the
  address podman allocated, and records which namespace each address belongs to
  (`dnet.ipNetns`). A connect to a network address resolves to the owning
  namespace and matches an exact-address or 0.0.0.0 listener there, so a server
  bound to all addresses is reached from another container; a 127/8 bind stays
  private. AF_INET6 is accepted as dual-stack — :: / ::1 / ::ffff:a.b.c.d map
  onto the IPv4 registry so a `::`-bound server is reachable by IPv4. Verified
  with bare `podman run` and two containers talking over the default network
  (testdata/xnetns.c, vnet_test.go). Still to do: multiple named networks, and a
  broker for cross-run networks (dnet is run-scoped).
- **Phase 3 (mostly done): host egress, DNS, port publishing.**
  - *Done:* policy-controlled host **TCP egress** (a container reaches the
    internet, relayed through a host socket; `--network=none` stays isolated);
    **DNS** resolution via the supervisor's resolver proxy (IPv4-only so the
    container uses addresses egress can reach); **UDP egress** for connected
    datagram sockets (NTP, QUIC); inbound **TCP port publishing** (`-p host:ctr`,
    supervisor listens on the host port and relays into the vnet — needs
    podman's `enable_port_reservation=false` so the supervisor owns the port).
    `apk add` inside a default-network container works end to end.

  ## TODO (remaining, niche)
  - **UDP port publishing** (`-p` for `/udp`) — datagram inbound relay.
  - **Unconnected UDP egress** (`sendto` without `connect`) and **intra-network
    UDP** (container-to-container datagrams) — only connected UDP egress and the
    `:53` DNS proxy are realized.
  - **Container-name DNS** — aardvark is shimmed away, so containers resolve each
    other by address, not name. (Host names resolve fine via the proxy.)
  - **`-p` without config** — auto-provide `enable_port_reservation=false` so
    publishing works out of the box; today it is a documented `containers.conf.d`
    drop-in. No clean guest-`/etc` injection hook exists yet (root is read-only).
  - **Multiple named networks** and a **cross-run broker** (`dnet` is run-scoped).
  - **Real global IPv6** and packet-level features (raw/ICMP/pcap/iptables) stay
    out of the endpoint-pairing contract by design.

## Biggest risk

An incomplete boundary that silently reaches existing host/DNS paths. Bounded
structurally: immutable socket ownership, explicit backend dispatch, no host
fallback for virtual sockets, and adversarial tests on every I/O entry point.
