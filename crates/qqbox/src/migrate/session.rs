//! Affine typestate for a migration, plus the two-sided wire exchange.
//!
//! Each transition consumes `self`; `Session` is deliberately not `Clone`, so
//! a phase cannot be reused or committed twice. Every privileged transition
//! requires a `Capability` passed as a parameter. All IO is explicit: the
//! reader and writer are arguments, never ambient state.

use std::io::{Read, Write};
use std::marker::PhantomData;

use crate::migrate::state::{Delta, MigrationError, MigrationState, STATE_VERSION};
use crate::migrate::wire::{
    ack_payload, decode_ack, decode_delta, decode_hello, decode_hello_ack, decode_state,
    default_hello, default_hello_ack, delta_payload, fresh_nonce, hello_ack_payload, hello_payload,
    read_expect_keyed, read_frame_keyed, state_payload, write_frame_keyed, Ack, Capability, Kind,
    WireState, HEADER_LEN, MAX_FRAME,
};

pub trait Phase {
    const NAME: &'static str;
}

#[derive(Debug)]
pub struct Idle;
#[derive(Debug)]
pub struct Prepared;
#[derive(Debug)]
pub struct Streaming;
#[derive(Debug)]
pub struct Committed;
#[derive(Debug)]
pub struct Restored;

impl Phase for Idle {
    const NAME: &'static str = "idle";
}
impl Phase for Prepared {
    const NAME: &'static str = "prepared";
}
impl Phase for Streaming {
    const NAME: &'static str = "streaming";
}
impl Phase for Committed {
    const NAME: &'static str = "committed";
}
impl Phase for Restored {
    const NAME: &'static str = "restored";
}

/// No `Clone`, no `Copy`: the affine resource may be moved exactly once.
pub struct Session<P: Phase> {
    state: MigrationState,
    capability: Capability,
    rounds: u64,
    digest: [u8; 32],
    peer_digest: Option<[u8; 32]>,
    _phase: PhantomData<P>,
}

fn rephase<P: Phase, Q: Phase>(s: Session<P>) -> Session<Q> {
    Session {
        state: s.state,
        capability: s.capability,
        rounds: s.rounds,
        digest: s.digest,
        peer_digest: s.peer_digest,
        _phase: PhantomData,
    }
}

fn require(cap: &Capability, expected: &Capability) -> Result<(), MigrationError> {
    if cap.matches(expected) {
        Ok(())
    } else {
        Err(MigrationError::Capability)
    }
}

impl Session<Idle> {
    /// Build a session from real captured state and an explicit capability.
    pub fn open(state: MigrationState, capability: Capability) -> Result<Self, MigrationError> {
        let digest = state.digest()?;
        Ok(Session {
            state,
            capability,
            rounds: 0,
            digest,
            peer_digest: None,
            _phase: PhantomData,
        })
    }

    /// Freeze the source at generation 0 and normalize it. Consumes `self`.
    pub fn prepare(self, cap: &Capability) -> Result<Session<Prepared>, MigrationError> {
        require(cap, &self.capability)?;
        let mut out = rephase(self);
        out.state = out.state.normalize();
        out.state.generation = 0;
        out.digest = out.state.digest()?;
        Ok(out)
    }
}

impl Session<Prepared> {
    /// Enter the pre-copy phase. Consumes `self`.
    pub fn begin(self, cap: &Capability) -> Result<Session<Streaming>, MigrationError> {
        require(cap, &self.capability)?;
        Ok(rephase(self))
    }
}

impl Session<Streaming> {
    /// Pre-copy round. `newer` is a fresh capture; only changed partitions go
    /// on the wire. Assignment-based apply makes the round idempotent.
    pub fn push(
        mut self,
        cap: &Capability,
        newer: &MigrationState,
    ) -> Result<(Session<Streaming>, Delta), MigrationError> {
        require(cap, &self.capability)?;
        let mut target = newer.clone();
        target.generation = self.rounds.saturating_add(1);
        let d = self.state.delta(&target);
        self.state = self.state.apply_delta(&d)?;
        self.digest = self.state.digest()?;
        self.rounds = target.generation;
        Ok((self, d))
    }

    /// Send one delta frame and read its ack. IO is passed in explicitly.
    pub fn send_delta<R: Read, W: Write>(
        self,
        cap: &Capability,
        st: &mut WireState,
        r: &mut R,
        w: &mut W,
        d: &Delta,
    ) -> Result<Session<Streaming>, MigrationError> {
        require(cap, &self.capability)?;
        write_frame_keyed(w, Kind::Delta, &delta_payload(d)?, st)?;
        self.read_ack(r, cap, st, "streaming")
    }

    fn read_ack<R: Read>(
        mut self,
        r: &mut R,
        cap: &Capability,
        st: &mut WireState,
        phase: &'static str,
    ) -> Result<Session<Streaming>, MigrationError> {
        require(cap, &self.capability)?;
        let bytes = read_expect_keyed(r, Kind::Ack, st)?;
        let ack: Ack = decode_ack(&bytes)?;
        if ack.phase != phase {
            return Err(MigrationError::Phase {
                expected: phase,
                found: ack.phase.clone(),
            });
        }
        self.peer_digest = Some(ack.digest);
        Ok(self)
    }

    /// Final cutover: consumes `self`; no further deltas are possible.
    pub fn commit(self, cap: &Capability) -> Result<Session<Committed>, MigrationError> {
        require(cap, &self.capability)?;
        Ok(rephase(self))
    }
}

impl Session<Committed> {
    pub fn digest(&self) -> [u8; 32] {
        self.digest
    }

    pub fn peer_digest(&self) -> Option<[u8; 32]> {
        self.peer_digest
    }

    pub fn rounds(&self) -> u64 {
        self.rounds
    }

    /// Verify the peer applied the same canonical state. Total: a mismatch is
    /// a typed error, not a panic.
    pub fn finish(self, cap: &Capability) -> Result<Session<Restored>, MigrationError> {
        require(cap, &self.capability)?;
        match self.peer_digest {
            Some(peer) if !super::wire::constant_time_eq(&peer, &self.digest) => Err(
                MigrationError::Wire("source/destination digest mismatch".to_owned()),
            ),
            Some(_) => Ok(rephase(self)),
            None => Err(MigrationError::Wire("no peer digest received".to_owned())),
        }
    }
}

impl Session<Restored> {
    pub fn state(&self) -> &MigrationState {
        &self.state
    }

    pub fn digest(&self) -> [u8; 32] {
        self.digest
    }

    pub fn peer_digest(&self) -> Option<[u8; 32]> {
        self.peer_digest
    }

    pub fn rounds(&self) -> u64 {
        self.rounds
    }
}

/// Source side: hello, full state, optional pre-copy deltas, commit. Returns
/// the consumed `Restored` session so the caller can read both digests.
#[allow(clippy::too_many_arguments)]
pub fn send<R: Read, W: Write>(
    r: &mut R,
    w: &mut W,
    state: MigrationState,
    cap: &Capability,
    deltas: &[MigrationState],
) -> Result<Session<Restored>, MigrationError> {
    let session = Session::<Idle>::open(state, cap.clone())?
        .prepare(cap)?
        .begin(cap)?;

    let mut st = WireState::source(cap);
    let client_nonce = fresh_nonce()?;
    let hello = default_hello(client_nonce);
    write_frame_keyed(w, Kind::Hello, &hello_payload(&hello)?, &mut st)?;

    // The destination answers with its own fresh nonce. Both peers then derive
    // the same session key from the shared secret and BOTH nonces; the key is
    // never on the wire. Replaying a recorded session cannot reproduce it.
    let ack_bytes = read_expect_keyed(r, Kind::HelloAck, &mut st)?;
    let hello_ack = decode_hello_ack(&ack_bytes)?;
    if hello_ack.wire_version != super::wire::WIRE_VERSION
        || hello_ack.state_version != STATE_VERSION
    {
        return Err(MigrationError::Wire(format!(
            "peer version wire={} state={}",
            hello_ack.wire_version, hello_ack.state_version
        )));
    }
    if hello_ack.max_frame > MAX_FRAME {
        return Err(MigrationError::Wire(
            "peer max_frame exceeds bound".to_owned(),
        ));
    }
    let limit = hello.max_frame.min(hello_ack.max_frame).min(MAX_FRAME);
    if limit < HEADER_LEN as u32 + 1 {
        return Err(MigrationError::Wire(format!(
            "negotiated max_frame {limit} is below the minimum frame size"
        )));
    }
    st.establish(cap, &client_nonce, &hello_ack.server_nonce, limit);

    write_frame_keyed(w, Kind::State, &state_payload(&session.state)?, &mut st)?;

    let mut streaming = session;
    for newer in deltas {
        let (next, d) = streaming.push(cap, newer)?;
        streaming = next.send_delta(cap, &mut st, r, w, &d)?;
    }

    // The source's canonical digest travels in the Commit payload, so the
    // destination can verify parity instead of comparing a value to itself.
    let source_digest = streaming.digest;
    write_frame_keyed(w, Kind::Commit, &source_digest, &mut st)?;
    let bytes = read_expect_keyed(r, Kind::Ack, &mut st)?;
    let ack: Ack = decode_ack(&bytes)?;
    if ack.phase != Committed::NAME {
        return Err(MigrationError::Phase {
            expected: Committed::NAME,
            found: ack.phase.clone(),
        });
    }
    let mut committed = streaming.commit(cap)?;
    committed.peer_digest = Some(ack.digest);
    committed.finish(cap)
}

/// Destination side: verify hello, receive full state + deltas, apply, ack.
pub fn receive<R: Read, W: Write>(
    r: &mut R,
    w: &mut W,
    cap: &Capability,
) -> Result<Session<Restored>, MigrationError> {
    let mut st = WireState::destination(cap);
    let hello_bytes = read_expect_keyed(r, Kind::Hello, &mut st)?;
    let hello = decode_hello(&hello_bytes)?;
    if hello.wire_version != super::wire::WIRE_VERSION || hello.state_version != STATE_VERSION {
        return Err(MigrationError::Wire(format!(
            "peer version wire={} state={}",
            hello.wire_version, hello.state_version
        )));
    }
    if hello.max_frame > MAX_FRAME {
        return Err(MigrationError::Wire(
            "peer max_frame exceeds bound".to_owned(),
        ));
    }
    // The smaller of the two peers' bounds governs every subsequent frame,
    // in BOTH directions.
    let limit = hello.max_frame.min(MAX_FRAME);
    if limit < HEADER_LEN as u32 + 1 {
        return Err(MigrationError::Wire(format!(
            "peer max_frame {limit} is below the minimum frame size"
        )));
    }
    let server_nonce = fresh_nonce()?;
    write_frame_keyed(
        w,
        Kind::HelloAck,
        &hello_ack_payload(&default_hello_ack(server_nonce))?,
        &mut st,
    )?;
    st.establish(cap, &hello.client_nonce, &server_nonce, limit);

    let state_bytes = read_expect_keyed(r, Kind::State, &mut st)?;
    let state = decode_state(&state_bytes)?;
    if state.version != STATE_VERSION {
        return Err(MigrationError::Version {
            found: state.version,
            supported: STATE_VERSION,
        });
    }
    let mut session = Session::<Idle>::open(state, cap.clone())?
        .prepare(cap)?
        .begin(cap)?;

    let source_digest = loop {
        let frame = read_frame_keyed(r, &mut st)?;
        match frame.kind {
            Kind::Delta => {
                let d = decode_delta(&frame.payload)?;
                session.state = session.state.apply_delta(&d)?;
                session.digest = session.state.digest()?;
                session.rounds = d.generation;
                let ack = Ack {
                    generation: session.rounds,
                    digest: session.digest,
                    phase: Streaming::NAME.to_string(),
                };
                write_frame_keyed(w, Kind::Ack, &ack_payload(&ack)?, &mut st)?;
            }
            Kind::Commit => {
                if frame.payload.len() != 32 {
                    return Err(MigrationError::Wire(
                        "commit frame must carry the source digest".to_owned(),
                    ));
                }
                let mut digest = [0u8; 32];
                digest.copy_from_slice(&frame.payload);
                if !super::wire::constant_time_eq(&digest, &session.digest) {
                    return Err(MigrationError::Wire(
                        "source/destination digest mismatch at commit".to_owned(),
                    ));
                }
                break digest;
            }
            other => {
                return Err(MigrationError::Wire(format!(
                    "unexpected frame {other:?} during streaming"
                )))
            }
        }
    };

    // `apply` is the real restore morphism: it must succeed on the received
    // canonical state before we acknowledge the cutover.
    let applied = session.state.apply()?;
    let recaptured = MigrationState::recapture(&applied, &session.state);
    if recaptured.normalize() != session.state.normalize() {
        return Err(MigrationError::Wire(
            "capture(apply(state)) != state".to_owned(),
        ));
    }

    let ack = Ack {
        generation: session.rounds,
        digest: session.digest,
        phase: Committed::NAME.to_string(),
    };
    write_frame_keyed(w, Kind::Ack, &ack_payload(&ack)?, &mut st)?;

    let mut committed = session.commit(cap)?;
    committed.peer_digest = Some(source_digest);
    committed.finish(cap)
}
