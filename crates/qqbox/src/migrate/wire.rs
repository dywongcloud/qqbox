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
pub const WIRE_VERSION: u8 = 2;
/// Direction tags bound into every keyed MAC so a captured frame cannot be
/// reflected back along the other half of the duplex stream.
pub const DIR_SOURCE: u8 = 0x53;
pub const DIR_DEST: u8 = 0x44;
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
    HelloAck = 7,
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
            7 => Some(Kind::HelloAck),
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

/// A per-session key. It is derived from the caller secret plus two fresh
/// nonces and is NEVER serialized onto the wire; the old wire put the
/// capability digest itself in Hello and used it as the HMAC key, so a passive
/// reader could forge any subsequent frame.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct SessionKey {
    pub digest: [u8; 32],
}

impl SessionKey {
    fn hmac(key: &[u8; 32], parts: &[&[u8]]) -> SessionKey {
        let mut ipad = [0x36u8; 64];
        let mut opad = [0x5cu8; 64];
        for (i, byte) in key.iter().enumerate() {
            ipad[i] ^= *byte;
            opad[i] ^= *byte;
        }
        let mut inner = Sha256::new();
        inner.update(ipad);
        for part in parts {
            inner.update(part);
        }
        let ih = inner.finalize();
        let mut outer = Sha256::new();
        outer.update(opad);
        outer.update(ih);
        let out = outer.finalize();
        let mut digest = [0u8; 32];
        digest.copy_from_slice(&out);
        SessionKey { digest }
    }

    /// Key used only for the Hello / HelloAck handshake. Domain-separated so
    /// handshake frames can never be replayed as session frames.
    pub fn handshake(cap: &Capability) -> SessionKey {
        Self::hmac(&cap.digest, &[b"qqbox-migrate-hello-v2"])
    }

    /// Session key bound to BOTH peers' fresh nonces: replaying a recorded
    /// session verbatim cannot reproduce it.
    pub fn session(cap: &Capability, client: &[u8; 32], server: &[u8; 32]) -> SessionKey {
        Self::hmac(&cap.digest, &[b"qqbox-migrate-session-v2", client, server])
    }
}

/// CSPRNG nonce from the OS entropy device. This is an explicit IO effect,
/// not an ambient environment read; the same source backs the daemon token.
pub fn fresh_nonce() -> Result<[u8; 32], MigrationError> {
    use std::io::Read as _;
    let mut file = std::fs::File::open("/dev/urandom")
        .map_err(|e| MigrationError::Wire(format!("cannot open entropy: {e}")))?;
    let mut nonce = [0u8; 32];
    file.read_exact(&mut nonce)
        .map_err(|e| MigrationError::Wire(format!("cannot read entropy: {e}")))?;
    Ok(nonce)
}

/// Per-direction transcript state: outbound/inbound sequence counters, the
/// negotiated frame bound and the direction tags. Both peers advance these in
/// lockstep, so a dropped, duplicated, reordered or reflected frame fails its
/// MAC before it reaches a decoder.
pub struct WireState {
    key: SessionKey,
    out_seq: u64,
    in_seq: u64,
    limit: u32,
    write_dir: u8,
    read_dir: u8,
}

impl WireState {
    pub fn source(cap: &Capability) -> WireState {
        WireState {
            key: SessionKey::handshake(cap),
            out_seq: 0,
            in_seq: 0,
            limit: MAX_FRAME,
            write_dir: DIR_SOURCE,
            read_dir: DIR_DEST,
        }
    }

    pub fn destination(cap: &Capability) -> WireState {
        WireState {
            key: SessionKey::handshake(cap),
            out_seq: 0,
            in_seq: 0,
            limit: MAX_FRAME,
            write_dir: DIR_DEST,
            read_dir: DIR_SOURCE,
        }
    }

    pub fn establish(
        &mut self,
        cap: &Capability,
        client: &[u8; 32],
        server: &[u8; 32],
        limit: u32,
    ) {
        self.key = SessionKey::session(cap, client, server);
        self.limit = limit;
    }

    pub fn limit(&self) -> u32 {
        self.limit
    }
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub struct Hello {
    pub wire_version: u8,
    pub state_version: u8,
    pub client_nonce: [u8; 32],
    pub max_frame: u32,
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub struct HelloAck {
    pub wire_version: u8,
    pub state_version: u8,
    pub server_nonce: [u8; 32],
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

pub fn hello_ack_payload(ack: &HelloAck) -> Result<Vec<u8>, MigrationError> {
    serde_json::to_vec(ack).map_err(|e| MigrationError::Wire(format!("hello ack encode: {e}")))
}

pub fn decode_hello_ack(payload: &[u8]) -> Result<HelloAck, MigrationError> {
    serde_json::from_slice(payload)
        .map_err(|e| MigrationError::Wire(format!("hello ack decode: {e}")))
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

/// HMAC-SHA-256 over the frame header, payload, transcript sequence and
/// direction tag, keyed by the per-session key. The key is derived from the
/// caller secret plus two nonces and is never transmitted. Sequence and
/// direction binding makes every frame position-specific in each direction.
/// Local checkpoint files still use `framed_digest` (no secret is stored with
/// them); the two-process session uses this keyed form exclusively.
fn keyed_digest(kind: Kind, payload: &[u8], st: &WireState, seq: u64, dir: u8) -> [u8; 32] {
    let mut ipad = [0x36u8; 64];
    let mut opad = [0x5cu8; 64];
    for (i, byte) in st.key.digest.iter().enumerate() {
        ipad[i] ^= *byte;
        opad[i] ^= *byte;
    }
    let mut inner = Sha256::new();
    inner.update(ipad);
    inner.update(header(kind, payload.len() as u32));
    inner.update(seq.to_be_bytes());
    inner.update([dir]);
    inner.update(payload);
    let ih = inner.finalize();
    let mut outer = Sha256::new();
    outer.update(opad);
    outer.update(ih);
    let out = outer.finalize();
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

/// Write one capability-authenticated frame. Used on the wire only.
pub fn write_frame_keyed<W: Write>(
    w: &mut W,
    kind: Kind,
    payload: &[u8],
    st: &mut WireState,
) -> Result<(), MigrationError> {
    let len = u32::try_from(payload.len())
        .map_err(|_| MigrationError::Wire("payload exceeds u32".to_owned()))?;
    if len > MAX_FRAME || len > st.limit {
        return Err(MigrationError::Wire(format!(
            "frame {len} exceeds bound {}/{}",
            st.limit, MAX_FRAME
        )));
    }
    let h = header(kind, len);
    let digest = keyed_digest(kind, payload, st, st.out_seq, st.write_dir);
    st.out_seq = st
        .out_seq
        .checked_add(1)
        .ok_or_else(|| MigrationError::Wire("outbound sequence overflow".to_owned()))?;
    w.write_all(&h)
        .map_err(|e| MigrationError::Wire(format!("header write: {e}")))?;
    w.write_all(payload)
        .map_err(|e| MigrationError::Wire(format!("payload write: {e}")))?;
    w.write_all(&digest)
        .map_err(|e| MigrationError::Wire(format!("digest write: {e}")))?;
    w.flush()
        .map_err(|e| MigrationError::Wire(format!("flush: {e}")))
}

/// Read one capability-authenticated frame. `limit` is the negotiated frame
/// bound from Hello, so `max_frame` is a real negotiation field rather than a
/// value that is transmitted and then ignored.
pub fn read_frame_keyed<R: Read>(r: &mut R, st: &mut WireState) -> Result<Frame, MigrationError> {
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
    if len > st.limit {
        return Err(MigrationError::Wire(format!(
            "frame {len} exceeds negotiated limit {}",
            st.limit
        )));
    }
    let mut payload = vec![0u8; len as usize];
    r.read_exact(&mut payload)
        .map_err(|e| MigrationError::Wire(format!("payload read: {e}")))?;
    let mut got = [0u8; DIGEST_LEN];
    r.read_exact(&mut got)
        .map_err(|e| MigrationError::Wire(format!("digest read: {e}")))?;
    let expected = keyed_digest(kind, &payload, st, st.in_seq, st.read_dir);
    if !constant_time_eq(&got, &expected) {
        return Err(MigrationError::Wire(
            "authentication digest mismatch".to_owned(),
        ));
    }
    st.in_seq = st
        .in_seq
        .checked_add(1)
        .ok_or_else(|| MigrationError::Wire("inbound sequence overflow".to_owned()))?;
    Ok(Frame { kind, payload })
}

pub fn read_expect_keyed<R: Read>(
    r: &mut R,
    expected: Kind,
    st: &mut WireState,
) -> Result<Vec<u8>, MigrationError> {
    let frame = read_frame_keyed(r, st)?;
    if frame.kind != expected {
        return Err(MigrationError::Wire(format!(
            "expected {:?} frame, got {:?}",
            expected, frame.kind
        )));
    }
    Ok(frame.payload)
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

pub fn default_hello(client_nonce: [u8; 32]) -> Hello {
    Hello {
        wire_version: WIRE_VERSION,
        state_version: STATE_VERSION,
        client_nonce,
        max_frame: MAX_FRAME,
    }
}

pub fn default_hello_ack(server_nonce: [u8; 32]) -> HelloAck {
    HelloAck {
        wire_version: WIRE_VERSION,
        state_version: STATE_VERSION,
        server_nonce,
        max_frame: MAX_FRAME,
    }
}
