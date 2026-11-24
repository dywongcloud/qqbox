use crate::config::Config;
use crate::error::{Error, Result};
use crate::types::*;
use flate2::read::GzDecoder;
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};
use std::collections::BTreeMap;
use std::io::{Cursor, Read};
use std::path::{Path, PathBuf};

pub struct Image {
    pub reference: String,
    pub digest: String,
    pub root: PathBuf,
    pub config: ImageConfig,
}

#[derive(Debug, Clone)]
pub struct PullOptions {
    pub cache_dir: Option<PathBuf>,
    pub auth_config: Option<PathBuf>,
    pub max_size: u64,
    pub refresh: bool,
    pub arch: Arch,
}

impl Default for PullOptions {
    fn default() -> Self {
        PullOptions {
            cache_dir: None,
            auth_config: None,
            max_size: 16 << 30,
            refresh: false,
            arch: Arch::host(),
        }
    }
}

#[derive(Debug, Clone, Default, Serialize, Deserialize)]
pub struct ImageConfig {
    #[serde(default)]
    pub created: String,
    #[serde(default)]
    pub author: String,
    #[serde(default)]
    pub architecture: String,
    #[serde(default)]
    pub os: String,
    #[serde(rename = "os.version", default)]
    pub os_version: String,
    #[serde(rename = "os.features", default)]
    pub os_features: Vec<String>,
    #[serde(default)]
    pub variant: String,
    #[serde(default)]
    pub config: RuntimeConfig,
    #[serde(default)]
    pub rootfs: RootFs,
    #[serde(default)]
    pub history: Vec<History>,
}

#[derive(Debug, Clone, Default, Serialize, Deserialize)]
pub struct RuntimeConfig {
    #[serde(rename = "User", default)]
    pub user: String,
    #[serde(rename = "ExposedPorts", default)]
    pub exposed_ports: BTreeMap<String, serde_json::Value>,
    #[serde(rename = "Env", default)]
    pub env: Vec<String>,
    #[serde(rename = "Entrypoint", default)]
    pub entrypoint: Vec<String>,
    #[serde(rename = "Cmd", default)]
    pub cmd: Vec<String>,
    #[serde(rename = "Volumes", default)]
    pub volumes: BTreeMap<String, serde_json::Value>,
    #[serde(rename = "WorkingDir", default)]
    pub working_dir: String,
    #[serde(rename = "Labels", default)]
    pub labels: LabelMap,
    #[serde(rename = "StopSignal", default)]
    pub stop_signal: String,
    #[serde(rename = "ArgsEscaped", default)]
    pub args_escaped: bool,
    #[serde(rename = "Shell", default)]
    pub shell: Vec<String>,
    #[serde(rename = "OnBuild", default)]
    pub on_build: Vec<String>,
    #[serde(rename = "Hostname", default)]
    pub hostname: String,
    #[serde(rename = "Domainname", default)]
    pub domainname: String,
    #[serde(rename = "AttachStdin", default)]
    pub attach_stdin: bool,
    #[serde(rename = "AttachStdout", default)]
    pub attach_stdout: bool,
    #[serde(rename = "AttachStderr", default)]
    pub attach_stderr: bool,
    #[serde(rename = "Tty", default)]
    pub tty: bool,
    #[serde(rename = "OpenStdin", default)]
    pub open_stdin: bool,
    #[serde(rename = "StdinOnce", default)]
    pub stdin_once: bool,
    #[serde(rename = "NetworkDisabled", default)]
    pub network_disabled: bool,
    #[serde(rename = "MacAddress", default)]
    pub mac_address: String,
    #[serde(rename = "Healthcheck", default)]
    pub healthcheck: Option<HealthcheckJson>,
}

#[derive(Debug, Clone, Default, Serialize, Deserialize)]
pub struct HealthcheckJson {
    #[serde(rename = "Test", default)]
    pub test: Vec<String>,
    #[serde(rename = "Interval", default)]
    pub interval_ns: u64,
    #[serde(rename = "Timeout", default)]
    pub timeout_ns: u64,
    #[serde(rename = "Retries", default)]
    pub retries: u32,
    #[serde(rename = "StartPeriod", default)]
    pub start_period_ns: u64,
}

#[derive(Debug, Clone, Default, Serialize, Deserialize)]
pub struct RootFs {
    #[serde(rename = "type", default)]
    pub type_: String,
    #[serde(default)]
    pub diff_ids: Vec<String>,
}

#[derive(Debug, Clone, Default, Serialize, Deserialize)]
pub struct History {
    #[serde(default)]
    pub created: String,
    #[serde(default)]
    pub created_by: String,
    #[serde(default)]
    pub comment: String,
    #[serde(default)]
    pub empty_layer: bool,
}

pub fn parse_image_config(bytes: &[u8]) -> Result<ImageConfig> {
    let config: ImageConfig = serde_json::from_slice(bytes)?;
    Ok(config)
}

pub fn to_run_config(c: &ImageConfig) -> Result<Config> {
    let mut cfg = Config {
        env: c.config.env.clone(),
        ..Config::default()
    };
    if cfg.env.is_empty() {
        cfg.env = Config::default_env();
    }
    if !c.config.env.iter().any(|e| e.starts_with("HOME=")) {
        cfg.env.push("HOME=/".to_string());
    }
    cfg.entrypoint = c.config.entrypoint.clone();
    cfg.cmd = c.config.cmd.clone();
    cfg.working_dir = c.config.working_dir.clone();
    cfg.dir = c.config.working_dir.clone();
    cfg.user_spec = c.config.user.clone();
    for (k, v) in &c.config.labels {
        validate_label_key(k)?;
        validate_label_value(v)?;
        cfg.labels.insert(k.clone(), v.clone());
    }
    cfg.hostname = c.config.hostname.clone();
    cfg.domainname = c.config.domainname.clone();
    cfg.stop_signal = if c.config.stop_signal.is_empty() {
        "SIGTERM".to_string()
    } else {
        c.config.stop_signal.clone()
    };
    cfg.stdin_open = c.config.open_stdin;
    cfg.tty = c.config.tty;
    cfg.stdin = if c.config.attach_stdin {
        Stdio::Pipe
    } else {
        Stdio::Null
    };
    cfg.stdout = if c.config.attach_stdout {
        Stdio::Pipe
    } else {
        Stdio::Null
    };
    cfg.stderr = if c.config.attach_stderr {
        Stdio::Pipe
    } else {
        Stdio::Null
    };
    if c.config.network_disabled {
        cfg.no_net = true;
    }
    for key in c.config.exposed_ports.keys() {
        cfg.exposed_ports
            .push(crate::net::ports::parse_port_spec(key)?);
    }
    cfg.volumes = c
        .config
        .volumes
        .keys()
        .map(|path| VolumeSpec {
            name: String::new(),
            container_path: path.clone(),
            driver: "local".to_string(),
            read_only: false,
            propagation: MountPropagation::Private,
            source: None,
        })
        .collect();
    if let Some(h) = &c.config.healthcheck {
        cfg.healthcheck = Some(Healthcheck {
            test: h.test.clone(),
            interval_ns: h.interval_ns,
            timeout_ns: h.timeout_ns,
            retries: h.retries,
            start_period_ns: h.start_period_ns,
        });
    }
    cfg.image = String::new();
    Ok(cfg)
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct ImageRef {
    pub registry: String,
    pub repository: String,
    pub tag: Option<String>,
    pub digest: Option<String>,
}

fn valid_tag(tag: &str) -> bool {
    !tag.is_empty()
        && tag.len() <= 128
        && tag
            .bytes()
            .next()
            .is_some_and(|b| b.is_ascii_alphanumeric() || b == b'_')
        && tag
            .bytes()
            .all(|b| b.is_ascii_alphanumeric() || matches!(b, b'_' | b'.' | b'-'))
}

/// OCI digest grammar: algorithm ":" encoded-hex, e.g. `sha256:<64 hex>`.
fn valid_digest(digest: &str) -> bool {
    let Some((algorithm, encoded)) = digest.split_once(':') else {
        return false;
    };
    !algorithm.is_empty()
        && algorithm.len() <= 32
        && algorithm.bytes().all(|b| {
            b.is_ascii_lowercase() || b.is_ascii_digit() || matches!(b, b'+' | b'.' | b'_' | b'-')
        })
        && encoded.len() >= 32
        && encoded.len() <= 128
        && encoded.bytes().all(|b| b.is_ascii_hexdigit())
}

fn valid_registry(registry: &str) -> bool {
    let (host, port) = match registry.rsplit_once(':') {
        Some((h, p)) => (h, Some(p)),
        None => (registry, None),
    };
    if let Some(p) = port {
        // `alpine:../etc` must not be reinterpreted as a registry with a
        // non-numeric "port". A port is digits only.
        if p.is_empty() || !p.bytes().all(|b| b.is_ascii_digit()) || p.len() > 5 {
            return false;
        }
    }
    !host.is_empty()
        && !host.contains("..")
        && !host.starts_with('.')
        && !host.ends_with('.')
        && host
            .chars()
            .all(|c| c.is_ascii_alphanumeric() || c == '.' || c == '-')
}

fn valid_repository(repository: &str) -> bool {
    !repository.is_empty()
        && !repository.starts_with('/')
        && !repository.ends_with('/')
        && repository.split('/').all(|component| {
            !component.is_empty()
                && component != "."
                && component != ".."
                && !component.starts_with('.')
                && component.bytes().all(|b| {
                    b.is_ascii_lowercase()
                        || b.is_ascii_digit()
                        || matches!(b, b'_' | b'-' | b'.' | b'+')
                })
        })
}

impl ImageRef {
    pub fn parse(s: &str) -> Result<ImageRef> {
        let s = s.trim();
        if s.is_empty() {
            return Err(Error::BadRef("empty reference".into()));
        }
        let (name, digest) = match s.rsplit_once('@') {
            Some((n, d)) => (n, Some(d.to_string())),
            None => (s, None),
        };
        let (name, tag) = match name.rsplit_once(':') {
            Some((n, t)) if !t.contains('/') => (n, Some(t.to_string())),
            _ => (name, None),
        };
        let mut parts = name.splitn(2, '/');
        let first = parts.next().unwrap_or("");
        let (registry, repository) = match parts.next() {
            Some(rest) if first.contains('.') || first.contains(':') || first == "localhost" => {
                (first.to_string(), rest.to_string())
            }
            Some(rest) => (
                "registry-1.docker.io".to_string(),
                format!("{first}/{rest}"),
            ),
            None => (
                "registry-1.docker.io".to_string(),
                format!("library/{first}"),
            ),
        };
        if !valid_registry(&registry) || !valid_repository(&repository) {
            return Err(Error::BadRef(s.to_string()));
        }
        if let Some(t) = &tag {
            if !valid_tag(t) {
                return Err(Error::BadRef(format!("invalid tag in {s}")));
            }
        }
        if let Some(d) = &digest {
            if !valid_digest(d) {
                return Err(Error::BadRef(format!("invalid digest in {s}")));
            }
        }
        Ok(ImageRef {
            registry,
            repository,
            tag: if digest.is_none() { tag } else { None },
            digest,
        })
    }

    pub fn name(&self) -> String {
        format!("{}/{}", self.registry, self.repository)
    }

    pub fn reference(&self) -> String {
        match (&self.tag, &self.digest) {
            (_, Some(d)) => format!("{}@{}", self.name(), d),
            (Some(t), None) => format!("{}:{}", self.name(), t),
            (None, None) => format!("{}:latest", self.name()),
        }
    }

    pub fn manifest_url(&self) -> String {
        let r = match (&self.tag, &self.digest) {
            (_, Some(d)) => d.clone(),
            (Some(t), None) => t.clone(),
            (None, None) => "latest".to_string(),
        };
        format!(
            "https://{}/v2/{}/manifests/{}",
            self.registry, self.repository, r
        )
    }

    pub fn blob_url(&self, digest: &str) -> String {
        format!(
            "https://{}/v2/{}/blobs/{}",
            self.registry, self.repository, digest
        )
    }
}

/// Libraries take options, not the environment. A cache root is a required
/// caller-supplied option; the deterministic fallback is a fixed path, never
/// `TMPDIR`/`std::env::temp_dir()`. The fallback is created 0700 and checked
/// on every use, so a local user cannot pre-plant refs or layer contents.
pub fn cache_dir(o: &PullOptions) -> PathBuf {
    o.cache_dir
        .clone()
        .unwrap_or_else(|| PathBuf::from("/tmp/qqbox/images"))
}

fn ensure_private_dir(dir: &Path) -> Result<()> {
    std::fs::create_dir_all(dir)?;
    #[cfg(unix)]
    {
        use std::os::unix::fs::{MetadataExt, PermissionsExt};
        let meta = std::fs::symlink_metadata(dir)?;
        if meta.file_type().is_symlink() || !meta.is_dir() {
            return Err(Error::Config(format!(
                "image cache {} is not a real directory",
                dir.display()
            )));
        }
        if meta.uid() != unsafe { libc::geteuid() } {
            return Err(Error::Config(format!(
                "image cache {} is owned by another user",
                dir.display()
            )));
        }
        if meta.permissions().mode() & 0o077 != 0 {
            std::fs::set_permissions(dir, std::fs::Permissions::from_mode(0o700))?;
        }
    }
    Ok(())
}

/// Read a cache entry without following a symlink out of the private root.
fn read_cache_bytes(path: &Path) -> Result<Vec<u8>> {
    #[cfg(unix)]
    {
        let meta = std::fs::symlink_metadata(path)?;
        if meta.file_type().is_symlink() {
            return Err(Error::Config(format!(
                "image cache entry {} is a symlink",
                path.display()
            )));
        }
    }
    Ok(std::fs::read(path)?)
}

/// Constant-time equality over two byte strings of equal length.
fn ct_eq(a: &[u8], b: &[u8]) -> bool {
    if a.len() != b.len() {
        return false;
    }
    let mut diff = 0u8;
    for (x, y) in a.iter().zip(b.iter()) {
        diff |= x ^ y;
    }
    diff == 0
}

/// Verify that registry bytes really hash to the digest the manifest named.
/// Without this, a digest-pinned reference is only a URL string, not a pin.
fn verify_content_digest(digest: &str, body: &[u8]) -> Result<()> {
    let Some(encoded) = digest.strip_prefix("sha256:") else {
        return Err(Error::Config(format!(
            "unsupported content digest algorithm in {digest}"
        )));
    };
    if encoded.len() != 64 || !encoded.bytes().all(|b| b.is_ascii_hexdigit()) {
        return Err(Error::Config(format!("malformed content digest {digest}")));
    }
    let got = hex::encode(Sha256::digest(body));
    if !ct_eq(got.as_bytes(), encoded.as_bytes()) {
        return Err(Error::Config(format!(
            "content digest mismatch: manifest says {encoded}, registry returned {got}"
        )));
    }
    Ok(())
}

fn ref_key(r: &ImageRef, arch: Arch) -> String {
    let mut key = r.name();
    if arch != Arch::Amd64 {
        key.push('@');
        key.push_str(arch.as_str());
    }
    key.chars()
        .map(|c| if c.is_ascii_alphanumeric() { c } else { '_' })
        .collect()
}

pub fn valid_hex(s: &str) -> bool {
    s.len() == 64
        && s.chars()
            .all(|c| c.is_ascii_hexdigit() && !c.is_ascii_uppercase())
}

pub fn local(r: &ImageRef, o: &PullOptions) -> Result<Option<Image>> {
    let dir = cache_dir(o);
    ensure_private_dir(&dir)?;
    ensure_private_dir(&dir.join("refs"))?;
    let ref_file = dir.join("refs").join(ref_key(r, o.arch));
    let hex = match std::fs::read_to_string(&ref_file) {
        Ok(v) => v.trim().to_string(),
        Err(_) => return Ok(None),
    };
    if !valid_hex(&hex) {
        return Err(Error::Config("corrupt ref entry".into()));
    }
    let image_dir = dir.join(&hex);
    ensure_private_dir(&image_dir)?;
    let bytes = read_cache_bytes(&image_dir.join("config.json"))?;
    // A pre-planted config.json must hash to the directory name it lives in.
    let got = hex::encode(Sha256::digest(&bytes));
    if !ct_eq(got.as_bytes(), hex.as_bytes()) {
        return Err(Error::Config(format!(
            "cached config digest mismatch: ref says {hex}, bytes hash to {got}"
        )));
    }
    let config = parse_image_config(&bytes)?;
    let root = image_dir.join("rootfs");
    if !root.is_dir() {
        return Ok(None);
    }
    Ok(Some(Image {
        reference: r.reference(),
        digest: format!("sha256:{hex}"),
        root,
        config,
    }))
}

pub fn remember(r: &ImageRef, digest: &str, o: &PullOptions) -> Result<()> {
    let dir = cache_dir(o);
    ensure_private_dir(&dir)?;
    let refs = dir.join("refs");
    ensure_private_dir(&refs)?;
    let dir = refs;
    std::fs::write(dir.join(ref_key(r, o.arch)), format!("{digest}\n"))?;
    Ok(())
}

fn auth_header(registry: &str, config: Option<&Path>) -> Option<String> {
    let path = config?;
    let value: serde_json::Value = serde_json::from_slice(&std::fs::read(path).ok()?).ok()?;
    let mut encoded = value
        .get("auths")?
        .get(registry)?
        .get("auth")?
        .as_str()?
        .to_string();
    if encoded.is_empty() {
        return None;
    }
    use base64::Engine;
    let decoded = base64::engine::general_purpose::STANDARD
        .decode(&encoded)
        .ok()?;
    encoded.clear();
    let text = String::from_utf8(decoded).ok()?;
    Some(format!("Basic {}", {
        use base64::Engine;
        base64::engine::general_purpose::STANDARD.encode(text.as_bytes())
    }))
}

fn get(url: &str, accept: &str, auth: Option<&str>) -> Result<Vec<u8>> {
    let mut req = ureq::get(url).set("Accept", accept);
    if let Some(a) = auth {
        req = req.set("Authorization", a);
    }
    let response = req
        .call()
        .map_err(|e| Error::Message(format!("registry request failed: {e}")))?;
    let mut body = Vec::new();
    response
        .into_reader()
        .take(64 << 20)
        .read_to_end(&mut body)?;
    Ok(body)
}

#[derive(Debug, Clone, Default, Serialize, Deserialize)]
pub struct Manifest {
    #[serde(default)]
    pub schema_version: u32,
    #[serde(default)]
    pub media_type: String,
    #[serde(default)]
    pub config: Descriptor,
    #[serde(default)]
    pub layers: Vec<Descriptor>,
    #[serde(default)]
    pub manifests: Vec<PlatformDescriptor>,
}

#[derive(Debug, Clone, Default, Serialize, Deserialize)]
pub struct Descriptor {
    #[serde(default)]
    pub media_type: String,
    #[serde(default)]
    pub digest: String,
    #[serde(default)]
    pub size: u64,
}

#[derive(Debug, Clone, Default, Serialize, Deserialize)]
pub struct PlatformDescriptor {
    #[serde(default)]
    pub media_type: String,
    #[serde(default)]
    pub digest: String,
    #[serde(default)]
    pub size: u64,
    #[serde(default)]
    pub platform: Platform,
}

#[derive(Debug, Clone, Default, Serialize, Deserialize)]
pub struct Platform {
    #[serde(default)]
    pub architecture: String,
    #[serde(default)]
    pub os: String,
    #[serde(default)]
    pub variant: String,
    #[serde(rename = "os.version", default)]
    pub os_version: String,
}

pub fn select_platform(index: &Manifest, arch: Arch, os: &str) -> Result<String> {
    if index.manifests.is_empty() {
        return Ok(String::new());
    }
    for m in &index.manifests {
        if m.platform.architecture == arch.as_str() && m.platform.os == os {
            return Ok(m.digest.clone());
        }
    }
    Err(Error::UnsupportedPlatform(format!(
        "{os}/{} not present in the index",
        arch.as_str()
    )))
}

pub fn fetch_manifest(r: &ImageRef, o: &PullOptions) -> Result<(Manifest, String)> {
    let auth = auth_header(&r.registry, o.auth_config.as_deref());
    let accept = "application/vnd.oci.image.index.v1+json, application/vnd.docker.distribution.manifest.list.v2+json, application/vnd.oci.image.manifest.v1+json, application/vnd.docker.distribution.manifest.v2+json";
    let bytes = get(&r.manifest_url(), accept, auth.as_deref())?;
    if let Some(pin) = r.digest.as_deref() {
        verify_content_digest(pin, &bytes)?;
    }
    let manifest: Manifest = serde_json::from_slice(&bytes)?;
    let digest = if manifest.manifests.is_empty() {
        r.digest.clone().unwrap_or_default()
    } else {
        let d = select_platform(&manifest, o.arch, "linux")?;
        let url = format!("https://{}/v2/{}/manifests/{}", r.registry, r.repository, d);
        let sub = get(&url, accept, auth.as_deref())?;
        verify_content_digest(&d, &sub)?;
        let m: Manifest = serde_json::from_slice(&sub)?;
        return Ok((m, d));
    };
    Ok((manifest, digest))
}

pub fn fetch_blob(r: &ImageRef, digest: &str, o: &PullOptions) -> Result<Vec<u8>> {
    let auth = auth_header(&r.registry, o.auth_config.as_deref());
    let body = get(
        &r.blob_url(digest),
        "application/octet-stream",
        auth.as_deref(),
    )?;
    verify_content_digest(digest, &body)?;
    Ok(body)
}

pub fn pull(reference: &str, o: &PullOptions) -> Result<Image> {
    let r = ImageRef::parse(reference)?;
    if !o.refresh {
        if let Some(img) = local(&r, o)? {
            return Ok(img);
        }
    }
    let (manifest, manifest_digest) = fetch_manifest(&r, o)?;
    let config_bytes = fetch_blob(&r, &manifest.config.digest, o)?;
    let config = parse_image_config(&config_bytes)?;
    if !config.os.is_empty() && config.os != "linux" {
        return Err(Error::UnsupportedPlatform(format!(
            "{} is {}, need linux/{}",
            reference,
            config.os,
            o.arch.as_str()
        )));
    }
    let mut hasher = sha2::Sha256::new();
    hasher.update(&config_bytes);
    let hex = hex::encode(hasher.finalize());
    let dir = cache_dir(o);
    ensure_private_dir(&dir)?;
    let image_dir = dir.join(&hex);
    ensure_private_dir(&image_dir)?;
    let root = image_dir.join("rootfs");
    if !root.is_dir() {
        std::fs::create_dir_all(&image_dir)?;
        let tmp = image_dir.join(".unpack");
        let _ = std::fs::remove_dir_all(&tmp);
        std::fs::create_dir_all(&tmp)?;
        let mut total = 0u64;
        let mut written = 0u64;
        for layer in &manifest.layers {
            let bytes = fetch_blob(&r, &layer.digest, o)?;
            total = total.saturating_add(bytes.len() as u64);
            if total > o.max_size {
                let _ = std::fs::remove_dir_all(&tmp);
                return Err(Error::ImageTooLarge);
            }
            unpack_budgeted(&bytes, &tmp, o.max_size, &mut written)?;
        }
        // The cache directory name is SHA-256 of the RAW registry config blob
        // (see the hasher above and `local`). Writing a serde re-serialization
        // here would make the next `local()` recompute a different digest and
        // reject its own cache as "pre-planted". Preserve the exact bytes.
        std::fs::write(image_dir.join("config.json"), &config_bytes)?;
        match std::fs::rename(&tmp, &root) {
            Ok(()) => {}
            Err(_) if root.is_dir() => {}
            Err(e) => return Err(Error::Io(e)),
        }
    }
    remember(&r, &hex, o)?;
    let _ = manifest_digest;
    Ok(Image {
        reference: r.reference(),
        digest: format!("sha256:{hex}"),
        root,
        config,
    })
}

/// Store a raw OCI config blob. `local()` hashes these exact bytes against the
/// digest-named cache directory, so the blob must never be re-serialized.
pub fn store_config(dir: &Path, raw_config: &[u8]) -> Result<()> {
    std::fs::create_dir_all(dir)?;
    std::fs::write(dir.join("config.json"), raw_config)?;
    Ok(())
}

/// A whiteout remainder must be exactly one ordinary path component.
fn safe_whiteout_name(white: &str) -> bool {
    !white.is_empty()
        && white != "."
        && white != ".."
        && !white.starts_with('/')
        && !white.contains('/')
        && !white.contains('\\')
        && !white.chars().any(char::is_control)
}

/// Remove the contents of `dir` without removing or replacing `dir` itself.
fn clear_dir_entries(dir: &Path) -> Result<()> {
    for entry in std::fs::read_dir(dir)? {
        let entry = entry?;
        let path = entry.path();
        match entry.file_type()? {
            kind if kind.is_dir() => std::fs::remove_dir_all(path)?,
            _ => std::fs::remove_file(path)?,
        }
    }
    Ok(())
}

pub fn unpack(flat: &[u8], dest: &Path, max_size: u64) -> Result<()> {
    let mut written = 0u64;
    unpack_budgeted(flat, dest, max_size, &mut written)
}

/// `written` is shared across layers so N small-compressed layers cannot each
/// write a full `max_size` of decompressed bytes.
pub fn unpack_budgeted(flat: &[u8], dest: &Path, max_size: u64, written: &mut u64) -> Result<()> {
    let decoder = GzDecoder::new(Cursor::new(flat));
    let mut archive = tar::Archive::new(decoder);
    let entries = archive.entries()?;
    for entry in entries {
        let mut entry = entry?;
        let path = match entry.path() {
            Ok(p) => p.into_owned(),
            Err(_) => continue,
        };
        let cleaned = match clean_guest(&path) {
            Some(c) => c,
            None => continue,
        };
        let target = confined(dest, &dest.join(&cleaned))?;
        let name = cleaned.rsplit('/').next().unwrap_or("");
        if name.starts_with(".wh.") {
            let Some(parent) = target.parent() else {
                continue;
            };
            if name == ".wh..wh..opq" {
                // Opaque whiteouts clear the entries of their directory only.
                // Removing `parent` recursively would delete `dest` itself
                // when the marker sits at the root of the layer.
                clear_dir_entries(parent)?;
            } else if let Some(white) = name.strip_prefix(".wh.") {
                // `.wh...` leaves white=".." and `.wh..` leaves white=".".
                // Either used to make `remove_dir_all` climb above `dest`.
                if !safe_whiteout_name(white) {
                    return Err(Error::PathEscape(format!("unsafe whiteout {name}")));
                }
                let gone = confined(dest, &parent.join(white))?;
                if gone == dest || !gone.starts_with(dest) {
                    return Err(Error::PathEscape(format!("whiteout target {white}")));
                }
                // A symlink must be unlinked, never followed outside `dest`.
                match std::fs::symlink_metadata(&gone) {
                    Ok(meta) if meta.file_type().is_symlink() || meta.is_file() => {
                        let _ = std::fs::remove_file(&gone);
                    }
                    Ok(meta) if meta.is_dir() => {
                        let _ = std::fs::remove_dir_all(&gone);
                    }
                    _ => {}
                }
            }
            continue;
        }
        let kind = entry.header().entry_type();
        if kind.is_gnu_longname()
            || kind.is_pax_global_extensions()
            || kind.is_pax_local_extensions()
        {
            continue;
        }
        if matches!(kind.as_byte(), b'3' | b'4' | b'6') {
            continue;
        }
        if let Some(parent) = target.parent() {
            std::fs::create_dir_all(parent)?;
        }
        if kind.is_dir() {
            std::fs::create_dir_all(&target)?;
        } else if kind.is_symlink() {
            let link = entry
                .link_name()
                .ok()
                .flatten()
                .map(|p| p.into_owned())
                .unwrap_or_default();
            if is_confined_link(&link.to_string_lossy()) {
                #[cfg(unix)]
                {
                    let _ = std::fs::remove_file(&target);
                    std::os::unix::fs::symlink(&link, &target)?;
                }
                #[cfg(not(unix))]
                {
                    let _ = link;
                    std::fs::write(&target, b"")?;
                }
            }
        } else if kind.is_file() {
            let size = entry.size();
            *written = written.saturating_add(size);
            if *written > max_size {
                return Err(Error::ImageTooLarge);
            }
            let mut out = std::fs::File::create(&target)?;
            std::io::copy(&mut entry, &mut out)?;
            let mode = entry.header().mode().unwrap_or(0o644) & 0o755;
            #[cfg(unix)]
            {
                use std::os::unix::fs::PermissionsExt;
                let _ = std::fs::set_permissions(&target, std::fs::Permissions::from_mode(mode));
            }
        }
    }
    Ok(())
}

fn clean_guest(path: &Path) -> Option<String> {
    let mut parts: Vec<&str> = Vec::new();
    for component in path.components() {
        match component {
            std::path::Component::Normal(s) => {
                let s = s.to_str()?;
                if s == ".." {
                    parts.pop();
                } else if s != "." && !s.is_empty() {
                    parts.push(s);
                }
            }
            std::path::Component::ParentDir => {
                parts.pop();
            }
            std::path::Component::CurDir => {}
            _ => {}
        }
    }
    if parts.is_empty() {
        None
    } else {
        Some(parts.join("/"))
    }
}

fn is_confined_link(link: &str) -> bool {
    !link.starts_with('/') && !link.split('/').any(|c| c == "..")
}

/// Delegate to the single shared confiner; the local copy previously rebuilt
/// absolute candidates as relative and doubled the root prefix.
fn confined(root: &Path, candidate: &Path) -> Result<PathBuf> {
    crate::vfs::path::confined(root, candidate)
}

pub fn look_path(root: &Path, program: &str, env: &[String]) -> Result<PathBuf> {
    if program.contains('/') {
        let candidate = confined(root, &root.join(program.trim_start_matches('/')))?;
        if is_executable(&candidate) {
            return Ok(candidate);
        }
        return Err(Error::NotFound(program.to_string()));
    }
    let path = env
        .iter()
        .find_map(|e| e.strip_prefix("PATH=").map(|v| v.to_string()))
        .unwrap_or_else(|| "/usr/local/bin:/usr/bin:/bin".to_string());
    for dir in path.split(':') {
        let candidate = confined(root, &root.join(dir.trim_start_matches('/')).join(program))?;
        if is_executable(&candidate) {
            return Ok(candidate);
        }
    }
    Err(Error::NotFound(program.to_string()))
}

fn is_executable(p: &Path) -> bool {
    let meta = match std::fs::metadata(p) {
        Ok(m) => m,
        Err(_) => return false,
    };
    if !meta.is_file() {
        return false;
    }
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        meta.permissions().mode() & 0o111 != 0
    }
    #[cfg(not(unix))]
    {
        true
    }
}

pub mod config {
    pub use super::{
        parse_image_config, to_run_config, HealthcheckJson, History, ImageConfig, RootFs,
        RuntimeConfig,
    };
}

pub mod reference {
    pub use super::ImageRef;
}

pub mod registry {
    pub use super::{
        fetch_blob, fetch_manifest, select_platform, Descriptor, Manifest, Platform,
        PlatformDescriptor,
    };
}

pub mod cache {
    pub use super::{cache_dir, local, remember, valid_hex};
}

pub mod layers {
    pub use super::unpack;
}

pub mod lookpath {
    pub use super::look_path;
}

pub mod runtime;
