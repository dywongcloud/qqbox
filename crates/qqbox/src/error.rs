use thiserror::Error;

#[derive(Debug, Error)]
pub enum Error {
    #[error("guest killed by the syscall backstop: rejected syscall")]
    Killed,
    #[error("bad image reference: {0}")]
    BadRef(String),
    #[error("unsupported platform: {0}")]
    UnsupportedPlatform(String),
    #[error("{0} has no entrypoint or command")]
    NoEntrypoint(String),
    #[error("image exceeds the unpack size limit")]
    ImageTooLarge,
    #[error("network denied: {0}")]
    NetworkDenied(String),
    #[error("path escapes the namespace: {0}")]
    PathEscape(String),
    #[error("not found: {0}")]
    NotFound(String),
    #[error("invalid config: {0}")]
    Config(String),
    #[error("{0}")]
    Message(String),
    #[error(transparent)]
    Io(#[from] std::io::Error),
    #[error(transparent)]
    Json(#[from] serde_json::Error),
}

pub type Result<T> = std::result::Result<T, Error>;
