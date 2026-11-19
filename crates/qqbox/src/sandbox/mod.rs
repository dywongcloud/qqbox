use crate::config::Config;
use crate::error::{Error, Result};
use crate::types::{Arch, Context, HostOs};
use crate::{net, rewrite, security, vfs, zig};
use std::path::Path;

#[repr(C)]
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct SockFilter {
    pub code: u16,
    pub jt: u8,
    pub jf: u8,
    pub k: u32,
}

pub const SECCOMP_RET_KILL_PROCESS: u32 = 0x8000_0000;
pub const SECCOMP_RET_TRAP: u32 = 0x0003_0000;
pub const SECCOMP_RET_ALLOW: u32 = 0x7fff_0000;
pub const SECCOMP_TRAP_TAG: u32 = 0x5ca1;

#[derive(Debug, Clone, Default)]
pub struct Segment {
    pub kind: u32,
    pub flags: u32,
    pub offset: u64,
    pub vaddr: u64,
    pub filesz: u64,
    pub memsz: u64,
}

#[derive(Debug, Clone, Default)]
pub struct Elf {
    pub class: u8,
    pub endian: u8,
    pub machine: u16,
    pub entry: u64,
    pub segments: Vec<Segment>,
}

fn u16_at(bytes: &[u8], at: usize, little: bool) -> Result<u16> {
    let pair = bytes
        .get(at..at + 2)
        .ok_or_else(|| Error::Config("truncated ELF".into()))?;
    Ok(if little {
        u16::from_le_bytes([pair[0], pair[1]])
    } else {
        u16::from_be_bytes([pair[0], pair[1]])
    })
}

fn u32_at(bytes: &[u8], at: usize, little: bool) -> Result<u32> {
    let q = bytes
        .get(at..at + 4)
        .ok_or_else(|| Error::Config("truncated ELF".into()))?;
    Ok(if little {
        u32::from_le_bytes([q[0], q[1], q[2], q[3]])
    } else {
        u32::from_be_bytes([q[0], q[1], q[2], q[3]])
    })
}

fn u64_at(bytes: &[u8], at: usize, little: bool) -> Result<u64> {
    let o = bytes
        .get(at..at + 8)
        .ok_or_else(|| Error::Config("truncated ELF".into()))?;
    let mut raw = [0u8; 8];
    raw.copy_from_slice(o);
    Ok(if little {
        u64::from_le_bytes(raw)
    } else {
        u64::from_be_bytes(raw)
    })
}

pub fn parse_elf(bytes: &[u8]) -> Result<Elf> {
    if bytes.len() < 16 || &bytes[..4] != b"\x7fELF" {
        return Err(Error::Config("not an ELF image".into()));
    }
    let class = bytes[4];
    let little = bytes[5] == 1;
    if class != 1 && class != 2 {
        return Err(Error::Config("unsupported ELF class".into()));
    }
    let is64 = class == 2;
    if bytes.len() < if is64 { 64 } else { 52 } {
        return Err(Error::Config("truncated ELF header".into()));
    }
    let machine = u16_at(bytes, 18, little)?;
    let phoff = if is64 {
        u64_at(bytes, 32, little)?
    } else {
        u32_at(bytes, 28, little)? as u64
    };
    let phentsize = u16_at(bytes, if is64 { 54 } else { 42 }, little)? as u64;
    let phnum = u16_at(bytes, if is64 { 56 } else { 44 }, little)? as u64;
    let entry = if is64 {
        u64_at(bytes, 24, little)?
    } else {
        u32_at(bytes, 24, little)? as u64
    };
    if phentsize == 0 || phnum > 4096 {
        return Err(Error::Config("bad ELF program header table".into()));
    }
    let mut segments = Vec::new();
    for i in 0..phnum {
        let at = phoff
            .checked_add(i.saturating_mul(phentsize))
            .ok_or_else(|| Error::Config("ELF header overflow".into()))? as usize;
        if at.saturating_add(phentsize as usize) > bytes.len() {
            return Err(Error::Config("truncated ELF program header".into()));
        }
        let (kind, flags, offset, vaddr, filesz, memsz) = if is64 {
            (
                u32_at(bytes, at, little)?,
                u32_at(bytes, at + 4, little)?,
                u64_at(bytes, at + 8, little)?,
                u64_at(bytes, at + 16, little)?,
                u64_at(bytes, at + 32, little)?,
                u64_at(bytes, at + 40, little)?,
            )
        } else {
            (
                u32_at(bytes, at, little)?,
                u32_at(bytes, at + 24, little)?,
                u32_at(bytes, at + 4, little)? as u64,
                u32_at(bytes, at + 8, little)? as u64,
                u32_at(bytes, at + 16, little)? as u64,
                u32_at(bytes, at + 20, little)? as u64,
            )
        };
        if filesz > memsz || offset.saturating_add(filesz) > bytes.len() as u64 {
            return Err(Error::Config("ELF segment exceeds the image".into()));
        }
        segments.push(Segment {
            kind,
            flags,
            offset,
            vaddr,
            filesz,
            memsz,
        });
    }
    Ok(Elf {
        class,
        endian: bytes[5],
        machine,
        entry,
        segments,
    })
}

pub fn guest_filter() -> Vec<SockFilter> {
    let load_arch = SockFilter {
        code: 0x20,
        jt: 0,
        jf: 0,
        k: 4,
    };
    let load_nr = SockFilter {
        code: 0x20,
        jt: 0,
        jf: 0,
        k: 0,
    };
    let allow = |nr: u32| {
        vec![
            SockFilter {
                code: 0x15,
                jt: 0,
                jf: 1,
                k: nr,
            },
            SockFilter {
                code: 0x06,
                jt: 0,
                jf: 0,
                k: SECCOMP_RET_ALLOW,
            },
        ]
    };
    let mut out = vec![load_arch, load_nr];
    for nr in [
        0u32, 1, 60, 231, 202, 35, 56, 57, 158, 14, 15, 228, 96, 62, 63, 334, 309,
    ] {
        out.extend(allow(nr));
    }
    out.push(SockFilter {
        code: 0x06,
        jt: 0,
        jf: 0,
        k: SECCOMP_RET_KILL_PROCESS,
    });
    out
}

pub fn backstop_filter() -> Vec<SockFilter> {
    vec![
        SockFilter {
            code: 0x20,
            jt: 0,
            jf: 0,
            k: 0,
        },
        SockFilter {
            code: 0x06,
            jt: 0,
            jf: 0,
            k: SECCOMP_RET_TRAP | SECCOMP_TRAP_TAG,
        },
    ]
}

#[cfg(target_os = "linux")]
pub fn install(_filters: &[SockFilter]) -> Result<()> {
    Err(Error::UnsupportedPlatform(
        "native seccomp install is gated behind the host-op backend".into(),
    ))
}

#[derive(Debug, Clone, Default)]
pub struct GuestState {
    pub pid: u32,
    pub brk: u64,
    pub mapped_bytes: u64,
    pub fd_table: std::collections::BTreeMap<u64, String>,
}

#[derive(Debug, Default)]
pub struct Control {
    pid: std::sync::atomic::AtomicU32,
}

impl Control {
    pub fn pid(&self) -> u32 {
        self.pid.load(std::sync::atomic::Ordering::SeqCst)
    }

    pub fn set_pid(&self, pid: u32) {
        self.pid.store(pid, std::sync::atomic::Ordering::SeqCst);
    }

    pub fn signal(&self, sig: i32) -> Result<()> {
        let _ = crate::run::linux_signal(sig)?;
        Ok(())
    }
}

#[derive(Debug, Clone, Default)]
pub struct SyscallTable {
    pub handled: u64,
}

impl SyscallTable {
    pub fn dispatch(&mut self, nr: u64, args: [u64; 6], state: &mut GuestState) -> i64 {
        self.handled = self.handled.saturating_add(1);
        match nr {
            0 | 1 => args[2] as i64,
            2 | 3 => {
                state.fd_table.remove(&args[0]);
                0
            }
            9 => {
                state.mapped_bytes = state.mapped_bytes.saturating_add(args[1]);
                -12
            }
            10 => 0,
            12 => {
                state.brk = args[0];
                state.brk as i64
            }
            39 | 60 | 231 => args[0] as i64,
            _ => -38,
        }
    }
}

#[derive(Debug, Clone)]
pub struct RunPlan {
    pub program: String,
    pub host: &'static str,
    pub arch: &'static str,
    pub elf_class: u8,
    pub elf_machine: u16,
    pub elf_entry: u64,
    pub segments: usize,
    pub syscall_sites: usize,
    pub patched_sites: usize,
    pub guest_filter_bytes: usize,
    pub backstop_filter_bytes: usize,
    pub channel_bytes: usize,
    pub memory_cap: u64,
    pub namespace_root: String,
    pub published_ports: usize,
    pub exposed_ports: usize,
    pub volume_mounts: usize,
    pub labels: usize,
    pub egress_denied: Vec<String>,
    pub invariants: Vec<String>,
    pub security_checks: Vec<String>,
    pub wx_segments: usize,
    pub stub_window_base: u64,
    pub stub_window_slots: u64,
}

impl serde::Serialize for RunPlan {
    fn serialize<S: serde::Serializer>(&self, s: S) -> std::result::Result<S::Ok, S::Error> {
        use serde::ser::SerializeStruct;
        let mut st = s.serialize_struct("RunPlan", 24)?;
        st.serialize_field("program", &self.program)?;
        st.serialize_field("host", &self.host)?;
        st.serialize_field("arch", &self.arch)?;
        st.serialize_field("elf_class", &self.elf_class)?;
        st.serialize_field("elf_machine", &self.elf_machine)?;
        st.serialize_field("elf_entry", &self.elf_entry)?;
        st.serialize_field("segments", &self.segments)?;
        st.serialize_field("syscall_sites", &self.syscall_sites)?;
        st.serialize_field("patched_sites", &self.patched_sites)?;
        st.serialize_field("guest_filter_bytes", &self.guest_filter_bytes)?;
        st.serialize_field("backstop_filter_bytes", &self.backstop_filter_bytes)?;
        st.serialize_field("channel_bytes", &self.channel_bytes)?;
        st.serialize_field("memory_cap", &self.memory_cap)?;
        st.serialize_field("namespace_root", &self.namespace_root)?;
        st.serialize_field("published_ports", &self.published_ports)?;
        st.serialize_field("exposed_ports", &self.exposed_ports)?;
        st.serialize_field("volume_mounts", &self.volume_mounts)?;
        st.serialize_field("labels", &self.labels)?;
        st.serialize_field("egress_denied", &self.egress_denied)?;
        st.serialize_field("invariants", &self.invariants)?;
        st.serialize_field("security_checks", &self.security_checks)?;
        st.serialize_field("wx_segments", &self.wx_segments)?;
        st.serialize_field("stub_window_base", &self.stub_window_base)?;
        st.serialize_field("stub_window_slots", &self.stub_window_slots)?;
        st.end()
    }
}

pub fn prepare(_ctx: &Context, program: &str, cfg: &Config) -> Result<RunPlan> {
    let bytes = std::fs::read(program)?;
    let elf = parse_elf(&bytes)?;
    let arch = if elf.machine == 183 {
        Arch::Arm64
    } else {
        Arch::Amd64
    };
    let instance_index = program
        .bytes()
        .fold(0u32, |acc, b| acc.wrapping_mul(31).wrapping_add(b as u32))
        % 64;
    let window = zig::StubWindow::for_instance(instance_index);
    let mut site_count = 0usize;
    let mut patched = 0usize;
    let mut mapped_bytes = 0u64;
    let mut wx_segments = 0usize;
    let mut security_checks = Vec::new();
    for segment in &elf.segments {
        let prot = ((segment.flags & 0x1) * 4) | (segment.flags & 0x2);
        security::enforce_wx(prot, cfg)?;
        if prot & 0x6 == 0x6 {
            wx_segments = wx_segments.saturating_add(1);
        }
        mapped_bytes = mapped_bytes.saturating_add(segment.memsz);
        if segment.flags & 1 == 0 || segment.filesz == 0 {
            continue;
        }
        let start = segment.offset as usize;
        let end = start
            .saturating_add(segment.filesz as usize)
            .min(bytes.len());
        if start >= end {
            continue;
        }
        let mut code = bytes[start..end].to_vec();
        let sites = rewrite::classify(arch, &code);
        patched = patched.saturating_add(rewrite::patch(arch, &mut code, &sites, window.base)?);
        site_count = site_count.saturating_add(sites.len());
    }
    let guest = guest_filter();
    let backstop = backstop_filter();
    security::enforce_memory_cap(mapped_bytes, cfg)?;
    security_checks.push("WriteXorExecute".to_string());
    security_checks.push("MemoryCap".to_string());
    zig::validate_abi()?;
    let namespace = vfs::Namespace::new(Path::new(cfg.root.as_deref().unwrap_or("/")))?;
    for mount in &cfg.mounts {
        security::enforce_mount(&namespace, &mount.guest)?;
    }
    for volume in &cfg.volumes {
        security::enforce_mount(&namespace, &volume.container_path)?;
    }
    security_checks.push("MountConfinement".to_string());
    let _ = namespace.read_dir("/")?;
    let egress = net::EgressPolicy::from_config(cfg);
    let metadata: std::net::SocketAddr = "169.254.169.254:80"
        .parse()
        .unwrap_or_else(|_| std::net::SocketAddr::from(([169, 254, 169, 254], 80)));
    security_checks.push("EgressConfinement".to_string());
    let mut egress_denied = Vec::new();
    for name in ["example.org"] {
        if let Err(e) = egress.resolve(name) {
            egress_denied.push(format!("{name}: {e}"));
        }
    }
    let metadata_err = security::enforce_egress(&egress, &metadata, false)
        .err()
        .map(|e| e.to_string())
        .unwrap_or_default();
    if !metadata_err.is_empty() {
        egress_denied.push(format!("169.254.169.254: {metadata_err}"));
    }
    security::backstop_verdict(cfg.no_rewrite)?;
    security_checks.push("SyscallBackstop".to_string());
    let mut state = GuestState::default();
    let mut table = SyscallTable::default();
    let _ = table.dispatch(12, [0, 0, 0, 0, 0, 0], &mut state);
    let root = namespace
        .resolve("/")
        .map(|p| p.host.to_string_lossy().into_owned())
        .unwrap_or_else(|_| "/".to_string());
    Ok(RunPlan {
        program: program.to_string(),
        host: HostOs::current().as_str(),
        arch: arch.as_str(),
        elf_class: elf.class,
        elf_machine: elf.machine,
        elf_entry: elf.entry,
        segments: elf.segments.len(),
        syscall_sites: site_count,
        patched_sites: patched,
        guest_filter_bytes: guest.len() * 8,
        backstop_filter_bytes: backstop.len() * 8,
        channel_bytes: zig::CHAN_SIZE,
        memory_cap: cfg.max_bytes,
        namespace_root: root,
        published_ports: cfg.published_ports.len(),
        exposed_ports: cfg.exposed_ports.len(),
        volume_mounts: cfg.volumes.len(),
        labels: cfg.labels.len(),
        egress_denied,
        invariants: security::audit().iter().map(|s| (*s).to_string()).collect(),
        security_checks,
        wx_segments,
        stub_window_base: window.base,
        stub_window_slots: window.slots,
    })
}

pub fn run(ctx: &Context, program: &str, cfg: &Config) -> Result<i32> {
    let plan = prepare(ctx, program, cfg)?;
    security::backstop_verdict(cfg.no_rewrite)?;
    match HostOs::current() {
        HostOs::Linux => Err(Error::UnsupportedPlatform(format!(
            "native guest launch needs the host-op backend; prepared {} with {} syscall sites",
            plan.program, plan.syscall_sites
        ))),
        other => Err(Error::UnsupportedPlatform(format!(
            "{} guest execution requires the preserved Zig stub backend; prepared {} with {} syscall sites",
            other.as_str(),
            plan.program,
            plan.syscall_sites
        ))),
    }
}

pub mod elf {
    pub use super::{parse_elf, Elf, Segment};
}

pub mod seccomp {
    pub use super::{
        backstop_filter, guest_filter, SockFilter, SECCOMP_RET_ALLOW, SECCOMP_RET_KILL_PROCESS,
        SECCOMP_RET_TRAP, SECCOMP_TRAP_TAG,
    };
}

pub mod syscall {
    pub use super::{GuestState, SyscallTable};
}

pub mod guest {
    pub use super::{Control, GuestState};
}

pub mod control {
    pub use super::Control;
}

pub mod run {
    pub use super::run;
}
