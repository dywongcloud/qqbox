use std::collections::BTreeMap;
use std::io::{Read, Write};
use std::net::{IpAddr, Ipv4Addr, Ipv6Addr, SocketAddr, TcpStream, ToSocketAddrs, UdpSocket};
use std::str::FromStr;
use std::time::Duration;

use crate::config::Config;
use crate::error::{Error, Result};
use crate::net::socket::Afi;

pub const CLASS_IN: u16 = 1;
pub const TYPE_A: u16 = 1;
pub const TYPE_CNAME: u16 = 5;
pub const TYPE_AAAA: u16 = 28;
pub const DNS_PORT: u16 = 53;

const MAX_ANSWERS: usize = 64;
const MAX_LABELS: usize = 127;
const MAX_NAME_LEN: usize = 253;
const QUERY_TIMEOUT: Duration = Duration::from_secs(3);

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum DnsCode {
    Success,
    FormatError,
    ServerFailure,
    NameError,
    NotImplemented,
    Refused,
    Other(u8),
}

impl DnsCode {
    pub fn from_u8(value: u8) -> Self {
        match value & 0x0f {
            0 => DnsCode::Success,
            1 => DnsCode::FormatError,
            2 => DnsCode::ServerFailure,
            3 => DnsCode::NameError,
            4 => DnsCode::NotImplemented,
            5 => DnsCode::Refused,
            other => DnsCode::Other(other),
        }
    }

    pub fn as_u8(self) -> u8 {
        match self {
            DnsCode::Success => 0,
            DnsCode::FormatError => 1,
            DnsCode::ServerFailure => 2,
            DnsCode::NameError => 3,
            DnsCode::NotImplemented => 4,
            DnsCode::Refused => 5,
            DnsCode::Other(value) => value & 0x0f,
        }
    }
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct DnsQuestion {
    pub name: String,
    pub qtype: u16,
    pub qclass: u16,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct DnsMessage {
    pub id: u16,
    pub recursion_desired: bool,
    pub question: DnsQuestion,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct DnsResponse {
    pub id: u16,
    pub code: DnsCode,
    pub truncated: bool,
    pub addresses: Vec<IpAddr>,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct IpCidr {
    network: IpAddr,
    prefix: u8,
}

impl IpCidr {
    pub fn parse(spec: &str) -> Result<Self> {
        let (address, prefix) = match spec.split_once('/') {
            Some(pair) => pair,
            None => (spec, ""),
        };
        let ip = IpAddr::from_str(address.trim())
            .map_err(|_| Error::Config(format!("invalid cidr address: {}", spec)))?;
        if prefix.is_empty() || !prefix.bytes().all(|byte| byte.is_ascii_digit()) {
            return Err(Error::Config(format!("invalid cidr prefix: {}", spec)));
        }
        let bits: u32 = prefix
            .parse()
            .map_err(|_| Error::Config(format!("invalid cidr prefix: {}", spec)))?;
        let width = match ip {
            IpAddr::V4(_) => 32u32,
            IpAddr::V6(_) => 128u32,
        };
        if bits > width {
            return Err(Error::Config(format!("cidr prefix out of range: {}", spec)));
        }
        let prefix = bits as u8;
        Ok(IpCidr {
            network: masked(ip, prefix),
            prefix,
        })
    }

    pub fn network(&self) -> IpAddr {
        self.network
    }

    pub fn prefix(&self) -> u8 {
        self.prefix
    }

    pub fn contains(&self, ip: IpAddr) -> bool {
        match (self.network, ip) {
            (IpAddr::V4(network), IpAddr::V4(candidate)) => {
                v4_mask(u32::from(network), self.prefix)
                    == v4_mask(u32::from(candidate), self.prefix)
            }
            (IpAddr::V6(network), IpAddr::V6(candidate)) => {
                v6_mask(u128::from(network), self.prefix)
                    == v6_mask(u128::from(candidate), self.prefix)
            }
            _ => false,
        }
    }
}

fn v4_mask(value: u32, prefix: u8) -> u32 {
    if prefix == 0 {
        0
    } else if prefix >= 32 {
        value
    } else {
        value & (u32::MAX << (32 - u32::from(prefix)))
    }
}

fn v6_mask(value: u128, prefix: u8) -> u128 {
    if prefix == 0 {
        0
    } else if prefix >= 128 {
        value
    } else {
        value & (u128::MAX << (128 - u32::from(prefix)))
    }
}

fn masked(ip: IpAddr, prefix: u8) -> IpAddr {
    match ip {
        IpAddr::V4(value) => IpAddr::V4(Ipv4Addr::from(v4_mask(u32::from(value), prefix))),
        IpAddr::V6(value) => IpAddr::V6(Ipv6Addr::from(v6_mask(u128::from(value), prefix))),
    }
}

pub fn is_loopback_ip(ip: IpAddr) -> bool {
    match ip {
        IpAddr::V4(value) => value.octets()[0] == 127,
        IpAddr::V6(value) => {
            let octets = value.octets();
            value.is_loopback() || (is_v4_mapped(&octets) && octets[12] == 127)
        }
    }
}

pub fn is_unspecified_ip(ip: IpAddr) -> bool {
    match ip {
        IpAddr::V4(value) => value.is_unspecified(),
        IpAddr::V6(value) => value.is_unspecified(),
    }
}

fn is_v4_mapped(octets: &[u8; 16]) -> bool {
    octets[0..10].iter().all(|byte| *byte == 0) && octets[10] == 0xff && octets[11] == 0xff
}

pub fn is_internal(ip: IpAddr) -> bool {
    match ip {
        IpAddr::V4(value) => internal_v4(value),
        IpAddr::V6(value) => internal_v6(value),
    }
}

fn internal_v4(value: Ipv4Addr) -> bool {
    let o = value.octets();
    let private =
        o[0] == 10 || (o[0] == 172 && (16..=31).contains(&o[1])) || (o[0] == 192 && o[1] == 168);
    let loopback = o[0] == 127;
    let link_local = o[0] == 169 && o[1] == 254;
    let multicast = (224..=239).contains(&o[0]);
    let link_local_multicast = o[0] == 224 && o[1] == 0 && o[2] == 0;
    let broadcast = o == [255, 255, 255, 255];
    o[0] == 0 || private || loopback || link_local || multicast || link_local_multicast || broadcast
}

fn internal_v6(value: Ipv6Addr) -> bool {
    let o = value.octets();
    if is_v4_mapped(&o) {
        return internal_v4(Ipv4Addr::new(o[12], o[13], o[14], o[15]));
    }
    let unspecified = o.iter().all(|byte| *byte == 0);
    let loopback = o[0..15].iter().all(|byte| *byte == 0) && o[15] == 1;
    let unique_local = (o[0] & 0xfe) == 0xfc;
    let link_local = o[0] == 0xfe && (o[1] & 0xc0) == 0x80;
    let multicast = o[0] == 0xff;
    unspecified || loopback || unique_local || link_local || multicast
}

pub fn is_valid_label(label: &str) -> bool {
    let bytes = label.as_bytes();
    if bytes.is_empty() || bytes.len() > 63 {
        return false;
    }
    if !bytes[0].is_ascii_alphanumeric() || !bytes[bytes.len() - 1].is_ascii_alphanumeric() {
        return false;
    }
    bytes
        .iter()
        .all(|byte| byte.is_ascii_alphanumeric() || *byte == b'-' || *byte == b'_')
}

pub fn is_valid_name(name: &str) -> bool {
    let trimmed = match name.strip_suffix('.') {
        Some(value) => value,
        None => name,
    };
    if trimmed.is_empty() || trimmed.len() > MAX_NAME_LEN || trimmed.contains("..") {
        return false;
    }
    let mut labels = 0usize;
    let mut wire_len = 0usize;
    for label in trimmed.split('.') {
        if !is_valid_label(label) {
            return false;
        }
        labels += 1;
        wire_len += label.len() + 1;
    }
    labels <= MAX_LABELS && wire_len <= MAX_NAME_LEN + 1
}

pub fn validate_name(name: &str) -> Result<()> {
    if is_valid_name(name) {
        Ok(())
    } else {
        Err(Error::NetworkDenied(format!("invalid dns name: {}", name)))
    }
}

pub fn normalize_name(name: &str) -> Result<String> {
    validate_name(name)?;
    let trimmed = match name.strip_suffix('.') {
        Some(value) => value,
        None => name,
    };
    Ok(trimmed.to_ascii_lowercase())
}

fn push_u16(out: &mut Vec<u8>, value: u16) {
    out.extend_from_slice(&value.to_be_bytes());
}

fn push_u32(out: &mut Vec<u8>, value: u32) {
    out.extend_from_slice(&value.to_be_bytes());
}

fn read_u16(bytes: &[u8], cursor: &mut usize) -> Result<u16> {
    let high = *bytes
        .get(*cursor)
        .ok_or_else(|| Error::Message("truncated dns message".to_owned()))?;
    let low = *bytes
        .get(*cursor + 1)
        .ok_or_else(|| Error::Message("truncated dns message".to_owned()))?;
    *cursor += 2;
    Ok(u16::from_be_bytes([high, low]))
}

fn read_u32(bytes: &[u8], cursor: &mut usize) -> Result<u32> {
    let high = read_u16(bytes, cursor)?;
    let low = read_u16(bytes, cursor)?;
    Ok((u32::from(high) << 16) | u32::from(low))
}

pub fn encode_name(name: &str) -> Result<Vec<u8>> {
    let normalized = normalize_name(name)?;
    let mut out = Vec::with_capacity(normalized.len() + 2);
    for label in normalized.split('.') {
        out.push(label.len() as u8);
        out.extend_from_slice(label.as_bytes());
    }
    out.push(0);
    Ok(out)
}

fn decode_name(bytes: &[u8], cursor: &mut usize) -> Result<String> {
    let mut at = *cursor;
    let mut end = *cursor;
    let mut jumped = false;
    let mut jumps = 0usize;
    let mut labels: Vec<String> = Vec::new();
    loop {
        if at >= bytes.len() {
            return Err(Error::Message("truncated dns name".to_owned()));
        }
        let length = bytes[at];
        if length & 0xc0 == 0xc0 {
            if at + 1 >= bytes.len() {
                return Err(Error::Message(
                    "truncated dns compression pointer".to_owned(),
                ));
            }
            let target = ((usize::from(length) & 0x3f) << 8) | usize::from(bytes[at + 1]);
            if target >= at {
                return Err(Error::Message("cyclic dns compression pointer".to_owned()));
            }
            jumps += 1;
            if jumps > 32 {
                return Err(Error::Message(
                    "too many dns compression pointers".to_owned(),
                ));
            }
            if !jumped {
                end = at + 2;
                jumped = true;
            }
            at = target;
            continue;
        }
        if length & 0xc0 != 0 {
            return Err(Error::Message("reserved dns label length".to_owned()));
        }
        if length == 0 {
            if !jumped {
                end = at + 1;
            }
            break;
        }
        let start = at + 1;
        let stop = start + usize::from(length);
        if stop > bytes.len() {
            return Err(Error::Message("truncated dns label".to_owned()));
        }
        let label = std::str::from_utf8(&bytes[start..stop])
            .map_err(|_| Error::Message("non-utf8 dns label".to_owned()))?;
        if !is_valid_label(label) {
            return Err(Error::Message("invalid dns label".to_owned()));
        }
        labels.push(label.to_ascii_lowercase());
        if labels.len() > MAX_LABELS {
            return Err(Error::Message("dns name has too many labels".to_owned()));
        }
        at = stop;
    }
    *cursor = end;
    let joined = labels.join(".");
    if !is_valid_name(&joined) {
        return Err(Error::Message("invalid dns name".to_owned()));
    }
    Ok(joined)
}

#[derive(Debug, Clone, Copy)]
struct Header {
    id: u16,
    response: bool,
    truncated: bool,
    recursion_desired: bool,
    #[allow(dead_code)]
    recursion_available: bool,
    code: DnsCode,
    question_count: u16,
    answer_count: u16,
}

fn parse_header(bytes: &[u8]) -> Result<Header> {
    if bytes.len() < 12 {
        return Err(Error::Message(
            "dns message is shorter than its header".to_owned(),
        ));
    }
    let id = u16::from_be_bytes([bytes[0], bytes[1]]);
    let flags = u16::from_be_bytes([bytes[2], bytes[3]]);
    let question_count = u16::from_be_bytes([bytes[4], bytes[5]]);
    let answer_count = u16::from_be_bytes([bytes[6], bytes[7]]);
    Ok(Header {
        id,
        response: flags & 0x8000 != 0,
        truncated: flags & 0x0200 != 0,
        recursion_desired: flags & 0x0100 != 0,
        recursion_available: flags & 0x0080 != 0,
        code: DnsCode::from_u8((flags & 0x000f) as u8),
        question_count,
        answer_count,
    })
}

pub fn parse_query(bytes: &[u8]) -> Result<DnsMessage> {
    let header = parse_header(bytes)?;
    if header.response {
        return Err(Error::Message(
            "dns query has the response bit set".to_owned(),
        ));
    }
    if header.question_count == 0 {
        return Err(Error::Message("dns query has no question".to_owned()));
    }
    let mut cursor = 12usize;
    let name = decode_name(bytes, &mut cursor)?;
    let qtype = read_u16(bytes, &mut cursor)?;
    let qclass = read_u16(bytes, &mut cursor)?;
    Ok(DnsMessage {
        id: header.id,
        recursion_desired: header.recursion_desired,
        question: DnsQuestion {
            name,
            qtype,
            qclass,
        },
    })
}

pub fn reply(query: &DnsMessage, code: DnsCode, ttl: u32, addresses: &[IpAddr]) -> Result<Vec<u8>> {
    let wanted = match query.question.qtype {
        TYPE_A => Some(false),
        TYPE_AAAA => Some(true),
        _ => None,
    };
    let mut matched: Vec<IpAddr> = Vec::new();
    if let Some(ipv6) = wanted {
        for address in addresses {
            if address.is_ipv6() == ipv6 && !matched.contains(address) {
                matched.push(*address);
                if matched.len() >= MAX_ANSWERS {
                    break;
                }
            }
        }
    }
    let mut out = Vec::with_capacity(64 + matched.len() * 20);
    push_u16(&mut out, query.id);
    let mut flags: u16 = 0x8000 | 0x0400 | 0x0080 | u16::from(code.as_u8());
    if query.recursion_desired {
        flags |= 0x0100;
    }
    push_u16(&mut out, flags);
    push_u16(&mut out, 1);
    push_u16(&mut out, matched.len() as u16);
    push_u16(&mut out, 0);
    push_u16(&mut out, 0);
    out.extend_from_slice(&encode_name(&query.question.name)?);
    push_u16(&mut out, query.question.qtype);
    push_u16(&mut out, query.question.qclass);
    for address in &matched {
        push_u16(&mut out, 0xc00c);
        match address {
            IpAddr::V4(value) => {
                push_u16(&mut out, TYPE_A);
                push_u16(&mut out, CLASS_IN);
                push_u32(&mut out, ttl);
                push_u16(&mut out, 4);
                out.extend_from_slice(&value.octets());
            }
            IpAddr::V6(value) => {
                push_u16(&mut out, TYPE_AAAA);
                push_u16(&mut out, CLASS_IN);
                push_u32(&mut out, ttl);
                push_u16(&mut out, 16);
                out.extend_from_slice(&value.octets());
            }
        }
    }
    Ok(out)
}

fn query_id(name: &str, qtype: u16) -> u16 {
    let qtype_bytes = qtype.to_be_bytes();
    let mut hash: u32 = 0x811c9dc5;
    for byte in name.as_bytes().iter().chain(qtype_bytes.iter()) {
        hash ^= u32::from(*byte);
        hash = hash.wrapping_mul(0x01000193);
    }
    (hash & 0xffff) as u16
}

pub fn build_query(name: &str, qtype: u16) -> Result<Vec<u8>> {
    let mut out = Vec::with_capacity(64);
    push_u16(&mut out, query_id(name, qtype));
    push_u16(&mut out, 0x0100);
    push_u16(&mut out, 1);
    push_u16(&mut out, 0);
    push_u16(&mut out, 0);
    push_u16(&mut out, 0);
    out.extend_from_slice(&encode_name(name)?);
    push_u16(&mut out, qtype);
    push_u16(&mut out, CLASS_IN);
    Ok(out)
}

pub fn parse_response(bytes: &[u8]) -> Result<DnsResponse> {
    let header = parse_header(bytes)?;
    if !header.response {
        return Err(Error::Message(
            "dns response has the query bit set".to_owned(),
        ));
    }
    let mut cursor = 12usize;
    let question_count = usize::from(header.question_count).min(MAX_ANSWERS);
    for _ in 0..question_count {
        decode_name(bytes, &mut cursor)?;
        read_u16(bytes, &mut cursor)?;
        read_u16(bytes, &mut cursor)?;
    }
    let mut addresses = Vec::new();
    let answer_count = usize::from(header.answer_count).min(MAX_ANSWERS);
    for _ in 0..answer_count {
        decode_name(bytes, &mut cursor)?;
        let rrtype = read_u16(bytes, &mut cursor)?;
        let class = read_u16(bytes, &mut cursor)?;
        read_u32(bytes, &mut cursor)?;
        let length = usize::from(read_u16(bytes, &mut cursor)?);
        let stop = cursor
            .checked_add(length)
            .ok_or_else(|| Error::Message("dns record length overflow".to_owned()))?;
        let rdata = bytes
            .get(cursor..stop)
            .ok_or_else(|| Error::Message("truncated dns record".to_owned()))?;
        if class == CLASS_IN {
            if rrtype == TYPE_A && length == 4 {
                addresses.push(IpAddr::V4(Ipv4Addr::new(
                    rdata[0], rdata[1], rdata[2], rdata[3],
                )));
            } else if rrtype == TYPE_AAAA && length == 16 {
                let mut octets = [0u8; 16];
                octets.copy_from_slice(rdata);
                addresses.push(IpAddr::V6(Ipv6Addr::from(octets)));
            } else if rrtype == TYPE_CNAME {
                continue;
            }
        }
        cursor = stop;
    }
    Ok(DnsResponse {
        id: header.id,
        code: header.code,
        truncated: header.truncated,
        addresses,
    })
}

#[derive(Debug, Clone, Default)]
pub struct DnsResolver {
    records: BTreeMap<String, Vec<IpAddr>>,
    servers: Vec<SocketAddr>,
    host_resolver: bool,
}

impl DnsResolver {
    pub fn new() -> Self {
        let mut resolver = DnsResolver {
            records: BTreeMap::new(),
            servers: Vec::new(),
            host_resolver: true,
        };
        resolver.add("localhost", IpAddr::V4(Ipv4Addr::LOCALHOST));
        resolver.add("localhost", IpAddr::V6(Ipv6Addr::LOCALHOST));
        resolver.add("ip6-localhost", IpAddr::V6(Ipv6Addr::LOCALHOST));
        resolver.add("ip6-loopback", IpAddr::V6(Ipv6Addr::LOCALHOST));
        resolver
    }

    pub fn from_config(config: &Config) -> Self {
        let mut resolver = DnsResolver::new();
        if !config.hostname.is_empty() && is_valid_name(&config.hostname) {
            let name = normalize_name(&config.hostname)
                .unwrap_or_else(|_| config.hostname.to_ascii_lowercase());
            resolver.add(&name, IpAddr::V4(Ipv4Addr::LOCALHOST));
        }
        for (name, address) in &config.extra_hosts {
            if !is_valid_name(name) {
                continue;
            }
            if let Ok(ip) = IpAddr::from_str(address.trim()) {
                let key = normalize_name(name).unwrap_or_else(|_| name.to_ascii_lowercase());
                resolver.add(&key, ip);
            }
        }
        for server in &config.dns {
            if let Some(address) = parse_server(server) {
                if !resolver.servers.contains(&address) {
                    resolver.servers.push(address);
                }
            }
        }
        resolver.host_resolver = resolver.servers.is_empty();
        resolver
    }

    pub fn with_record(mut self, name: &str, ip: IpAddr) -> Self {
        if is_valid_name(name) {
            let key = normalize_name(name).unwrap_or_else(|_| name.to_ascii_lowercase());
            self.add(&key, ip);
        }
        self
    }

    pub fn with_server(mut self, address: SocketAddr) -> Self {
        if !self.servers.contains(&address) {
            self.servers.push(address);
        }
        self.host_resolver = false;
        self
    }

    pub fn without_host_resolver(mut self) -> Self {
        self.host_resolver = false;
        self
    }

    pub fn records(&self) -> &BTreeMap<String, Vec<IpAddr>> {
        &self.records
    }

    pub fn servers(&self) -> &[SocketAddr] {
        &self.servers
    }

    pub fn lookup_local(&self, name: &str, family: Afi) -> Result<Vec<IpAddr>> {
        let normalized = normalize_name(name)?;
        let literal = match IpAddr::from_str(&normalized) {
            Ok(ip) if family_matches(ip, family) => vec![ip],
            Ok(_) => Vec::new(),
            Err(_) => Vec::new(),
        };
        if !literal.is_empty() {
            return Ok(literal);
        }
        match self.records.get(&normalized) {
            Some(entries) => {
                let out: Vec<IpAddr> = entries
                    .iter()
                    .copied()
                    .filter(|ip| family_matches(*ip, family))
                    .collect();
                if out.is_empty() {
                    Err(Error::NetworkDenied(format!(
                        "no {} address for {}",
                        family_name(family),
                        normalized
                    )))
                } else {
                    Ok(out)
                }
            }
            None => Err(Error::NetworkDenied(format!(
                "{} is not in the supervisor host table",
                normalized
            ))),
        }
    }

    pub fn lookup(&self, name: &str, family: Afi) -> Result<Vec<IpAddr>> {
        if let Ok(local) = self.lookup_local(name, family) {
            return Ok(local);
        }
        if !self.servers.is_empty() {
            return self.query_servers(name, family);
        }
        if self.host_resolver {
            return self.host_lookup(name, family);
        }
        Err(Error::NetworkDenied(format!(
            "no dns source configured for {}",
            name
        )))
    }

    fn add(&mut self, name: &str, ip: IpAddr) {
        let entries = self.records.entry(name.to_ascii_lowercase()).or_default();
        if !entries.contains(&ip) {
            entries.push(ip);
        }
    }

    fn query_servers(&self, name: &str, family: Afi) -> Result<Vec<IpAddr>> {
        let qtype = match family {
            Afi::Inet6 => TYPE_AAAA,
            _ => TYPE_A,
        };
        let normalized = normalize_name(name)?;
        let query = build_query(&normalized, qtype)?;
        let expected = query_id(&normalized, qtype);
        for server in &self.servers {
            let wire = match udp_exchange(*server, &query) {
                Ok(bytes) => bytes,
                Err(_) => continue,
            };
            let mut response = match parse_response(&wire) {
                Ok(value) if value.id == expected => value,
                Ok(_) => continue,
                Err(_) => continue,
            };
            if response.truncated {
                if let Ok(bytes) = tcp_exchange(*server, &query) {
                    if let Ok(value) = parse_response(&bytes) {
                        if value.id == expected {
                            response = value;
                        }
                    }
                }
            }
            if response.code == DnsCode::Success {
                let out: Vec<IpAddr> = response
                    .addresses
                    .into_iter()
                    .filter(|ip| family_matches(*ip, family))
                    .collect();
                if !out.is_empty() {
                    return Ok(out);
                }
            }
        }
        Err(Error::NetworkDenied(format!(
            "dns lookup failed for {}",
            normalized
        )))
    }

    fn host_lookup(&self, name: &str, family: Afi) -> Result<Vec<IpAddr>> {
        let normalized = normalize_name(name)?;
        let resolved = (normalized.as_str(), 0u16)
            .to_socket_addrs()
            .map_err(|err| {
                Error::NetworkDenied(format!("dns lookup failed for {}: {}", normalized, err))
            })?;
        let mut out = Vec::new();
        for address in resolved {
            let ip = address.ip();
            if family_matches(ip, family) && !out.contains(&ip) {
                out.push(ip);
                if out.len() >= MAX_ANSWERS {
                    break;
                }
            }
        }
        if out.is_empty() {
            Err(Error::NetworkDenied(format!(
                "no {} address for {}",
                family_name(family),
                normalized
            )))
        } else {
            Ok(out)
        }
    }
}

fn family_matches(ip: IpAddr, family: Afi) -> bool {
    match family {
        Afi::Inet => ip.is_ipv4(),
        Afi::Inet6 => ip.is_ipv6(),
        _ => true,
    }
}

fn family_name(family: Afi) -> &'static str {
    match family {
        Afi::Inet6 => "ipv6",
        _ => "ipv4",
    }
}

fn parse_server(spec: &str) -> Option<SocketAddr> {
    let trimmed = spec.trim();
    if let Ok(address) = SocketAddr::from_str(trimmed) {
        return Some(address);
    }
    if let Ok(ip) = IpAddr::from_str(trimmed) {
        return Some(SocketAddr::new(ip, DNS_PORT));
    }
    None
}

fn udp_exchange(server: SocketAddr, query: &[u8]) -> Result<Vec<u8>> {
    let local = if server.is_ipv4() {
        "0.0.0.0:0"
    } else {
        "[::]:0"
    };
    let socket = UdpSocket::bind(local)?;
    socket.set_read_timeout(Some(QUERY_TIMEOUT))?;
    socket.set_write_timeout(Some(QUERY_TIMEOUT))?;
    socket.send_to(query, server)?;
    let mut buffer = [0u8; 4096];
    let (size, _) = socket.recv_from(&mut buffer)?;
    Ok(buffer[..size].to_vec())
}

fn tcp_exchange(server: SocketAddr, query: &[u8]) -> Result<Vec<u8>> {
    let stream = TcpStream::connect_timeout(&server, QUERY_TIMEOUT)?;
    stream.set_read_timeout(Some(QUERY_TIMEOUT))?;
    stream.set_write_timeout(Some(QUERY_TIMEOUT))?;
    let mut framed = Vec::with_capacity(query.len() + 2);
    framed.extend_from_slice(&(query.len() as u16).to_be_bytes());
    framed.extend_from_slice(query);
    let mut writer = &stream;
    writer.write_all(&framed)?;
    let mut reader = &stream;
    let mut length = [0u8; 2];
    reader.read_exact(&mut length)?;
    let size = usize::from(u16::from_be_bytes(length));
    if size < 12 {
        return Err(Error::Message(
            "dns tcp reply is shorter than its header".to_owned(),
        ));
    }
    let mut body = vec![0u8; size];
    reader.read_exact(&mut body)?;
    Ok(body)
}
