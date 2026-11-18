use std::path::{Path, PathBuf};
use std::sync::atomic::{AtomicBool, AtomicI32, AtomicU32, Ordering};
use std::sync::{Arc, Condvar, Mutex, MutexGuard};
use std::thread::{self, JoinHandle};

use serde_json::Value;

use crate::config::Config;
use crate::error::{Error, Result};
use crate::oci::{Image, PullOptions};
use crate::options::Options;
use crate::types::{Arch, Context, UserId};

const DEFAULT_MAX_IMAGE_SIZE: u64 = 16 * 1024 * 1024 * 1024;

static LINUX_SIGNALS: &[(i32, &str)] = &[
    (1, "SIGHUP"),
    (2, "SIGINT"),
    (3, "SIGQUIT"),
    (6, "SIGABRT"),
    (9, "SIGKILL"),
    (10, "SIGUSR1"),
    (11, "SIGSEGV"),
    (12, "SIGUSR2"),
    (13, "SIGPIPE"),
    (14, "SIGALRM"),
    (15, "SIGTERM"),
    (17, "SIGCHLD"),
    (18, "SIGCONT"),
    (19, "SIGSTOP"),
    (20, "SIGTSTP"),
    (28, "SIGWINCH"),
];

#[cfg(target_os = "linux")]
static HOST_SIGNALS: &[(i32, i32)] = &[
    (1, 1),
    (2, 2),
    (3, 3),
    (6, 6),
    (9, 9),
    (10, 10),
    (11, 11),
    (12, 12),
    (13, 13),
    (14, 14),
    (15, 15),
    (17, 17),
    (18, 18),
    (19, 19),
    (20, 20),
    (28, 28),
];

#[cfg(target_os = "macos")]
static HOST_SIGNALS: &[(i32, i32)] = &[
    (1, 1),
    (2, 2),
    (3, 3),
    (6, 6),
    (9, 9),
    (10, 30),
    (11, 11),
    (12, 31),
    (13, 13),
    (14, 14),
    (15, 15),
    (17, 20),
    (18, 19),
    (19, 17),
    (20, 18),
    (28, 28),
];

#[cfg(not(any(target_os = "linux", target_os = "macos")))]
static HOST_SIGNALS: &[(i32, i32)] = &[(2, 2), (6, 6), (9, 9), (15, 15)];

static FATAL_SIGNALS: &[i32] = &[1, 2, 3, 6, 9, 10, 11, 12, 13, 14, 15];

pub fn run(ctx: &Context, program: &str, opts: &Options) -> Result<i32> {
    let (prog, cfg) = program_config(program, opts)?;
    crate::sandbox::run::run(ctx, &prog, &cfg)
}

pub fn run_image(ctx: &Context, reference: &str, opts: &Options) -> Result<i32> {
    let (prog, cfg) = image_run(reference, opts)?;
    crate::sandbox::run::run(ctx, &prog, &cfg)
}

pub fn start(ctx: &Context, program: &str, opts: &Options) -> Result<Process> {
    let (prog, cfg) = program_config(program, opts)?;
    spawn(ctx, prog, cfg)
}

pub fn start_image(ctx: &Context, reference: &str, opts: &Options) -> Result<Process> {
    let (prog, cfg) = image_run(reference, opts)?;
    spawn(ctx, prog, cfg)
}

pub fn host_arch() -> Arch {
    if cfg!(target_arch = "aarch64") {
        Arch::Arm64
    } else {
        Arch::Amd64
    }
}

pub fn program_config(program: &str, opts: &Options) -> Result<(String, Config)> {
    if opts.entrypoint.is_some() {
        return Err(Error::Config(
            "WithEntrypoint applies to an image".to_owned(),
        ));
    }
    let base = Config {
        env: Config::default_env(),
        dir: "/".to_owned(),
        ..Config::default()
    };
    let mut cfg = opts.apply(&base)?;
    let mut args = vec![program.to_owned()];
    if let Some(extra) = &opts.args {
        args.extend(extra.iter().cloned());
    }
    cfg.entrypoint.clear();
    cfg.cmd.clear();
    cfg.args = args;
    if let Some(spec) = &opts.user {
        let root = cfg.root.clone().unwrap_or_else(|| "/".to_owned());
        cfg.user = resolve_user(Path::new(&root), spec)?;
        cfg.user_spec = spec.clone();
    }
    Ok((program.to_owned(), cfg))
}

pub fn image_config(img: &Image, opts: &Options) -> Result<Config> {
    reject_image_root(opts)?;
    let raw = match serde_json::to_value(&img.config) {
        Ok(value) => value,
        Err(_) => Value::Null,
    };
    let runtime = runtime_object(&raw);
    let mut base = crate::oci::config::to_run_config(&img.config);
    base.root = Some(img.root.to_string_lossy().into_owned());
    base.image = img.reference.clone();

    let entry = string_list(runtime, &["Entrypoint", "entrypoint"])
        .unwrap_or_else(|| base.entrypoint.clone());
    let cmd = string_list(runtime, &["Cmd", "cmd"]).unwrap_or_else(|| base.cmd.clone());
    let env = string_list(runtime, &["Env", "env"]).unwrap_or_else(|| base.env.clone());
    let working_dir = string_field(runtime, &["WorkingDir", "working_dir"])
        .unwrap_or_else(|| base.working_dir.clone());
    let user_spec =
        string_field(runtime, &["User", "user"]).unwrap_or_else(|| base.user_spec.clone());

    let mut image_env = env.clone();
    if !has_env(&image_env, "HOME") {
        image_env.push("HOME=/".to_owned());
    }
    base.entrypoint = entry.clone();
    base.cmd = cmd.clone();
    base.env = image_env;
    base.working_dir = working_dir.clone();
    base.user_spec = user_spec.clone();

    let mut cfg = opts.apply(&base)?;
    cfg.dir = match &opts.dir {
        Some(dir) => dir.clone(),
        None => {
            if working_dir.is_empty() {
                "/".to_owned()
            } else {
                working_dir
            }
        }
    };

    let (final_entry, final_cmd) = match (&opts.entrypoint, &opts.args) {
        (Some(overridden), args) => (overridden.clone(), args.clone().unwrap_or_default()),
        (None, Some(args)) => (entry, args.clone()),
        (None, None) => (entry, cmd),
    };
    cfg.entrypoint = final_entry.clone();
    cfg.cmd = final_cmd.clone();
    cfg.args = final_entry;
    cfg.args.extend(final_cmd);
    if cfg.args.is_empty() {
        return Err(Error::NoEntrypoint(img.reference.clone()));
    }

    let spec = match &opts.user {
        Some(overridden) => overridden.clone(),
        None => user_spec,
    };
    cfg.user_spec = spec.clone();
    cfg.user = resolve_user(&img.root, &spec)?;
    Ok(cfg)
}

fn reject_image_root(opts: &Options) -> Result<()> {
    match &opts.root {
        Some(_) => Err(Error::Config(
            "WithRoot does not apply to an image".to_owned(),
        )),
        None => Ok(()),
    }
}

fn platform_arch(platform: &str) -> Result<Arch> {
    match platform {
        "linux/amd64" => Ok(Arch::Amd64),
        "linux/arm64" => Ok(Arch::Arm64),
        other => Err(Error::UnsupportedPlatform(format!(
            "bad platform {:?}: want linux/amd64 or linux/arm64",
            other
        ))),
    }
}

fn image_run(reference: &str, opts: &Options) -> Result<(String, Config)> {
    reject_image_root(opts)?;
    let arch = match &opts.platform {
        Some(platform) => platform_arch(platform)?,
        None => host_arch(),
    };
    let pull = PullOptions {
        cache_dir: opts.image_cache_dir.as_ref().map(PathBuf::from),
        max_size: if opts.max_image_size == 0 {
            DEFAULT_MAX_IMAGE_SIZE
        } else {
            opts.max_image_size
        },
        refresh: opts.pull,
        arch,
    };
    let img = crate::oci::pull(reference, &pull)?;
    let cfg = image_config(&img, opts)?;
    let program = match cfg.args.first() {
        Some(first) => first.clone(),
        None => return Err(Error::NoEntrypoint(reference.to_owned())),
    };
    let resolved = crate::oci::lookpath::look_path(&img.root, &program, &cfg.env)?;
    Ok((resolved.to_string_lossy().into_owned(), cfg))
}

fn has_env(env: &[String], key: &str) -> bool {
    let prefix = format!("{}=", key);
    env.iter().any(|entry| entry.starts_with(&prefix))
}

fn runtime_object(value: &Value) -> &Value {
    for key in ["config", "Config"] {
        if let Some(found) = value.get(key) {
            if found.is_object() {
                return found;
            }
        }
    }
    value
}

fn string_list(value: &Value, keys: &[&str]) -> Option<Vec<String>> {
    for key in keys {
        if let Some(items) = value.get(*key).and_then(Value::as_array) {
            return Some(
                items
                    .iter()
                    .filter_map(|item| item.as_str().map(str::to_owned))
                    .collect(),
            );
        }
    }
    None
}

fn string_field(value: &Value, keys: &[&str]) -> Option<String> {
    for key in keys {
        if let Some(text) = value.get(*key).and_then(Value::as_str) {
            return Some(text.to_owned());
        }
    }
    None
}

pub fn resolve_user(root: &Path, spec: &str) -> Result<Option<UserId>> {
    let trimmed = spec.trim();
    if trimmed.is_empty() {
        return Ok(None);
    }
    let (user_part, group_part) = match trimmed.split_once(':') {
        Some((user, group)) => (user, Some(group)),
        None => (trimmed, None),
    };
    if user_part.is_empty() {
        return Err(Error::Config(format!("user {:?}: empty user", trimmed)));
    }

    let mut gid = 0u32;
    let mut gid_known = false;
    let mut name = String::new();
    let uid = match parse_id(user_part) {
        Some(id) => id,
        None => match passwd_entry(root, user_part) {
            Some((parsed_uid, primary_gid)) => {
                gid = primary_gid;
                gid_known = true;
                name = user_part.to_owned();
                parsed_uid
            }
            None => {
                return Err(Error::Config(format!(
                    "user {:?}: no such user {}",
                    trimmed, user_part
                )))
            }
        },
    };

    if let Some(group) = group_part {
        if group.is_empty() {
            return Err(Error::Config(format!("user {:?}: empty group", trimmed)));
        }
        match parse_id(group) {
            Some(id) => {
                gid = id;
                gid_known = true;
            }
            None => match group_gid(root, group) {
                Some(id) => {
                    gid = id;
                    gid_known = true;
                }
                None => {
                    return Err(Error::Config(format!(
                        "user {:?}: no such group {}",
                        trimmed, group
                    )))
                }
            },
        }
    }
    if !gid_known {
        gid = uid;
    }

    let supplementary = if name.is_empty() {
        0
    } else {
        supplementary_group_count(root, &name, gid)
    };
    if uid == 0 && gid == 0 && supplementary == 0 {
        return Ok(None);
    }
    Ok(Some(UserId { uid, gid }))
}

fn parse_id(text: &str) -> Option<u32> {
    if text.is_empty() || !text.bytes().all(|byte| byte.is_ascii_digit()) {
        return None;
    }
    text.parse::<u32>().ok()
}

fn account_lines(root: &Path, relative: &str) -> Vec<Vec<String>> {
    let text = match std::fs::read_to_string(root.join(relative)) {
        Ok(contents) => contents,
        Err(_) => return Vec::new(),
    };
    let mut lines = Vec::new();
    for line in text.lines() {
        let trimmed = line.trim();
        if trimmed.is_empty() || trimmed.starts_with('#') {
            continue;
        }
        lines.push(
            trimmed
                .split(':')
                .map(|field| field.trim().to_owned())
                .collect(),
        );
    }
    lines
}

fn passwd_entry(root: &Path, name: &str) -> Option<(u32, u32)> {
    for fields in account_lines(root, "etc/passwd") {
        if fields.first().map(String::as_str) == Some(name) {
            let uid = fields.get(2).and_then(|field| parse_id(field));
            let gid = fields.get(3).and_then(|field| parse_id(field));
            if let (Some(uid), Some(gid)) = (uid, gid) {
                return Some((uid, gid));
            }
        }
    }
    None
}

fn group_gid(root: &Path, name: &str) -> Option<u32> {
    for fields in account_lines(root, "etc/group") {
        if fields.first().map(String::as_str) == Some(name) {
            if let Some(gid) = fields.get(2).and_then(|field| parse_id(field)) {
                return Some(gid);
            }
        }
    }
    None
}

fn supplementary_group_count(root: &Path, name: &str, primary: u32) -> usize {
    let mut count = 0usize;
    for fields in account_lines(root, "etc/group") {
        let listed = fields
            .get(3)
            .map(|members| members.split(',').any(|member| member.trim() == name))
            .unwrap_or(false);
        if !listed {
            continue;
        }
        if let Some(gid) = fields.get(2).and_then(|field| parse_id(field)) {
            if gid != primary {
                count += 1;
            }
        }
    }
    count
}

pub fn linux_signal(sig: i32) -> Result<i32> {
    if sig == 0 {
        return Ok(0);
    }
    if sig < 0 {
        return Err(Error::Config(format!(
            "signal {} has no Linux ABI number",
            sig
        )));
    }
    for (linux, host) in HOST_SIGNALS {
        if *host == sig {
            return Ok(*linux);
        }
    }
    if LINUX_SIGNALS.iter().any(|(number, _)| *number == sig) {
        return Ok(sig);
    }
    Err(Error::Config(format!(
        "signal {} has no Linux ABI number",
        sig
    )))
}

pub fn linux_signal_name(number: i32) -> Option<&'static str> {
    LINUX_SIGNALS
        .iter()
        .find(|(candidate, _)| *candidate == number)
        .map(|(_, label)| *label)
}

pub fn signal_number(name: &str) -> Option<i32> {
    let upper = name.trim().to_ascii_uppercase();
    let bare = upper.strip_prefix("SIG").unwrap_or(upper.as_str());
    for (number, label) in LINUX_SIGNALS {
        if label.strip_prefix("SIG") == Some(bare) {
            return Some(*number);
        }
    }
    if let Ok(number) = upper.parse::<i32>() {
        if number == 0 || LINUX_SIGNALS.iter().any(|(known, _)| *known == number) {
            return Some(number);
        }
    }
    None
}

#[derive(Clone)]
enum StoredError {
    Killed,
    BadRef(String),
    UnsupportedPlatform(String),
    NoEntrypoint(String),
    ImageTooLarge,
    NetworkDenied(String),
    PathEscape(String),
    NotFound(String),
    Config(String),
    Message(String),
    Io(String),
    Json(String),
}

impl StoredError {
    fn capture(err: &Error) -> Self {
        match err {
            Error::Killed => StoredError::Killed,
            Error::BadRef(text) => StoredError::BadRef(text.clone()),
            Error::UnsupportedPlatform(text) => StoredError::UnsupportedPlatform(text.clone()),
            Error::NoEntrypoint(text) => StoredError::NoEntrypoint(text.clone()),
            Error::ImageTooLarge => StoredError::ImageTooLarge,
            Error::NetworkDenied(text) => StoredError::NetworkDenied(text.clone()),
            Error::PathEscape(text) => StoredError::PathEscape(text.clone()),
            Error::NotFound(text) => StoredError::NotFound(text.clone()),
            Error::Config(text) => StoredError::Config(text.clone()),
            Error::Message(text) => StoredError::Message(text.clone()),
            Error::Io(err) => StoredError::Io(err.to_string()),
            Error::Json(err) => StoredError::Json(err.to_string()),
        }
    }

    fn restore(&self) -> Error {
        match self {
            StoredError::Killed => Error::Killed,
            StoredError::BadRef(text) => Error::BadRef(text.clone()),
            StoredError::UnsupportedPlatform(text) => Error::UnsupportedPlatform(text.clone()),
            StoredError::NoEntrypoint(text) => Error::NoEntrypoint(text.clone()),
            StoredError::ImageTooLarge => Error::ImageTooLarge,
            StoredError::NetworkDenied(text) => Error::NetworkDenied(text.clone()),
            StoredError::PathEscape(text) => Error::PathEscape(text.clone()),
            StoredError::NotFound(text) => Error::NotFound(text.clone()),
            StoredError::Config(text) => Error::Config(text.clone()),
            StoredError::Message(text) => Error::Message(text.clone()),
            StoredError::Io(text) => Error::Io(std::io::Error::other(text.clone())),
            StoredError::Json(text) => Error::Message(format!("json: {}", text)),
        }
    }
}

struct WaitResult {
    code: i32,
    error: Option<StoredError>,
}

struct Outcome {
    result: Mutex<Option<WaitResult>>,
    ready: Condvar,
}

fn lock<T>(mutex: &Mutex<T>) -> MutexGuard<'_, T> {
    match mutex.lock() {
        Ok(guard) => guard,
        Err(poisoned) => poisoned.into_inner(),
    }
}

pub struct Process {
    context: Context,
    pid: Arc<AtomicU32>,
    pending: Arc<AtomicI32>,
    handles: Mutex<Option<(JoinHandle<()>, JoinHandle<()>)>>,
    outcome: Arc<Outcome>,
}

impl Process {
    pub fn wait(&self) -> Result<i32> {
        let taken = {
            let mut guard = lock(&self.handles);
            guard.take()
        };
        if let Some((supervisor, watcher)) = taken {
            if supervisor.join().is_err() {
                self.context.cancel();
                let mut guard = lock(&self.outcome.result);
                if guard.is_none() {
                    *guard = Some(WaitResult {
                        code: 0,
                        error: Some(StoredError::Message(
                            "supervisor thread ended unexpectedly".to_owned(),
                        )),
                    });
                }
                drop(guard);
                self.outcome.ready.notify_all();
            }
            let _ = watcher.join();
        }

        let mut guard = lock(&self.outcome.result);
        while guard.is_none() {
            guard = match self.outcome.ready.wait(guard) {
                Ok(next) => next,
                Err(poisoned) => poisoned.into_inner(),
            };
        }
        match guard.as_ref() {
            Some(result) => match &result.error {
                Some(err) => Err(err.restore()),
                None => Ok(result.code),
            },
            None => Err(Error::Message("run produced no outcome".to_owned())),
        }
    }

    pub fn kill(&self) {
        self.context.cancel();
    }

    pub fn pid(&self) -> u32 {
        self.pid.load(Ordering::SeqCst)
    }

    pub fn signal(&self, sig: i32) -> Result<()> {
        let linux = linux_signal(sig)?;
        if !self.running() {
            return Err(Error::Message("process is not running".to_owned()));
        }
        if linux == 0 {
            return Ok(());
        }
        self.pending.store(linux, Ordering::SeqCst);
        if FATAL_SIGNALS.contains(&linux) {
            self.context.cancel();
        }
        Ok(())
    }

    pub fn signal_named(&self, name: &str) -> Result<()> {
        match signal_number(name) {
            Some(number) => self.signal(number),
            None => Err(Error::Config(format!(
                "signal {} has no Linux ABI number",
                name
            ))),
        }
    }

    pub fn last_signal(&self) -> Option<i32> {
        match self.pending.load(Ordering::SeqCst) {
            0 => None,
            number => Some(number),
        }
    }

    fn running(&self) -> bool {
        lock(&self.outcome.result).is_none()
    }
}

fn spawn(parent: &Context, prog: String, cfg: Config) -> Result<Process> {
    let context = Context {
        cancelled: Arc::new(AtomicBool::new(false)),
        wake: Arc::clone(&parent.wake),
    };
    let outcome = Arc::new(Outcome {
        result: Mutex::new(None),
        ready: Condvar::new(),
    });
    let pid = Arc::new(AtomicU32::new(0));

    let supervisor_ctx = context.clone();
    let supervisor_outcome = Arc::clone(&outcome);
    let supervisor_pid = Arc::clone(&pid);
    let supervisor = match thread::Builder::new()
        .name("laboon-supervisor".to_owned())
        .spawn(move || {
            supervisor_pid.store(std::process::id(), Ordering::SeqCst);
            let result = crate::sandbox::run::run(&supervisor_ctx, &prog, &cfg);
            supervisor_ctx.cancel();
            supervisor_pid.store(0, Ordering::SeqCst);
            let value = match result {
                Ok(code) => WaitResult { code, error: None },
                Err(err) => WaitResult {
                    code: 0,
                    error: Some(StoredError::capture(&err)),
                },
            };
            let mut guard = lock(&supervisor_outcome.result);
            *guard = Some(value);
            drop(guard);
            supervisor_outcome.ready.notify_all();
        }) {
        Ok(handle) => handle,
        Err(err) => return Err(Error::Io(err)),
    };

    let watch_parent = parent.clone();
    let watch_child = context.clone();
    let watcher = match thread::Builder::new()
        .name("laboon-cancel".to_owned())
        .spawn(move || {
            watch_parent.wait_cancelled();
            watch_child.cancel();
        }) {
        Ok(handle) => handle,
        Err(err) => {
            context.cancel();
            return Err(Error::Io(err));
        }
    };

    Ok(Process {
        context,
        pid,
        pending: Arc::new(AtomicI32::new(0)),
        handles: Mutex::new(Some((supervisor, watcher))),
        outcome,
    })
}
