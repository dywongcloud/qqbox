//! Framed, integrity-checked migration wire protocol.
//!
//! Every IO effect is parameterized: readers and writers are passed in, and no
//! ambient/global state is consulted. Frame length is bounded before any
//! allocation. The capability token is a runtime parameter, never a literal in
//! this source (`forall k in Secrets: k not in AST`). Digest comparison is
//! constant-time to avoid a timing side channel.

use std::io::{Read, Write};

use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};

use crate::migrate::state::{Delta, MigrationError, MigrationState, STATE_VERSION};

pub const MAGIC: [u8; 4] = *b"QQBM";
pub const WIRE_VERSION: u8 = 1;
/// Hard bound: a frame larger than this is rejected before allocation.
pub const MAX_FRAME: u32 = 16 * 1024 * 1024;
pub const HEADER_LEN: usize = 4 + 1 + 1 + 4;
pub const DIGEST_LEN: usize = 32;

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
pub enum Kind {
    Hello = 1,
    State = 2,
    Delta = 3,
    Commit = 4,
    Ack = 5,
    Reject = 6,
}

impl Kind {
    fn from_byte(b: u8) -> Option<Kind> {
        match b {
            1 => Some(Kind::Hello),
            2 => Some(Kind::State),
            3 => Some(Kind::Delta),
            4 => Some(Kind::Commit),
            5 => Some(Kind::Ack),
            6 => Some(Kind::Reject),
            _ => None,
        }
    }
}

/// A capability is the SHA-256 of a caller-supplied secret. Derivation is pure
/// and the secret itself is never stored in the frame or in this source.
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub struct Capability {
    pub digest: [u8; 32],
}

impl Capability {
    pub fn derive(secret: &[u8]) -> Capability {
        let out = Sha256::digest(secret);
        let mut d = [0u8; 32];
        d.copy_from_slice(&out);
        Capability { digest: d }
    }

    /// Constant-time equality. Length is fixed by the type, so there is no
    /// early-exit branch on a secret-dependent index.
    pub fn matches(&self, other: &Capability) -> bool {
        let mut diff = 0u8;
        for (a, b) in self.digest.iter().zip(other.digest.iter()) {
            diff |= a ^ b;
        }
        diff == 0
    }

    pub fn hex(&self) -> String {
        hex::encode(self.digest)
    }
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub struct Hello {
    pub wire_version: u8,
    pub state_version: u8,
    pub capability: Capability,
    pub max_frame: u32,
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub struct Ack {
    pub generation: u64,
    pub digest: [u8; 32],
    pub phase: String,
}

#[derive(Debug, Clone)]
pub struct Frame {
    pub kind: Kind,
    pub payload: Vec<u8>,
}

/// Payload encoders. All return `Result`; none can panic.
pub fn state_payload(state: &MigrationState) -> Result<Vec<u8>, MigrationError> {
    serde_json::to_vec(state).map_err(|e| MigrationError::Wire(format!("state encode: {e}")))
}

pub fn decode_state(payload: &[u8]) -> Result<MigrationState, MigrationError> {
    serde_json::from_slice(payload).map_err(|e| MigrationError::Wire(format!("state decode: {e}")))
}

pub fn delta_payload(delta: &Delta) -> Result<Vec<u8>, MigrationError> {
    serde_json::to_vec(delta).map_err(|e| MigrationError::Wire(format!("delta encode: {e}")))
}

pub fn decode_delta(payload: &[u8]) -> Result<Delta, MigrationError> {
    serde_json::from_slice(payload).map_err(|e| MigrationError::Wire(format!("delta decode: {e}")))
}

pub fn ack_payload(ack: &Ack) -> Result<Vec<u8>, MigrationError> {
    serde_json::to_vec(ack).map_err(|e| MigrationError::Wire(format!("ack encode: {e}")))
}

pub fn decode_ack(payload: &[u8]) -> Result<Ack, MigrationError> {
    serde_json::from_slice(payload).map_err(|e| MigrationError::Wire(format!("ack decode: {e}")))
}

pub fn hello_payload(hello: &Hello) -> Result<Vec<u8>, MigrationError> {
    serde_json::to_vec(hello).map_err(|e| MigrationError::Wire(format!("hello encode: {e}")))
}

pub fn decode_hello(payload: &[u8]) -> Result<Hello, MigrationError> {
    serde_json::from_slice(payload).map_err(|e| MigrationError::Wire(format!("hello decode: {e}")))
}

fn header(kind: Kind, len: u32) -> [u8; HEADER_LEN] {
    let mut h = [0u8; HEADER_LEN];
    h[..4].copy_from_slice(&MAGIC);
    h[4] = WIRE_VERSION;
    h[5] = kind as u8;
    h[6..10].copy_from_slice(&len.to_be_bytes());
    h
}

fn framed_digest(kind: Kind, payload: &[u8]) -> [u8; 32] {
    let mut hasher = Sha256::new();
    hasher.update(header(kind, payload.len() as u32));
    hasher.update(payload);
    let out = hasher.finalize();
    let mut d = [0u8; 32];
    d.copy_from_slice(&out);
    d
}

/// Write one frame. `w` is an explicit parameter; no implicit IO.
pub fn write_frame<W: Write>(w: &mut W, kind: Kind, payload: &[u8]) -> Result<(), MigrationError> {
    let len = u32::try_from(payload.len())
        .map_err(|_| MigrationError::Wire("payload exceeds u32".to_owned()))?;
    if len > MAX_FRAME {
        return Err(MigrationError::Wire(format!(
            "frame {len} exceeds MAX_FRAME {MAX_FRAME}"
        )));
    }
    let h = header(kind, len);
    let digest = framed_digest(kind, payload);
    w.write_all(&h)
        .map_err(|e| MigrationError::Wire(format!("header write: {e}")))?;
    w.write_all(payload)
        .map_err(|e| MigrationError::Wire(format!("payload write: {e}")))?;
    w.write_all(&digest)
        .map_err(|e| MigrationError::Wire(format!("digest write: {e}")))?;
    w.flush()
        .map_err(|e| MigrationError::Wire(format!("flush: {e}")))
}

/// Read one frame. Length is validated before allocation; every error is typed.
pub fn read_frame<R: Read>(r: &mut R) -> Result<Frame, MigrationError> {
    let mut h = [0u8; HEADER_LEN];
    r.read_exact(&mut h)
        .map_err(|e| MigrationError::Wire(format!("header read: {e}")))?;
    if h[..4] != MAGIC {
        return Err(MigrationError::Wire("bad magic".to_owned()));
    }
    if h[4] != WIRE_VERSION {
        return Err(MigrationError::Wire(format!(
            "unsupported wire version {}",
            h[4]
        )));
    }
    let kind = Kind::from_byte(h[5])
        .ok_or_else(|| MigrationError::Wire(format!("unknown frame kind {}", h[5])))?;
    let mut len_bytes = [0u8; 4];
    len_bytes.copy_from_slice(&h[6..10]);
    let len = u32::from_be_bytes(len_bytes);
    if len > MAX_FRAME {
        return Err(MigrationError::Wire(format!(
            "frame {len} exceeds MAX_FRAME {MAX_FRAME}"
        )));
    }
    let mut payload = vec![0u8; len as usize];
    r.read_exact(&mut payload)
        .map_err(|e| MigrationError::Wire(format!("payload read: {e}")))?;
    let mut got = [0u8; DIGEST_LEN];
    r.read_exact(&mut got)
        .map_err(|e| MigrationError::Wire(format!("digest read: {e}")))?;
    let expected = framed_digest(kind, &payload);
    if !constant_time_eq(&got, &expected) {
        return Err(MigrationError::Wire("integrity digest mismatch".to_owned()));
    }
    Ok(Frame { kind, payload })
}

pub fn constant_time_eq(a: &[u8; 32], b: &[u8; 32]) -> bool {
    let mut diff = 0u8;
    for (x, y) in a.iter().zip(b.iter()) {
        diff |= x ^ y;
    }
    diff == 0
}

/// Read a frame and require its kind. Prevents a type-confusion frame from
/// reaching a decoder that expects another shape.
pub fn read_expect<R: Read>(r: &mut R, expected: Kind) -> Result<Vec<u8>, MigrationError> {
    let frame = read_frame(r)?;
    if frame.kind != expected {
        return Err(MigrationError::Wire(format!(
            "expected {:?} frame, got {:?}",
            expected, frame.kind
        )));
    }
    Ok(frame.payload)
}

pub fn default_hello(capability: Capability) -> Hello {
    Hello {
        wire_version: WIRE_VERSION,
        state_version: STATE_VERSION,
        capability,
        max_frame: MAX_FRAME,
    }
}
