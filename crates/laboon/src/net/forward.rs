use serde::{Deserialize, Serialize};
use std::collections::BTreeMap;
use std::fs;
use std::net::{IpAddr, Ipv4Addr, Ipv6Addr, SocketAddr};
use std::path::{Path, PathBuf};
use std::str::FromStr;

use crate::error::{Error, Result};
use crate::net::dns::{self, is_loopback_ip};
use crate::net::ports;
use crate::types::{LabelMap, PortProtocol, PublishedPort};

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub struct ForwardRule {
    pub host_ip: IpAddr,
    pub host_port: u16,
    pub host_port_end: Option<u16>,
    pub container_ip: IpAddr,
    pub container_port: u16,
    pub container_port_end: Option<u16>,
    pub protocol: PortProtocol,
    pub published: bool,
}

impl ForwardRule {
    pub fn from_published(port: &PublishedPort) -> Result<Self> {
        ports::validate_published_port(port)?;
        Ok(ForwardRule {
            host_ip: port.host_ip,
            host_port: port.host_port,
            host_port_end: port.host_port_end,
            container_ip: IpAddr::V4(Ipv4Addr::LOCALHOST),
            container_port: port.container_port,
            container_port_end: port.container_port_end,
            protocol: port.protocol,
            published: true,
        })
    }

    pub fn host_span(&self) -> u16 {
        self.host_port_end
            .unwrap_or(self.host_port)
            .saturating_sub(self.host_port)
    }

    pub fn container_span(&self) -> u16 {
        self.container_port_end
            .unwrap_or(self.container_port)
            .saturating_sub(self.container_port)
    }
}

#[derive(Debug, Clone, Default)]
pub struct Forwarder {
    rules: Vec<ForwardRule>,
}

impl Forwarder {
    pub fn new(rules: &[PublishedPort]) -> Result<Self> {
        let mut by_key: BTreeMap<(String, u16, u16, u8), ForwardRule> = BTreeMap::new();
        for port in rules {
            let rule = ForwardRule::from_published(port)?;
            validate_rule(&rule)?;
            let key = (
                rule.host_ip.to_string(),
                rule.host_port,
                rule.container_port,
                protocol_code(rule.protocol),
            );
            match by_key.get(&key) {
                Some(existing) if *existing != rule => {
                    return Err(Error::Config(format!(
                        "conflicting publish for {}:{}",
                        key.0, key.1
                    )))
                }
                Some(_) => continue,
                None => {
                    by_key.insert(key, rule);
                }
            }
        }
        Ok(Forwarder {
            rules: by_key.into_values().collect(),
        })
    }

    pub fn plan(&self) -> Vec<ForwardRule> {
        self.rules.clone()
    }

    pub fn rules(&self) -> &[ForwardRule] {
        &self.rules
    }

    pub fn check(&self, rule: &ForwardRule) -> Result<()> {
        validate_rule(rule)?;
        if !rule.published {
            return Err(Error::NetworkDenied(
                "forwarding to an unpublished rule".to_owned(),
            ));
        }
        if !is_loopback_ip(rule.container_ip) {
            return Err(Error::NetworkDenied(
                "forward target is not the guest loopback".to_owned(),
            ));
        }
        let matched = self.rules.iter().any(|published| {
            published.published
                && published.protocol == rule.protocol
                && published.host_port == rule.host_port
                && published.host_port_end == rule.host_port_end
                && published.container_ip == rule.container_ip
                && published.container_port == rule.container_port
                && published.container_port_end == rule.container_port_end
                && (published.host_ip == rule.host_ip || published.host_ip.is_unspecified())
        });
        if !matched {
            return Err(Error::NetworkDenied(format!(
                "host address {} port {} is not explicitly published",
                rule.host_ip, rule.host_port
            )));
        }
        Ok(())
    }

    pub fn endpoint(
        &self,
        host_ip: IpAddr,
        host_port: u16,
        protocol: PortProtocol,
    ) -> Result<SocketAddr> {
        for rule in &self.rules {
            if rule.protocol != protocol {
                continue;
            }
            if rule.host_ip != host_ip && !rule.host_ip.is_unspecified() {
                continue;
            }
            let last = rule.host_port_end.unwrap_or(rule.host_port);
            if host_port < rule.host_port || host_port > last {
                continue;
            }
            let offset = host_port - rule.host_port;
            let container_port = rule
                .container_port
                .checked_add(offset)
                .ok_or_else(|| Error::Config("published port range overflows".to_owned()))?;
            let container_last = rule.container_port_end.unwrap_or(rule.container_port);
            if container_port > container_last {
                continue;
            }
            return Ok(SocketAddr::new(rule.container_ip, container_port));
        }
        Err(Error::NetworkDenied(format!(
            "no published forward for {}:{}",
            host_ip, host_port
        )))
    }

    pub fn published_host_addresses(&self) -> Vec<IpAddr> {
        let mut out: Vec<IpAddr> = Vec::new();
        for rule in &self.rules {
            if !out.contains(&rule.host_ip) {
                out.push(rule.host_ip);
            }
        }
        out
    }

    pub fn hosts_file(
        &self,
        hostname: &str,
        extra: &LabelMap,
        ips: &[(String, IpAddr)],
    ) -> String {
        let mut entries: BTreeMap<String, Vec<IpAddr>> = BTreeMap::new();
        let loopback4 = IpAddr::V4(Ipv4Addr::LOCALHOST);
        let loopback6 = IpAddr::V6(Ipv6Addr::LOCALHOST);
        add_entry(&mut entries, "localhost", loopback4);
        add_entry(&mut entries, "localhost", loopback6);
        add_entry(&mut entries, "localhost.localdomain", loopback4);
        add_entry(&mut entries, "ip6-localhost", loopback6);
        add_entry(&mut entries, "ip6-loopback", loopback6);
        add_entry(&mut entries, hostname, loopback4);
        for (name, address) in extra {
            if let Ok(ip) = IpAddr::from_str(address.trim()) {
                add_entry(&mut entries, name, ip);
            }
        }
        for (name, ip) in ips {
            add_entry(&mut entries, name, *ip);
        }
        let mut lines: Vec<String> = vec![String::from("# /etc/hosts generated by laboon")];
        let mut by_address: BTreeMap<String, Vec<String>> = BTreeMap::new();
        for (name, addresses) in &entries {
            for ip in addresses {
                by_address
                    .entry(ip.to_string())
                    .or_default()
                    .push(name.clone());
            }
        }
        for (address, mut names) in by_address {
            names.sort();
            names.dedup();
            lines.push(format!("{} {}", address, names.join(" ")));
        }
        lines.push(String::new());
        lines.join("\n")
    }

    pub fn resolv_conf(&self, dns: &[String], search: &[String]) -> String {
        let mut lines = vec![String::from("# /etc/resolv.conf generated by laboon")];
        if search.is_empty() {
            lines.push(String::from("search ."));
        } else {
            lines.push(format!("search {}", search.join(" ")));
        }
        if dns.is_empty() {
            lines.push(String::from("nameserver 127.0.0.53"));
        } else {
            for server in dns {
                lines.push(format!("nameserver {server}"));
            }
        }
        lines.push(String::from("options ndots:0"));
        lines.push(String::new());
        lines.join("\n")
    }

    pub fn write_hosts(
        &self,
        root: &Path,
        hostname: &str,
        extra: &LabelMap,
        ips: &[(String, IpAddr)],
    ) -> Result<PathBuf> {
        let etc = root.join("etc");
        fs::create_dir_all(&etc)?;
        let target = etc.join("hosts");
        let temporary = etc.join("hosts.laboon.tmp");
        let contents = self.hosts_file(hostname, extra, ips);
        fs::write(&temporary, contents.as_bytes())?;
        match fs::rename(&temporary, &target) {
            Ok(()) => Ok(target),
            Err(err) => {
                let _ = fs::remove_file(&temporary);
                Err(Error::Io(err))
            }
        }
    }
}

fn validate_rule(rule: &ForwardRule) -> Result<()> {
    if rule.container_port == 0 {
        return Err(Error::Config("forward rule has no container port".to_owned()));
    }
    let host_last = rule.host_port_end.unwrap_or(rule.host_port);
    let container_last = rule.container_port_end.unwrap_or(rule.container_port);
    if host_last < rule.host_port || container_last < rule.container_port {
        return Err(Error::Config("forward rule range is reversed".to_owned()));
    }
    if rule.host_port == 0 && (rule.host_port_end.is_some() || rule.container_port_end.is_some()) {
        return Err(Error::Config(
            "ephemeral host port cannot carry a range".to_owned(),
        ));
    }
    if rule.host_span() != rule.container_span() {
        return Err(Error::Config(
            "forward rule ranges have different lengths".to_owned(),
        ));
    }
    Ok(())
}

fn add_entry(entries: &mut BTreeMap<String, Vec<IpAddr>>, name: &str, ip: IpAddr) {
    if !dns::is_valid_name(name) {
        return;
    }
    let key = match dns::normalize_name(name) {
        Ok(value) => value,
        Err(_) => return,
    };
    let addresses = entries.entry(key).or_default();
    if !addresses.contains(&ip) {
        addresses.push(ip);
    }
}

fn protocol_code(protocol: PortProtocol) -> u8 {
    match protocol {
        PortProtocol::Tcp => 6,
        PortProtocol::Udp => 17,
        PortProtocol::Sctp => 132,
    }
}
