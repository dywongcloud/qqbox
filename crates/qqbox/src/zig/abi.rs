use crate::error::{Error, Result};
use crate::types::{Arch, HostOs};

pub const CHAN_SIZE: usize = 128;
pub const CHAN_ALIGN: usize = 8;
pub const ARGS_LEN: usize = 6;

pub const OFF_SELF: usize = 0;
pub const OFF_STATE: usize = 8;
pub const OFF_ALIVE: usize = 12;
pub const OFF_KIND: usize = 16;
pub const OFF_NR: usize = 24;
pub const OFF_ARGS: usize = 32;
pub const OFF_RIP: usize = 80;
pub const OFF_RET: usize = 88;
pub const OFF_STUB_SLEEP: usize = 96;
pub const OFF_SUP_SLEEP: usize = 100;
pub const OFF_GUEST_RIP: usize = 104;
pub const OFF_STDIO: usize = 112;
pub const OFF_PINFO: usize = 120;

pub const SCRATCH_OFF: usize = 128;
pub const SLOT_SIZE: usize = 4096;
pub const MAX_SLOTS: usize = 256;
pub const SLOTS_LEN: usize = SLOT_SIZE * MAX_SLOTS;
pub const SCRATCH_LEN: usize = SLOT_SIZE - SCRATCH_OFF;
pub const STUB_BASE: u64 = 0x6f00_0000_0000;
pub const WINDOW_ALIGN: u64 = 0x0020_0000_0000;
pub const WINDOW_SLOTS_OFF: u64 = 0x0010_0000_0000;
pub const WINDOW_HOP_REQ_OFF: u64 = 0x0016_0000_0000;
pub const WINDOW_HOP_REP_OFF: u64 = 0x0016_0010_0000;

// The Go backend keeps stubLo/slotsAddr/stubHi in package globals set once by
// adoptWindow. That makes a second instance in the same supervisor collide with
// the first. The Rust control plane derives a window per instance instead, while
// keeping the two-page-table design that the backstops depend on.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct StubWindow {
    pub base: u64,
    pub slots: u64,
    pub hi: u64,
    pub hop_req: u64,
    pub hop_rep: u64,
}

impl StubWindow {
    pub fn primary() -> Self {
        StubWindow::for_instance(0)
    }

    pub fn for_instance(index: u32) -> Self {
        let base = STUB_BASE.wrapping_sub((index as u64).saturating_mul(WINDOW_ALIGN));
        StubWindow {
            base,
            slots: base.wrapping_add(WINDOW_SLOTS_OFF),
            hi: base.wrapping_add(WINDOW_ALIGN),
            hop_req: base.wrapping_add(WINDOW_HOP_REQ_OFF),
            hop_rep: base.wrapping_add(WINDOW_HOP_REP_OFF),
        }
    }
}

pub const ST_REQ: u32 = 1;
pub const ST_REP: u32 = 2;
pub const ST_DEAD: u32 = 3;
pub const LAYOUT_NORAND: u32 = 1;
pub const SUP_SPIN: usize = 20000;
pub const STUB_SPIN: usize = 4000;

pub const MSG_READY: u64 = 1;
pub const MSG_SYSCALL: u64 = 2;
pub const MSG_HOSTRET: u64 = 3;
pub const MSG_FORKED: u64 = 4;
pub const MSG_TRACE_LOCAL: u64 = 5;
pub const MSG_THREAD_ID: u64 = 6;

pub const REP_RESULT: u64 = 1;
pub const REP_HOST: u64 = 2;
pub const REP_START: u64 = 3;
pub const REP_CLONE: u64 = 4;
pub const REP_EXEC: u64 = 5;
pub const REP_FORK: u64 = 6;
pub const REP_PEEK: u64 = 7;
pub const REP_POKE: u64 = 8;
pub const REP_REWRITE: u64 = 10;
pub const REP_THREAD_END: u64 = 11;
pub const REP_FAULT_LINUX: u64 = 12;
pub const REP_MAPS_DARWIN: u64 = 12;
pub const REP_EXEC_PREPARE_LINUX: u64 = 13;
pub const REP_ADOPT_WINDOWS: u64 = 13;
pub const REP_SIGRETURN_LINUX: u64 = 14;
pub const REP_WRITABLE_WINDOWS: u64 = 15;
pub const REP_FDSYNC: u64 = 20;
pub const REP_FORKCHILD: u64 = 21;
pub const REP_SMAPS: u64 = 22;

// `self` is a Rust keyword; self_addr is byte 0 of the shared page.
#[repr(C)]
#[derive(Debug, Clone, Copy, Default, PartialEq, Eq)]
pub struct ChannelRequest {
    pub self_addr: u64,
    pub state: u32,
    pub alive: u32,
    pub kind: u64,
    pub nr: u64,
    pub args: [u64; ARGS_LEN],
    pub rip: u64,
    pub ret: u64,
    pub stub_sleep: u32,
    pub sup_sleep: u32,
    pub guest_rip: u64,
    pub stdio: u64,
    pub pinfo: u64,
}

#[repr(C)]
#[derive(Debug, Clone, Copy, Default, PartialEq, Eq)]
pub struct ChannelResponse {
    pub self_addr: u64,
    pub state: u32,
    pub alive: u32,
    pub kind: u64,
    pub nr: u64,
    pub args: [u64; ARGS_LEN],
    pub rip: u64,
    pub ret: u64,
    pub stub_sleep: u32,
    pub sup_sleep: u32,
    pub guest_rip: u64,
    pub stdio: u64,
    pub pinfo: u64,
}

const _: () = {
    assert!(std::mem::size_of::<ChannelRequest>() == CHAN_SIZE);
    assert!(std::mem::align_of::<ChannelRequest>() == CHAN_ALIGN);
    assert!(std::mem::offset_of!(ChannelRequest, self_addr) == OFF_SELF);
    assert!(std::mem::offset_of!(ChannelRequest, state) == OFF_STATE);
    assert!(std::mem::offset_of!(ChannelRequest, alive) == OFF_ALIVE);
    assert!(std::mem::offset_of!(ChannelRequest, kind) == OFF_KIND);
    assert!(std::mem::offset_of!(ChannelRequest, nr) == OFF_NR);
    assert!(std::mem::offset_of!(ChannelRequest, args) == OFF_ARGS);
    assert!(std::mem::offset_of!(ChannelRequest, rip) == OFF_RIP);
    assert!(std::mem::offset_of!(ChannelRequest, ret) == OFF_RET);
    assert!(std::mem::offset_of!(ChannelRequest, stub_sleep) == OFF_STUB_SLEEP);
    assert!(std::mem::offset_of!(ChannelRequest, sup_sleep) == OFF_SUP_SLEEP);
    assert!(std::mem::offset_of!(ChannelRequest, guest_rip) == OFF_GUEST_RIP);
    assert!(std::mem::offset_of!(ChannelRequest, stdio) == OFF_STDIO);
    assert!(std::mem::offset_of!(ChannelRequest, pinfo) == OFF_PINFO);
};

const _: () = {
    assert!(std::mem::size_of::<ChannelResponse>() == CHAN_SIZE);
    assert!(std::mem::align_of::<ChannelResponse>() == CHAN_ALIGN);
    assert!(std::mem::offset_of!(ChannelResponse, self_addr) == OFF_SELF);
    assert!(std::mem::offset_of!(ChannelResponse, state) == OFF_STATE);
    assert!(std::mem::offset_of!(ChannelResponse, alive) == OFF_ALIVE);
    assert!(std::mem::offset_of!(ChannelResponse, kind) == OFF_KIND);
    assert!(std::mem::offset_of!(ChannelResponse, nr) == OFF_NR);
    assert!(std::mem::offset_of!(ChannelResponse, args) == OFF_ARGS);
    assert!(std::mem::offset_of!(ChannelResponse, rip) == OFF_RIP);
    assert!(std::mem::offset_of!(ChannelResponse, ret) == OFF_RET);
    assert!(std::mem::offset_of!(ChannelResponse, stub_sleep) == OFF_STUB_SLEEP);
    assert!(std::mem::offset_of!(ChannelResponse, sup_sleep) == OFF_SUP_SLEEP);
    assert!(std::mem::offset_of!(ChannelResponse, guest_rip) == OFF_GUEST_RIP);
    assert!(std::mem::offset_of!(ChannelResponse, stdio) == OFF_STDIO);
    assert!(std::mem::offset_of!(ChannelResponse, pinfo) == OFF_PINFO);
};

impl ChannelRequest {
    pub fn new(slot_addr: u64) -> Self {
        Self {
            self_addr: slot_addr,
            ..Self::default()
        }
    }

    pub fn ready(slot_addr: u64) -> Self {
        Self {
            self_addr: slot_addr,
            state: ST_REQ,
            kind: MSG_READY,
            ..Self::default()
        }
    }

    pub fn syscall(
        slot_addr: u64,
        nr: u64,
        args: [u64; ARGS_LEN],
        rip: u64,
        guest_rip: u64,
    ) -> Self {
        Self {
            self_addr: slot_addr,
            state: ST_REQ,
            kind: MSG_SYSCALL,
            nr,
            args,
            rip,
            guest_rip,
            ..Self::default()
        }
    }

    pub fn is_request(&self) -> bool {
        self.state == ST_REQ
    }

    pub fn layout_norand(&self) -> bool {
        self.alive & LAYOUT_NORAND != 0
    }

    pub fn as_bytes(&self) -> &[u8] {
        unsafe { std::slice::from_raw_parts(self as *const Self as *const u8, CHAN_SIZE) }
    }

    pub fn from_bytes(bytes: &[u8]) -> Option<&Self> {
        if bytes.len() != CHAN_SIZE {
            return None;
        }
        let ptr = bytes.as_ptr();
        if !(ptr as usize).is_multiple_of(CHAN_ALIGN) {
            return None;
        }
        Some(unsafe { &*(ptr as *const Self) })
    }
}

impl ChannelResponse {
    pub fn result(slot_addr: u64, ret: i64) -> Self {
        Self {
            self_addr: slot_addr,
            state: ST_REP,
            kind: REP_RESULT,
            ret: ret as u64,
            ..Self::default()
        }
    }

    pub fn host(slot_addr: u64, nr: u64, args: [u64; ARGS_LEN]) -> Self {
        Self {
            self_addr: slot_addr,
            state: ST_REP,
            kind: REP_HOST,
            nr,
            args,
            ..Self::default()
        }
    }

    pub fn dead(slot_addr: u64) -> Self {
        Self {
            self_addr: slot_addr,
            state: ST_DEAD,
            ..Self::default()
        }
    }

    pub fn is_reply(&self) -> bool {
        self.state == ST_REP
    }

    pub fn result_value(&self) -> i64 {
        self.ret as i64
    }

    pub fn as_bytes(&self) -> &[u8] {
        unsafe { std::slice::from_raw_parts(self as *const Self as *const u8, CHAN_SIZE) }
    }

    pub fn from_bytes(bytes: &[u8]) -> Option<&Self> {
        if bytes.len() != CHAN_SIZE {
            return None;
        }
        let ptr = bytes.as_ptr();
        if !(ptr as usize).is_multiple_of(CHAN_ALIGN) {
            return None;
        }
        Some(unsafe { &*(ptr as *const Self) })
    }
}

impl From<&ChannelRequest> for ChannelResponse {
    fn from(req: &ChannelRequest) -> Self {
        Self {
            self_addr: req.self_addr,
            state: req.state,
            alive: req.alive,
            kind: req.kind,
            nr: req.nr,
            args: req.args,
            rip: req.rip,
            ret: req.ret,
            stub_sleep: req.stub_sleep,
            sup_sleep: req.sup_sleep,
            guest_rip: req.guest_rip,
            stdio: req.stdio,
            pinfo: req.pinfo,
        }
    }
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct ChannelOffsets {
    pub self_addr: usize,
    pub state: usize,
    pub alive: usize,
    pub kind: usize,
    pub nr: usize,
    pub args: usize,
    pub rip: usize,
    pub ret: usize,
    pub stub_sleep: usize,
    pub sup_sleep: usize,
    pub guest_rip: usize,
    pub stdio: usize,
    pub pinfo: usize,
}

pub const EXPECTED_OFFSETS: ChannelOffsets = ChannelOffsets {
    self_addr: OFF_SELF,
    state: OFF_STATE,
    alive: OFF_ALIVE,
    kind: OFF_KIND,
    nr: OFF_NR,
    args: OFF_ARGS,
    rip: OFF_RIP,
    ret: OFF_RET,
    stub_sleep: OFF_STUB_SLEEP,
    sup_sleep: OFF_SUP_SLEEP,
    guest_rip: OFF_GUEST_RIP,
    stdio: OFF_STDIO,
    pinfo: OFF_PINFO,
};

impl ChannelOffsets {
    pub fn measured() -> Self {
        Self {
            self_addr: std::mem::offset_of!(ChannelRequest, self_addr),
            state: std::mem::offset_of!(ChannelRequest, state),
            alive: std::mem::offset_of!(ChannelRequest, alive),
            kind: std::mem::offset_of!(ChannelRequest, kind),
            nr: std::mem::offset_of!(ChannelRequest, nr),
            args: std::mem::offset_of!(ChannelRequest, args),
            rip: std::mem::offset_of!(ChannelRequest, rip),
            ret: std::mem::offset_of!(ChannelRequest, ret),
            stub_sleep: std::mem::offset_of!(ChannelRequest, stub_sleep),
            sup_sleep: std::mem::offset_of!(ChannelRequest, sup_sleep),
            guest_rip: std::mem::offset_of!(ChannelRequest, guest_rip),
            stdio: std::mem::offset_of!(ChannelRequest, stdio),
            pinfo: std::mem::offset_of!(ChannelRequest, pinfo),
        }
    }

    pub fn measured_response() -> Self {
        Self {
            self_addr: std::mem::offset_of!(ChannelResponse, self_addr),
            state: std::mem::offset_of!(ChannelResponse, state),
            alive: std::mem::offset_of!(ChannelResponse, alive),
            kind: std::mem::offset_of!(ChannelResponse, kind),
            nr: std::mem::offset_of!(ChannelResponse, nr),
            args: std::mem::offset_of!(ChannelResponse, args),
            rip: std::mem::offset_of!(ChannelResponse, rip),
            ret: std::mem::offset_of!(ChannelResponse, ret),
            stub_sleep: std::mem::offset_of!(ChannelResponse, stub_sleep),
            sup_sleep: std::mem::offset_of!(ChannelResponse, sup_sleep),
            guest_rip: std::mem::offset_of!(ChannelResponse, guest_rip),
            stdio: std::mem::offset_of!(ChannelResponse, stdio),
            pinfo: std::mem::offset_of!(ChannelResponse, pinfo),
        }
    }

    pub fn mismatches(&self) -> Vec<String> {
        let want = EXPECTED_OFFSETS;
        let pairs = [
            ("self", self.self_addr, want.self_addr),
            ("state", self.state, want.state),
            ("alive", self.alive, want.alive),
            ("kind", self.kind, want.kind),
            ("nr", self.nr, want.nr),
            ("args", self.args, want.args),
            ("rip", self.rip, want.rip),
            ("ret", self.ret, want.ret),
            ("stub_sleep", self.stub_sleep, want.stub_sleep),
            ("sup_sleep", self.sup_sleep, want.sup_sleep),
            ("guest_rip", self.guest_rip, want.guest_rip),
            ("stdio", self.stdio, want.stdio),
            ("pinfo", self.pinfo, want.pinfo),
        ];
        let mut found = Vec::new();
        for (name, actual, expected) in pairs {
            if actual != expected {
                found.push(format!("{} offset {} != {}", name, actual, expected));
            }
        }
        found
    }
}

const LINUX_AMD64_STUB: &[u8] = include_bytes!("../../../../stub/stub-amd64.bin");
const LINUX_ARM64_STUB: &[u8] = include_bytes!("../../../../stub/stub-arm64.bin");
const LINUX_AMD64_PIE_STUB: &[u8] = include_bytes!("../../../../stub/stub-amd64-pie.bin");
const LINUX_ARM64_PIE_STUB: &[u8] = include_bytes!("../../../../stub/stub-arm64-pie.bin");
const MACOS_ARM64_STUB: &[u8] = include_bytes!("../../../../stub/macos/stub.bin");
const MACOS_HVF_STUB: &[u8] = include_bytes!("../../../../stub/macos/hvf_stub.bin");
const WINDOWS_AMD64_STUB: &[u8] = include_bytes!("../../../../stub/windows/stub-amd64.bin");
const WINDOWS_ARM64_STUB: &[u8] = include_bytes!("../../../../stub/windows/stub-arm64.bin");
const NO_STUB: &[u8] = &[];

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct StubImage {
    pub arch: Arch,
    pub host: HostOs,
    pub path: &'static str,
    pub bytes: &'static [u8],
    pub embedded: bool,
}

// The macOS stub is arm64 only; no amd64 blob is committed for Darwin.
pub const STUB_IMAGES: &[StubImage] = &[
    StubImage {
        arch: Arch::Amd64,
        host: HostOs::Linux,
        path: "stub/stub-amd64.bin",
        bytes: LINUX_AMD64_STUB,
        embedded: true,
    },
    StubImage {
        arch: Arch::Arm64,
        host: HostOs::Linux,
        path: "stub/stub-arm64.bin",
        bytes: LINUX_ARM64_STUB,
        embedded: true,
    },
    StubImage {
        arch: Arch::Amd64,
        host: HostOs::Darwin,
        path: "stub/macos/stub-amd64.bin",
        bytes: NO_STUB,
        embedded: false,
    },
    StubImage {
        arch: Arch::Arm64,
        host: HostOs::Darwin,
        path: "stub/macos/stub.bin",
        bytes: MACOS_ARM64_STUB,
        embedded: true,
    },
    StubImage {
        arch: Arch::Amd64,
        host: HostOs::Windows,
        path: "stub/windows/stub-amd64.bin",
        bytes: WINDOWS_AMD64_STUB,
        embedded: true,
    },
    StubImage {
        arch: Arch::Arm64,
        host: HostOs::Windows,
        path: "stub/windows/stub-arm64.bin",
        bytes: WINDOWS_ARM64_STUB,
        embedded: true,
    },
];

pub const MISSING_STUB_BLOBS: &[&str] = &["stub/macos/stub-amd64.bin"];

pub fn stub_image(arch: Arch, host: HostOs) -> Option<&'static StubImage> {
    STUB_IMAGES
        .iter()
        .find(|image| image.arch == arch && image.host == host)
}

pub fn stub_bytes(arch: Arch, host: HostOs) -> &'static [u8] {
    match stub_image(arch, host) {
        Some(image) => image.bytes,
        None => NO_STUB,
    }
}

pub fn nested_stub_bytes(arch: Arch) -> &'static [u8] {
    match arch {
        Arch::Amd64 => LINUX_AMD64_PIE_STUB,
        Arch::Arm64 => LINUX_ARM64_PIE_STUB,
    }
}

pub fn hvf_stub_bytes() -> &'static [u8] {
    MACOS_HVF_STUB
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct AbiLayout {
    pub size: usize,
    pub align: usize,
    pub response_size: usize,
    pub response_align: usize,
    pub offsets: ChannelOffsets,
    pub response_offsets: ChannelOffsets,
    pub stub_base: u64,
    pub slot_size: usize,
    pub max_slots: usize,
    pub slots_len: usize,
    pub scratch_off: usize,
    pub scratch_len: usize,
    pub stubs: &'static [StubImage],
    pub missing_blobs: &'static [&'static str],
}

impl AbiLayout {
    pub fn missing_blob_paths(&self) -> Vec<&'static str> {
        self.stubs
            .iter()
            .filter(|image| !image.embedded)
            .map(|image| image.path)
            .collect()
    }

    pub fn supports(&self, arch: Arch, host: HostOs) -> bool {
        stub_image(arch, host).is_some_and(|image| image.embedded)
    }
}

impl std::fmt::Display for AbiLayout {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        write!(
            f,
            "chanPage size={} align={} scratch_off={} slot_size={} max_slots={} stub_base={:#x} missing_stub_blobs={:?}",
            self.size,
            self.align,
            self.scratch_off,
            self.slot_size,
            self.max_slots,
            self.stub_base,
            self.missing_blobs
        )
    }
}

pub fn abi_layout() -> AbiLayout {
    AbiLayout {
        size: std::mem::size_of::<ChannelRequest>(),
        align: std::mem::align_of::<ChannelRequest>(),
        response_size: std::mem::size_of::<ChannelResponse>(),
        response_align: std::mem::align_of::<ChannelResponse>(),
        offsets: ChannelOffsets::measured(),
        response_offsets: ChannelOffsets::measured_response(),
        stub_base: STUB_BASE,
        slot_size: SLOT_SIZE,
        max_slots: MAX_SLOTS,
        slots_len: SLOTS_LEN,
        scratch_off: SCRATCH_OFF,
        scratch_len: SCRATCH_LEN,
        stubs: STUB_IMAGES,
        missing_blobs: MISSING_STUB_BLOBS,
    }
}

pub fn validate_abi() -> Result<()> {
    let layout = abi_layout();
    let mut problems = Vec::new();

    if layout.size != CHAN_SIZE {
        problems.push(format!("request size {} != {}", layout.size, CHAN_SIZE));
    }
    if layout.align != CHAN_ALIGN {
        problems.push(format!("request align {} != {}", layout.align, CHAN_ALIGN));
    }
    if layout.response_size != CHAN_SIZE {
        problems.push(format!(
            "response size {} != {}",
            layout.response_size, CHAN_SIZE
        ));
    }
    if layout.response_align != CHAN_ALIGN {
        problems.push(format!(
            "response align {} != {}",
            layout.response_align, CHAN_ALIGN
        ));
    }
    if layout.size > layout.scratch_off {
        problems.push("channel header overlaps the slot scratch".to_owned());
    }
    if !layout.slot_size.is_multiple_of(layout.align) {
        problems.push("slot size is not a multiple of the channel alignment".to_owned());
    }
    match layout.slot_size.checked_sub(layout.scratch_off) {
        Some(len) if len == layout.scratch_len => {}
        _ => problems.push("scratch length does not match the slot size".to_owned()),
    }
    if layout.slots_len != layout.slot_size.saturating_mul(layout.max_slots) {
        problems.push("slots region length does not match the slot count".to_owned());
    }
    problems.extend(layout.offsets.mismatches());
    for problem in layout.response_offsets.mismatches() {
        problems.push(format!("response {}", problem));
    }
    for image in layout.stubs {
        if image.embedded == image.bytes.is_empty() {
            problems.push(format!(
                "stub {} embedded flag disagrees with its bytes",
                image.path
            ));
        }
    }
    for path in layout.missing_blobs {
        match layout.stubs.iter().find(|image| image.path == *path) {
            Some(image) if !image.embedded && image.bytes.is_empty() => {}
            _ => problems.push(format!("missing blob {} is not recorded as absent", path)),
        }
    }
    if problems.is_empty() {
        Ok(())
    } else {
        Err(Error::Config(format!(
            "zig channel ABI mismatch: {}",
            problems.join("; ")
        )))
    }
}

// Declarations only: the stub images are data blobs, so these symbols are never
// referenced by the Rust crate and introduce no link dependency on any host.
#[cfg(all(target_os = "linux", target_arch = "x86_64"))]
#[allow(dead_code)]
extern "C" {
    fn stub_main(sp: u64);
    fn lc_syscall_entry();
    fn lc_syscall_entry_gs();
    fn lc_latekick();
    fn lc_thread_start(slot_addr: u64);
    fn lc_tramp_syscall(frame: *mut u8, fx: *const u8);
    fn lc_tramp_syscall_gs(frame: *mut u8);
    fn lc_tramp_latekick(frame: *mut u8);
    fn lc_exec_on_stack(ch: *mut ChannelRequest);
    fn lc_fork_child(cs: u64, req: u64, ctx: u64, stack: u64, cap: u64);
    fn lc_hop_forked(cs: u64, req: u64, ctx: u64, stack: u64, cap: u64);
    fn lc_restorer_c(sp: u64);
    fn lc_guest_restorer();
    fn lc_sig_trap();
    fn stub_restorer();
    fn lc_trace_child();
}

#[cfg(all(target_os = "linux", target_arch = "aarch64"))]
#[allow(dead_code)]
extern "C" {
    fn stub_main(sp: u64);
    fn lc_syscall_entry();
    fn lc_latekick();
    fn lc_thread_start(slot_addr: u64);
    fn lc_tramp_syscall(frame: *mut u8);
    fn lc_tramp_latekick(frame: *mut u8);
    fn lc_exec_on_stack(ch: *mut ChannelRequest);
    fn lc_fork_child(cs: u64, req: u64, ctx: u64, stack: u64, cap: u64);
    fn lc_hop_forked(cs: u64, req: u64, ctx: u64, stack: u64, cap: u64);
    fn lc_restorer_c(sp: u64);
    fn lc_guest_restorer();
    fn lc_sig_trap();
    fn stub_restorer();
    fn lc_trace_child();
}

#[cfg(target_os = "macos")]
#[allow(dead_code)]
extern "C" {
    fn tramp_handler(frame: *mut u64) -> u64;
    fn tramp_common();
    fn lc_sig_switch();
    fn lc_trap_ret();
}

#[cfg(all(target_os = "windows", target_arch = "x86_64"))]
#[allow(dead_code)]
extern "C" {
    fn wWinMainCRTStartup();
    fn lc_tramp_syscall(frame: *mut u8, fx: *const u8);
    fn lc_exit_thread(code: u64);
    fn lc_sig_redirect(saved: *mut u8);
    fn lc_sig_apc(a: usize, b: usize, c: usize);
    fn lc_return_prepare() -> *mut u8;
    fn lc_instr_stray();
}

#[cfg(all(target_os = "windows", target_arch = "aarch64"))]
#[allow(dead_code)]
extern "C" {
    fn wWinMainCRTStartup();
    fn lc_tramp_syscall(frame: *mut u8);
    fn lc_exit_thread(code: u64);
    fn lc_sig_redirect_c(saved: *mut u8);
    fn lc_sig_apc(a: usize, b: usize, c: usize);
    fn lc_return_prepare() -> *mut u8;
    fn lc_instr_stray();
}
