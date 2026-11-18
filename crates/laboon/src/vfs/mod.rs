use crate::error::{Error, Result};
use crate::types::Mount;
use std::path::{Component, Path, PathBuf};

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct ResolvedPath {
    pub host: PathBuf,
    pub guest: String,
    pub read_only: bool,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Default)]
pub struct OpenFlags {
    pub read: bool,
    pub write: bool,
    pub create: bool,
    pub o_path: bool,
}

#[derive(Debug, Clone)]
pub struct FileHandle {
    pub path: ResolvedPath,
}

#[derive(Debug, Clone)]
struct MountEntry {
    host: PathBuf,
    guest: String,
    read_only: bool,
}

#[derive(Debug, Clone)]
pub struct Namespace {
    root: PathBuf,
    mounts: Vec<MountEntry>,
}

pub fn clean_guest(name: &str) -> Option<String> {
    let mut parts: Vec<&str> = Vec::new();
    for raw in name.split('/') {
        match raw {
            "" | "." => {}
            ".." => {
                parts.pop();
            }
            other => parts.push(other),
        }
    }
    if parts.is_empty() {
        None
    } else {
        Some(parts.join("/"))
    }
}

pub fn confined(root: &Path, candidate: &Path) -> Result<PathBuf> {
    let base = std::fs::canonicalize(root).unwrap_or_else(|_| root.to_path_buf());
    let mut rel = PathBuf::new();
    for component in candidate.components() {
        match component {
            Component::Normal(s) => rel.push(s),
            Component::ParentDir => {
                rel.pop();
            }
            _ => {}
        }
    }
    let joined = base.join(&rel);
    if !joined.starts_with(&base) {
        return Err(Error::PathEscape(candidate.display().to_string()));
    }
    Ok(joined)
}

fn normalize_guest(guest: &str) -> Result<String> {
    let mut parts: Vec<&str> = Vec::new();
    for raw in guest.split('/') {
        match raw {
            "" | "." => {}
            ".." => {
                if parts.pop().is_none() {
                    return Err(Error::PathEscape(guest.to_string()));
                }
            }
            other => parts.push(other),
        }
    }
    Ok(parts.join("/"))
}

fn symlink_safe(path: &Path, base: &Path) -> Result<()> {
    let mut current = base.to_path_buf();
    for component in path.strip_prefix(base).unwrap_or(path).components() {
        let Component::Normal(name) = component else {
            continue;
        };
        current.push(name);
        if let Ok(target) = std::fs::read_link(&current) {
            if target.is_absolute() || target.components().any(|c| c == Component::ParentDir) {
                return Err(Error::PathEscape(current.display().to_string()));
            }
        }
    }
    Ok(())
}

impl Namespace {
    pub fn new(root: &Path) -> Result<Self> {
        let root = std::fs::canonicalize(root).unwrap_or_else(|_| root.to_path_buf());
        Ok(Namespace {
            root,
            mounts: vec![
                MountEntry {
                    host: PathBuf::from("/proc"),
                    guest: "proc".to_string(),
                    read_only: true,
                },
                MountEntry {
                    host: PathBuf::from("/sys"),
                    guest: "sys".to_string(),
                    read_only: true,
                },
                MountEntry {
                    host: PathBuf::from("/dev"),
                    guest: "dev".to_string(),
                    read_only: true,
                },
            ],
        })
    }

    pub fn mount(&mut self, m: &Mount) -> Result<()> {
        let guest = normalize_guest(&m.guest)?;
        if guest.is_empty() {
            self.root = std::fs::canonicalize(&m.host).unwrap_or_else(|_| PathBuf::from(&m.host));
            return Ok(());
        }
        if m.host.contains("..") {
            return Err(Error::PathEscape(m.host.clone()));
        }
        self.mounts.push(MountEntry {
            host: PathBuf::from(&m.host),
            guest,
            read_only: m.read_only,
        });
        self.mounts.sort_by(|a, b| b.guest.len().cmp(&a.guest.len()));
        Ok(())
    }

    pub fn resolve(&self, guest_path: &str) -> Result<ResolvedPath> {
        let guest = normalize_guest(guest_path)?;
        if guest.is_empty() {
            return Ok(ResolvedPath {
                host: self.root.clone(),
                guest: "/".to_string(),
                read_only: false,
            });
        }
        for entry in &self.mounts {
            if guest == entry.guest || guest.starts_with(&format!("{}/", entry.guest)) {
                let rest = guest
                    .strip_prefix(&entry.guest)
                    .unwrap_or("")
                    .trim_start_matches('/');
                let host_root = std::fs::canonicalize(&entry.host)
                    .unwrap_or_else(|_| entry.host.clone());
                let candidate = host_root.join(rest);
                let host = confined(&host_root, &candidate)?;
                symlink_safe(&host, &host_root)?;
                return Ok(ResolvedPath {
                    host,
                    guest: format!("/{guest}"),
                    read_only: entry.read_only,
                });
            }
        }
        let candidate = self.root.join(&guest);
        let host = confined(&self.root, &candidate)?;
        symlink_safe(&host, &self.root)?;
        Ok(ResolvedPath {
            host,
            guest: format!("/{guest}"),
            read_only: false,
        })
    }

    pub fn open(&self, guest_path: &str, flags: &OpenFlags) -> Result<FileHandle> {
        let resolved = self.resolve(guest_path)?;
        if flags.write && resolved.read_only {
            return Err(Error::Config("EROFS".into()));
        }
        if !flags.o_path && !flags.create && flags.read && !resolved.host.exists() {
            if self.synth(guest_path).is_none() {
                return Err(Error::NotFound(guest_path.to_string()));
            }
        }
        Ok(FileHandle { path: resolved })
    }

    pub fn read_dir(&self, guest_path: &str) -> Result<Vec<String>> {
        let resolved = self.resolve(guest_path)?;
        let mut out = Vec::new();
        if let Ok(entries) = std::fs::read_dir(&resolved.host) {
            for entry in entries.flatten() {
                out.push(entry.file_name().to_string_lossy().into_owned());
            }
        }
        if let Some(synth) = self.synth(guest_path) {
            for line in synth.lines() {
                if let Some(name) = line.split_whitespace().next() {
                    out.push(name.to_string());
                }
            }
        }
        out.sort();
        out.dedup();
        Ok(out)
    }

    pub fn synth(&self, guest_path: &str) -> Option<String> {
        let p = guest_path.trim_start_matches('/');
        if p == "proc" || p == "sys" || p == "dev" {
            return Some("self cpuinfo meminfo mounts net sys stat uptime\n".to_string());
        }
        if p == "proc/self/status" {
            return Some(
                "Name:\tguest\nPid:\t1\nUid:\t0\t0\t0\t0\nGid:\t0\t0\t0\t0\n".to_string(),
            );
        }
        if p == "proc/meminfo" {
            return Some("MemTotal:       1048576 kB\nMemFree:         524288 kB\n".to_string());
        }
        if p == "proc/cpuinfo" {
            return Some("processor\t: 0\nmodel name\t: laboon guest\n".to_string());
        }
        if p == "proc/mounts" {
            return Some("overlay / overlay rw 0 0\nproc /proc proc ro 0 0\n".to_string());
        }
        if p == "dev" {
            return Some("null zero random urandom tty pts stdin stdout stderr fd\n".to_string());
        }
        if p == "dev/null" || p == "dev/zero" {
            return Some(String::new());
        }
        if p.starts_with("sys/") || p.starts_with("proc/") || p.starts_with("dev/") {
            return Some(String::new());
        }
        None
    }

    pub fn allowed_devices() -> &'static [&'static str] {
        &["null", "zero", "full", "random", "urandom", "tty", "ptmx"]
    }
}

pub mod path {
    pub use super::{clean_guest, confined};
}

pub mod procfs {
    pub use super::Namespace;
}

pub mod devfs {
    pub use super::Namespace;
}
