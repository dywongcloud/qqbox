//! Live migration for qqbox: a versioned control-plane state ADT, a framed
//! integrity-checked wire protocol, an affine typestate session, and a bounded
//! pre-copy/commit/restore exchange.
//!
//! ## Scope, stated precisely
//!
//! qqbox is **not a VM**. It runs an unmodified Linux ELF in a host process and
//! rewrites syscall instructions; the supervisor owns the data plane. The three
//! native guest backends (Linux memfd/seccomp/hop, macOS Mach/Seatbelt/HVF,
//! Windows LPAC) are not ported to Rust in this tree, so there is no guest
//! physical memory, no vCPU register file, no dirty-page bitmap and no VMM to
//! suspend. Firecracker-style **guest memory** live migration is therefore
//! impossible here and is not claimed.
//!
//! What this module migrates is the state the Rust supervisor actually owns:
//! the workload spec, the vfs Namespace (the only filesystem), the chanPage/fd
//! table, and the egress/port-binding policy. Everything is a pure value;
//! `capture`/`apply` form an isomorphism on that value (`f o f = f` after
//! canonicalization), the wire is bounded and digest-checked, and the phase
//! machine is affine and capability-gated.
//!
//! ## Design invariants
//!
//! - `State == ADT-isomorphic pure morphisms`: `state::capture` / `state::apply`.
//! - `Effects == IO_Monad`: all readers/writers are explicit arguments in
//!   `wire::write_frame`/`read_frame` and `session::send`/`receive`.
//! - `Boundaries == affine typestate`: `Session<P>` is consumed by each
//!   transition and is not `Clone`; no phase can be committed twice.
//! - `AuthZ == ZeroTrust`: every privileged transition requires a matching
//!   `Capability`; the bearer token gates the daemon endpoints.
//! - `forall q in IO: q == Parameterized`: no ambient state, no env reads, no
//!   global mutable counters, no command interpolation.
//! - Every error is handled or propagated as a typed `MigrationError`.

pub mod session;
pub mod state;
pub mod wire;

pub use session::{receive, send, Committed, Idle, Phase, Prepared, Restored, Session, Streaming};
pub use state::{
    Applied, Boundary, ChanState, Delta, FsState, GuestExecution, MigrationError, MigrationState,
    NetState, ResolvedBinding, Spec, STATE_VERSION,
};
pub use wire::{
    ack_payload, constant_time_eq, decode_ack, decode_delta, decode_hello, decode_state,
    delta_payload, hello_payload, read_expect, read_frame, state_payload, write_frame, Ack,
    Capability, Frame, Hello, Kind, DIGEST_LEN, HEADER_LEN, MAGIC, MAX_FRAME, WIRE_VERSION,
};

/// Human-readable feature/boundary description for the CLI and daemon.
pub fn features() -> serde_json::Value {
    serde_json::json!({
        "module": "qqbox-migrate",
        "stateVersion": STATE_VERSION,
        "wireVersion": WIRE_VERSION,
        "maxFrameBytes": MAX_FRAME,
        "phases": [Idle::NAME, Prepared::NAME, Streaming::NAME, Committed::NAME, Restored::NAME],
        "partitions": ["spec", "fs", "chan", "net"],
        "partitionDisjointness": "capture destructures Config exhaustively; each field is moved into exactly one partition",
        "laws": [
            "capture(apply(s)) == s",
            "normalize(normalize(s)) == normalize(s)",
            "apply_delta(apply_delta(s,d),d) == apply_delta(s,d)",
            "digest(s) == digest(normalize(s))"
        ],
        "preCopy": "iterative assignment-based deltas, then a single commit barrier",
        "capability": "SHA-256 of a caller-supplied secret; constant-time match",
        "integrity": "SHA-256 over header+payload, constant-time compare",
        "guestExecution": "NotCapturable",
        "guestExecutionReason": "no VMM/hypervisor; the three native guest backends are not ported to Rust; see docs/LIVE_MIGRATION.md",
        "unsupported": [
            "guest physical memory", "vCPU registers", "dirty-page tracking",
            "in-flight syscalls", "real OS fd/TCP connection state", "guest quiesce"
        ]
    })
}
