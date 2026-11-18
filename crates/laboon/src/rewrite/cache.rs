use std::collections::HashMap;
use std::sync::{Arc, OnceLock, RwLock};

use sha2::{Digest, Sha256};

use crate::types::Arch;

pub type Key = [u8; 32];

pub fn digest(code: &[u8]) -> Key {
    let mut hasher = Sha256::new();
    hasher.update(code);
    let bytes = hasher.finalize();
    let mut key = [0u8; 32];
    key.copy_from_slice(bytes.as_ref());
    key
}

pub fn arch_key(arch: Arch, code: &[u8]) -> Key {
    let tag = match arch {
        Arch::Amd64 => 0u8,
        Arch::Arm64 => 1u8,
    };
    let mut hasher = Sha256::new();
    hasher.update([tag]);
    hasher.update(digest(code));
    let bytes = hasher.finalize();
    let mut key = [0u8; 32];
    key.copy_from_slice(bytes.as_ref());
    key
}

#[derive(Debug, Default)]
pub struct Cache {
    plans: RwLock<HashMap<Key, Arc<Vec<usize>>>>,
}

impl Cache {
    pub fn new() -> Self {
        Self {
            plans: RwLock::new(HashMap::new()),
        }
    }

    pub fn get(&self, code: &[u8]) -> Option<Vec<usize>> {
        self.get_key(&digest(code))
    }

    pub fn insert(&self, code: &[u8], sites: Vec<usize>) {
        self.insert_key(digest(code), sites);
    }

    pub fn get_key(&self, key: &Key) -> Option<Vec<usize>> {
        let guard = match self.plans.read() {
            Ok(guard) => guard,
            Err(poisoned) => poisoned.into_inner(),
        };
        guard.get(key).map(|sites| (**sites).clone())
    }

    pub fn insert_key(&self, key: Key, sites: Vec<usize>) {
        let mut guard = match self.plans.write() {
            Ok(guard) => guard,
            Err(poisoned) => poisoned.into_inner(),
        };
        guard.insert(key, Arc::new(sites));
    }

    pub fn len(&self) -> usize {
        let guard = match self.plans.read() {
            Ok(guard) => guard,
            Err(poisoned) => poisoned.into_inner(),
        };
        guard.len()
    }

    pub fn is_empty(&self) -> bool {
        self.len() == 0
    }

    pub fn clear(&self) {
        let mut guard = match self.plans.write() {
            Ok(guard) => guard,
            Err(poisoned) => poisoned.into_inner(),
        };
        guard.clear();
    }
}

pub fn default_cache() -> &'static Cache {
    static DEFAULT: OnceLock<Cache> = OnceLock::new();
    DEFAULT.get_or_init(Cache::new)
}

pub fn classify_cached(arch: Arch, code: &[u8], cache: &Cache) -> Vec<usize> {
    let key = arch_key(arch, code);
    if let Some(sites) = cache.get_key(&key) {
        return sites;
    }
    let sites = super::classify::classify(arch, code);
    cache.insert_key(key, sites.clone());
    sites
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn round_trip_is_keyed_by_content() {
        let cache = Cache::new();
        let code = [0x0f, 0x05];
        assert!(cache.get(&code).is_none());
        cache.insert(&code, vec![0]);
        assert_eq!(cache.get(&code), Some(vec![0]));
        assert!(cache.get(&[0x0f, 0x0b]).is_none());
        assert_eq!(cache.len(), 1);
        cache.clear();
        assert!(cache.is_empty());
    }

    #[test]
    fn cache_hit_holds_the_classifier_result() {
        let cache = Cache::new();
        let code = [0x90, 0x0f, 0x05, 0x0f, 0x34];
        assert_eq!(classify_cached(Arch::Amd64, &code, &cache), vec![1]);
        assert_eq!(cache.get_key(&arch_key(Arch::Amd64, &code)), Some(vec![1]));
        assert!(cache.get_key(&arch_key(Arch::Arm64, &code)).is_none());
    }

    #[test]
    fn default_cache_is_shared_across_threads() {
        let cache = default_cache();
        let handles: Vec<_> = (0u8..4)
            .map(|index| {
                std::thread::spawn(move || {
                    let code = [index];
                    cache.insert(&code, vec![usize::from(index)]);
                    cache.get(&code)
                })
            })
            .collect();
        for (index, handle) in handles.into_iter().enumerate() {
            assert_eq!(handle.join().unwrap_or(None), Some(vec![index]));
        }
        assert!(std::ptr::eq(default_cache(), cache));
    }
}
