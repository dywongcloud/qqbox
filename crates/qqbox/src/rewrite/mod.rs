pub mod cache;
pub mod classify;

pub use cache::{arch_key, classify_cached, default_cache, digest, Cache, Key};
pub use classify::{classify, patch};
