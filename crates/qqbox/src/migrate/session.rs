//! Affine typestate for a migration, plus the two-sided wire exchange.
//!
//! Each transition consumes `self`; `Session` is deliberately not `Clone`, so
//! a phase cannot be reused or committed twice. Every privileged transition
//! requires a `Capability` passed as a parameter. All IO is explicit: the
//! reader and writer are arguments, never ambient state.

use std::io::{Read, Write};
use std::marker::PhantomData;

use crate::migrate::state::{Delta, MigrationError, MigrationState};
use crate::migrate::wire::{
    ack_payload, decode_ack, decode_delta, decode_hello, decode_state, default_hello,
    delta_payload, hello_payload, read_expect, state_payload, write_frame, Ack, Capability, Kind,
    MAX_FRAME,
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
        r: &mut R,
        w: &mut W,
        d: &Delta,
    ) -> Result<Session<Streaming>, MigrationError> {
        require(cap, &self.capability)?;
        write_frame(w, Kind::Delta, &delta_payload(d)?)?;
        self.read_ack(r, "streaming")
    }

    fn read_ack<R: Read>(
        mut self,
        r: &mut R,
        phase: &'static str,
    ) -> Result<Session<Streaming>, MigrationError> {
        let bytes = read_expect(r, Kind::Ack)?;
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

    write_frame(w, Kind::Hello, &hello_payload(&default_hello(cap.clone()))?)?;
    write_frame(w, Kind::State, &state_payload(&session.state)?)?;

    let mut streaming = session;
    for newer in deltas {
        let (next, d) = streaming.push(cap, newer)?;
        streaming = next.send_delta(cap, r, w, &d)?;
    }

    write_frame(w, Kind::Commit, &[])?;
    let bytes = read_expect(r, Kind::Ack)?;
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
    let hello_bytes = read_expect(r, Kind::Hello)?;
    let hello = decode_hello(&hello_bytes)?;
    if hello.wire_version != super::wire::WIRE_VERSION
        || hello.state_version != super::state::STATE_VERSION
    {
        return Err(MigrationError::Wire(format!(
            "peer version wire={} state={}",
            hello.wire_version, hello.state_version
        )));
    }
    if !cap.matches(&hello.capability) {
        return Err(MigrationError::Capability);
    }
    if hello.max_frame > MAX_FRAME {
        return Err(MigrationError::Wire(
            "peer max_frame exceeds bound".to_owned(),
        ));
    }

    let state_bytes = read_expect(r, Kind::State)?;
    let state = decode_state(&state_bytes)?;
    let mut session = Session::<Idle>::open(state, cap.clone())?
        .prepare(cap)?
        .begin(cap)?;

    loop {
        let frame = super::wire::read_frame(r)?;
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
                write_frame(w, Kind::Ack, &ack_payload(&ack)?)?;
            }
            Kind::Commit => break,
            other => {
                return Err(MigrationError::Wire(format!(
                    "unexpected frame {other:?} during streaming"
                )))
            }
        }
    }

    // `apply` is the real restore morphism: it must succeed on the received
    // canonical state before we acknowledge the cutover.
    let applied = session.state.apply()?;
    let mut recaptured =
        MigrationState::capture(&applied.config, &applied.guest, &applied.namespace);
    recaptured.generation = session.state.generation;
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
    write_frame(w, Kind::Ack, &ack_payload(&ack)?)?;

    let digest = session.digest;
    let mut committed = session.commit(cap)?;
    committed.peer_digest = Some(digest);
    committed.finish(cap)
}
