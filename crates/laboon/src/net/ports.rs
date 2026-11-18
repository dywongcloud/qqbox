use std::net::{IpAddr, Ipv4Addr};
use std::str::FromStr;

use crate::error::{Error, Result};
use crate::types::{PortProtocol, PortSpec, PublishedPort};

pub fn parse_protocol(spec: &str) -> Result<PortProtocol> {
    match spec.to_ascii_lowercase().as_str() {
        "tcp" => Ok(PortProtocol::Tcp),
        "udp" => Ok(PortProtocol::Udp),
        "sctp" => Ok(PortProtocol::Sctp),
        other => Err(Error::Config(format!("unknown port protocol: {}", other))),
    }
}

pub fn protocol_name(protocol: PortProtocol) -> &'static str {
    match protocol {
        PortProtocol::Tcp => "tcp",
        PortProtocol::Udp => "udp",
        PortProtocol::Sctp => "sctp",
    }
}

pub fn parse_port_spec(spec: &str) -> Result<PortSpec> {
    let input = clean_input(spec)?;
    let (body, protocol) = split_protocol(input)?;
    let (port, range_end) = parse_range(body, "exposed port")?;
    Ok(PortSpec {
        port,
        range_end,
        protocol,
    })
}

pub fn parse_published_port(spec: &str) -> Result<PublishedPort> {
    let input = clean_input(spec)?;
    let (body, protocol) = split_protocol(input)?;
    let (host_ip, host_text, container_text) = split_binding(body)?;
    let (host_port, host_port_end) = if host_text.is_empty() {
        (0, None)
    } else {
        parse_range(host_text, "host port")?
    };
    let (container_port, container_port_end) = parse_range(container_text, "container port")?;
    let published = PublishedPort {
        host_ip,
        host_port,
        host_port_end,
        container_port,
        container_port_end,
        protocol,
    };
    validate_published_port(&published)?;
    Ok(published)
}

pub fn validate_published_port(port: &PublishedPort) -> Result<()> {
    if port.container_port == 0 {
        return Err(Error::Config(
            "published port has no container port".to_owned(),
        ));
    }
    let container_last = port.container_port_end.unwrap_or(port.container_port);
    let host_last = port.host_port_end.unwrap_or(port.host_port);
    if container_last < port.container_port || host_last < port.host_port {
        return Err(Error::Config("published port range is reversed".to_owned()));
    }
    if port.host_port == 0 && (port.host_port_end.is_some() || port.container_port_end.is_some()) {
        return Err(Error::Config(
            "ephemeral host port cannot have a range".to_owned(),
        ));
    }
    match (port.host_port_end, port.container_port_end) {
        (Some(host_end), Some(container_end)) => {
            if host_end - port.host_port != container_end - port.container_port {
                return Err(Error::Config(
                    "published port ranges have different lengths".to_owned(),
                ));
            }
        }
        (None, Some(_)) | (Some(_), None) => {
            return Err(Error::Config(
                "published port ranges must be paired".to_owned(),
            ))
        }
        (None, None) => {}
    }
    Ok(())
}

pub fn expand(port: &PublishedPort) -> Result<Vec<(u16, u16)>> {
    validate_published_port(port)?;
    let last = port.container_port_end.unwrap_or(port.container_port);
    let span = u32::from(last) - u32::from(port.container_port);
    let mut out = Vec::with_capacity(span as usize + 1);
    for offset in 0..=span {
        let container = u32::from(port.container_port) + offset;
        let host = if port.host_port == 0 {
            0
        } else {
            u32::from(port.host_port) + offset
        };
        out.push((host as u16, container as u16));
    }
    Ok(out)
}

pub fn render_published_port(port: &PublishedPort) -> String {
    let host_range = range_text(port.host_port, port.host_port_end);
    let container_range = range_text(port.container_port, port.container_port_end);
    let suffix = format!("/{}", protocol_name(port.protocol));
    if port.host_ip.is_unspecified() && port.host_port == 0 {
        return format!("{}{}", container_range, suffix);
    }
    let host_ip = match port.host_ip {
        IpAddr::V4(_) => port.host_ip.to_string(),
        IpAddr::V6(_) => format!("[{}]", port.host_ip),
    };
    format!("{}:{}:{}{}", host_ip, host_range, container_range, suffix)
}

fn clean_input(spec: &str) -> Result<&str> {
    if spec.is_empty()
        || spec
            .chars()
            .any(|character| character.is_control() || character.is_whitespace())
    {
        return Err(Error::Config(format!("invalid port spec: {}", spec)));
    }
    Ok(spec)
}

fn split_protocol(input: &str) -> Result<(&str, PortProtocol)> {
    let parts: Vec<&str> = input.split('/').collect();
    match parts.as_slice() {
        [body] => Ok((*body, PortProtocol::Tcp)),
        [body, protocol] => Ok((*body, parse_protocol(protocol)?)),
        _ => Err(Error::Config(format!("invalid port spec: {}", input))),
    }
}

fn parse_number(text: &str, what: &str) -> Result<u16> {
    if text.is_empty() || !text.bytes().all(|byte| byte.is_ascii_digit()) {
        return Err(Error::Config(format!("invalid {}: {}", what, text)));
    }
    text.parse::<u16>()
        .map_err(|_| Error::Config(format!("{} out of range: {}", what, text)))
}

fn parse_range(text: &str, what: &str) -> Result<(u16, Option<u16>)> {
    match text.split_once('-') {
        Some((first, last)) => {
            let start = parse_number(first, what)?;
            let end = parse_number(last, what)?;
            if start == 0 || end < start {
                return Err(Error::Config(format!("invalid {} range: {}", what, text)));
            }
            Ok((start, if end == start { None } else { Some(end) }))
        }
        None => {
            let port = parse_number(text, what)?;
            if port == 0 {
                return Err(Error::Config(format!("invalid {}: {}", what, text)));
            }
            Ok((port, None))
        }
    }
}

fn looks_like_port(text: &str) -> bool {
    !text.is_empty()
        && text
            .bytes()
            .all(|byte| byte.is_ascii_digit() || byte == b'-')
}

fn parse_host_ip(text: &str) -> Result<IpAddr> {
    if text.is_empty() {
        return Ok(IpAddr::V4(Ipv4Addr::UNSPECIFIED));
    }
    IpAddr::from_str(text)
        .map_err(|_| Error::Config(format!("invalid publish host address: {}", text)))
}

fn split_binding(body: &str) -> Result<(IpAddr, &str, &str)> {
    if body.is_empty() {
        return Err(Error::Config("published port has no binding".to_owned()));
    }
    let unspecified = IpAddr::V4(Ipv4Addr::UNSPECIFIED);
    if body.starts_with('[') {
        let close = match body.find(']') {
            Some(index) => index,
            None => {
                return Err(Error::Config(
                    "unterminated ipv6 publish address".to_owned(),
                ))
            }
        };
        let host_ip = parse_host_ip(&body[1..close])?;
        let rest = &body[close + 1..];
        if !rest.starts_with(':') {
            return Err(Error::Config(format!("invalid published port: {}", body)));
        }
        return split_host_container(&rest[1..], host_ip);
    }
    let colons = body.bytes().filter(|byte| *byte == b':').count();
    match colons {
        0 => Ok((unspecified, "", body)),
        1 => {
            let (left, right) = match body.split_once(':') {
                Some(pair) => pair,
                None => return Err(Error::Config(format!("invalid published port: {}", body))),
            };
            if looks_like_port(left) {
                Ok((unspecified, left, right))
            } else {
                Ok((parse_host_ip(left)?, "", right))
            }
        }
        _ => {
            let mut parts = body.rsplitn(3, ':');
            let container = parts
                .next()
                .ok_or_else(|| Error::Config(format!("invalid published port: {}", body)))?;
            let host = parts
                .next()
                .ok_or_else(|| Error::Config(format!("invalid published port: {}", body)))?;
            let address = parts
                .next()
                .ok_or_else(|| Error::Config(format!("invalid published port: {}", body)))?;
            let host_ip = parse_host_ip(
                address.trim_matches(|character| character == '[' || character == ']'),
            )?;
            Ok((host_ip, host, container))
        }
    }
}

fn split_host_container(rest: &str, host_ip: IpAddr) -> Result<(IpAddr, &str, &str)> {
    let colons = rest.bytes().filter(|byte| *byte == b':').count();
    match colons {
        0 => Ok((host_ip, "", rest)),
        1 => {
            let (host, container) = match rest.split_once(':') {
                Some(pair) => pair,
                None => return Err(Error::Config(format!("invalid published port: {}", rest))),
            };
            Ok((host_ip, host, container))
        }
        _ => Err(Error::Config(format!("invalid published port: {}", rest))),
    }
}

fn range_text(port: u16, end: Option<u16>) -> String {
    match end {
        Some(last) => format!("{}-{}", port, last),
        None => port.to_string(),
    }
}
