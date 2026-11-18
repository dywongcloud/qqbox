use std::collections::BTreeMap;
use std::net::{IpAddr, SocketAddr};
use std::sync::{Arc, Mutex, MutexGuard};
use std::time::{Duration, Instant};

use crate::config::Config;
use crate::error::{Error, Result};
use crate::net::dns::{self, DnsCode, DnsResolver, IpCidr, CLASS_IN};
use crate::net::socket::{Afi, SockKind};
use crate::types::NetworkMode;

const PIN_TTL: Duration = Duration::from_secs(120);
const MAX_PINS: usize = 8192;
const DNS_TTL: u32 = 30;

#[derive(Debug, Default)]
struct Pinned {
    entries: BTreeMap<String, Instant>,
}

#[derive(Debug, Clone)]
pub struct EgressPolicy {
    no_net: bool,
    net: bool,
    allow_domains: Vec<String>,
    cidrs: Vec<IpCidr>,
    allow_ports: Vec<u16>,
    resolver: DnsResolver,
    pinned: Arc<Mutex<Pinned>>,
}

impl Default for EgressPolicy {
    fn default() -> Self {
        EgressPolicy::from_config(&Config::default())
    }
}

impl EgressPolicy {
    pub fn from_config(config: &Config) -> Self {
        let no_net = config.no_net || config.network_mode == NetworkMode::None;
        let net = (config.net || config.network_mode == NetworkMode::Host) && !no_net;
        let mut allow_domains = Vec::new();
        for domain in &config.allow_domains {
            let normalized = domain.trim().trim_end_matches('.').to_ascii_lowercase();
            if !normalized.is_empty() && dns::is_valid_name(&normalized) {
                allow_domains.push(normalized);
            }
        }
        let mut cidrs = Vec::new();
        for spec in &config.allow_cidrs {
            if let Ok(network) = IpCidr::parse(spec) {
                cidrs.push(network);
            }
        }
        let mut allow_ports = config.allow_ports.clone();
        allow_ports.sort_unstable();
        allow_ports.dedup();
        EgressPolicy {
            no_net,
            net,
            allow_domains,
            cidrs,
            allow_ports,
            resolver: DnsResolver::from_config(config),
            pinned: Arc::new(Mutex::new(Pinned::default())),
        }
    }

    pub fn allows_socket(&self, family: Afi, kind: SockKind) -> Result<()> {
        if !family.is_inet() {
            return Ok(());
        }
        if self.no_net {
            return Err(Error::NetworkDenied(
                "no_net forbids all inet sockets".to_owned(),
            ));
        }
        if kind == SockKind::Raw {
            return Err(Error::NetworkDenied(
                "raw inet sockets bypass the egress policy".to_owned(),
            ));
        }
        Ok(())
    }

    pub fn allows_connect(&self, addr: &SocketAddr, resolved_via_dns: bool) -> Result<()> {
        if self.no_net {
            return Err(Error::NetworkDenied("network is disabled".to_owned()));
        }
        let ip = addr.ip();
        if dns::is_loopback_ip(ip) {
            return Ok(());
        }
        if !self.net {
            return Err(Error::NetworkDenied(
                "loopback-only network: egress is denied".to_owned(),
            ));
        }
        if dns::is_unspecified_ip(ip) {
            return Err(Error::NetworkDenied(
                "the unspecified address is not an egress target".to_owned(),
            ));
        }
        if dns::is_internal(ip) && !self.cidr_matches(ip) {
            return Err(Error::NetworkDenied(format!(
                "internal address {} requires an explicit allow_cidr",
                ip
            )));
        }
        if !self.allow_ports.is_empty() && !self.allow_ports.contains(&addr.port()) {
            return Err(Error::NetworkDenied(format!(
                "destination port {} is not allowed",
                addr.port()
            )));
        }
        if !self.restricted() {
            return Ok(());
        }
        if self.cidr_matches(ip) {
            return Ok(());
        }
        if resolved_via_dns && self.is_pinned(ip) {
            return Ok(());
        }
        Err(Error::NetworkDenied(format!(
            "{} was neither resolved through supervisor dns nor allowed by cidr",
            addr
        )))
    }

    pub fn resolve(&self, name: &str) -> Result<Vec<IpAddr>> {
        if self.no_net {
            return Err(Error::NetworkDenied(
                "network is disabled: dns is not answered".to_owned(),
            ));
        }
        if !self.domain_allowed(name) {
            return Err(Error::NetworkDenied(format!(
                "dns name {} is not in the domain allowlist",
                name
            )));
        }
        let mut addresses: Vec<IpAddr> = Vec::new();
        let mut failures: Vec<String> = Vec::new();
        for family in [Afi::Inet, Afi::Inet6] {
            let answer = if self.net {
                self.resolver.lookup(name, family)
            } else {
                self.resolver.lookup_local(name, family)
            };
            match answer {
                Ok(found) => addresses.extend(found),
                Err(err) => failures.push(err.to_string()),
            }
        }
        addresses.retain(|ip| !dns::is_internal(*ip) || self.cidr_matches(*ip));
        addresses.sort_by_key(|ip| ip.to_string());
        addresses.dedup();
        if addresses.is_empty() {
            return Err(Error::NetworkDenied(format!(
                "no permitted address for {}: {}",
                name,
                failures.join("; ")
            )));
        }
        for ip in &addresses {
            self.pin(*ip)?;
        }
        Ok(addresses)
    }

    pub fn answer(&self, query: &[u8]) -> Result<Vec<u8>> {
        let message = dns::parse_query(query)
            .map_err(|err| Error::Message(format!("malformed dns query: {}", err)))?;
        if !self.domain_allowed(&message.question.name) {
            return dns::reply(&message, DnsCode::Refused, 0, &[]);
        }
        if message.question.qclass != CLASS_IN {
            return dns::reply(&message, DnsCode::NotImplemented, 0, &[]);
        }
        match self.resolve(&message.question.name) {
            Ok(addresses) => dns::reply(&message, DnsCode::Success, DNS_TTL, &addresses),
            Err(_) => dns::reply(&message, DnsCode::ServerFailure, 0, &[]),
        }
    }

    pub fn answer_framed(&self, frame: &[u8]) -> Result<Vec<u8>> {
        if frame.len() < 2 {
            return Err(Error::Message("dns stream frame is too short".to_owned()));
        }
        let length = usize::from(u16::from_be_bytes([frame[0], frame[1]]));
        if frame.len() != length + 2 {
            return Err(Error::Message(
                "dns stream frame length mismatch".to_owned(),
            ));
        }
        let answer = self.answer(&frame[2..])?;
        if answer.len() > usize::from(u16::MAX) {
            return Err(Error::Message(
                "dns answer exceeds the stream frame limit".to_owned(),
            ));
        }
        let mut out = Vec::with_capacity(answer.len() + 2);
        out.extend_from_slice(&(answer.len() as u16).to_be_bytes());
        out.extend_from_slice(&answer);
        Ok(out)
    }

    pub fn domain_allowed(&self, name: &str) -> bool {
        let normalized = name.trim().trim_end_matches('.').to_ascii_lowercase();
        if !dns::is_valid_name(&normalized) {
            return false;
        }
        if self.allow_domains.is_empty() {
            return true;
        }
        self.allow_domains.iter().any(|domain| {
            let suffix = format!(".{}", domain);
            normalized == *domain || normalized.ends_with(suffix.as_str())
        })
    }

    pub fn cidr_matches(&self, ip: IpAddr) -> bool {
        self.cidrs.iter().any(|network| network.contains(ip))
    }

    pub fn pin(&self, ip: IpAddr) -> Result<()> {
        let mut pinned = self.guard()?;
        let now = Instant::now();
        pinned.entries.retain(|_, expiry| *expiry > now);
        if pinned.entries.len() >= MAX_PINS {
            let mut oldest: Option<String> = None;
            let mut oldest_expiry: Option<Instant> = None;
            for (key, expiry) in &pinned.entries {
                let replace = match oldest_expiry {
                    Some(current) => *expiry < current,
                    None => true,
                };
                if replace {
                    oldest = Some(key.clone());
                    oldest_expiry = Some(*expiry);
                }
            }
            if let Some(key) = oldest {
                pinned.entries.remove(&key);
            }
        }
        let expiry = now.checked_add(PIN_TTL).unwrap_or(now);
        pinned.entries.insert(ip.to_string(), expiry);
        Ok(())
    }

    pub fn is_pinned(&self, ip: IpAddr) -> bool {
        let key = ip.to_string();
        match self.pinned.lock() {
            Ok(pinned) => match pinned.entries.get(&key) {
                Some(expiry) => *expiry > Instant::now(),
                None => false,
            },
            Err(_) => false,
        }
    }

    pub fn no_net(&self) -> bool {
        self.no_net
    }

    pub fn net(&self) -> bool {
        self.net
    }

    pub fn loopback_only(&self) -> bool {
        !self.no_net && !self.net
    }

    pub fn restricted(&self) -> bool {
        !self.allow_domains.is_empty() || !self.cidrs.is_empty()
    }

    pub fn is_unrestricted(&self) -> bool {
        !self.restricted()
    }

    pub fn allow_domains(&self) -> &[String] {
        &self.allow_domains
    }

    pub fn cidrs(&self) -> &[IpCidr] {
        &self.cidrs
    }

    pub fn allow_ports(&self) -> &[u16] {
        &self.allow_ports
    }

    pub fn resolver(&self) -> &DnsResolver {
        &self.resolver
    }

    fn guard(&self) -> Result<MutexGuard<'_, Pinned>> {
        self.pinned
            .lock()
            .map_err(|_| Error::Message("egress pin table lock is poisoned".to_owned()))
    }
}
