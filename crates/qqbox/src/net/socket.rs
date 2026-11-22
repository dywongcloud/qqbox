use std::collections::{BTreeMap, VecDeque};
use std::net::{IpAddr, Ipv4Addr, Ipv6Addr, SocketAddr};
use std::sync::{Mutex, MutexGuard};

use crate::error::{Error, Result};
use crate::net::dns::{is_loopback_ip, is_unspecified_ip};

const MAX_QUEUE: usize = 1024;
const MAX_DATAGRAM: usize = 65535;
const EPHEMERAL_FIRST: u16 = 32768;
const EPHEMERAL_LAST: u16 = 60999;

#[derive(Debug, Clone, Copy, PartialEq, Eq, Hash, Default)]
pub enum Afi {
    #[default]
    Inet,
    Inet6,
    Unix,
    Netlink,
    Packet,
    Other(u16),
}

impl Afi {
    pub fn from_linux(value: u64) -> Self {
        match value {
            1 => Afi::Unix,
            2 => Afi::Inet,
            10 => Afi::Inet6,
            16 => Afi::Netlink,
            17 => Afi::Packet,
            other => Afi::Other(u16::try_from(other).unwrap_or(u16::MAX)),
        }
    }

    pub fn as_linux(self) -> u64 {
        match self {
            Afi::Unix => 1,
            Afi::Inet => 2,
            Afi::Inet6 => 10,
            Afi::Netlink => 16,
            Afi::Packet => 17,
            Afi::Other(value) => u64::from(value),
        }
    }

    pub fn is_inet(self) -> bool {
        matches!(self, Afi::Inet | Afi::Inet6)
    }

    pub fn code(self) -> u8 {
        match self {
            Afi::Unix => 1,
            Afi::Inet => 2,
            Afi::Inet6 => 10,
            Afi::Netlink => 16,
            Afi::Packet => 17,
            Afi::Other(_) => 255,
        }
    }

    pub fn loopback_ip(self) -> IpAddr {
        match self {
            Afi::Inet6 => IpAddr::V6(Ipv6Addr::LOCALHOST),
            _ => IpAddr::V4(Ipv4Addr::LOCALHOST),
        }
    }
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Hash, Default)]
pub enum SockKind {
    #[default]
    Stream,
    Datagram,
    Raw,
    SeqPacket,
    Other(u16),
}

impl SockKind {
    pub fn from_linux(value: u64) -> Self {
        match value & 0x0f {
            1 => SockKind::Stream,
            2 => SockKind::Datagram,
            3 => SockKind::Raw,
            5 => SockKind::SeqPacket,
            other => SockKind::Other(u16::try_from(other).unwrap_or(u16::MAX)),
        }
    }

    pub fn as_linux(self) -> u64 {
        match self {
            SockKind::Stream => 1,
            SockKind::Datagram => 2,
            SockKind::Raw => 3,
            SockKind::SeqPacket => 5,
            SockKind::Other(value) => u64::from(value),
        }
    }

    pub fn is_stream(self) -> bool {
        matches!(self, SockKind::Stream | SockKind::SeqPacket)
    }

    pub fn code(self) -> u8 {
        match self {
            SockKind::Stream => 1,
            SockKind::Datagram => 2,
            SockKind::Raw => 3,
            SockKind::SeqPacket => 5,
            SockKind::Other(_) => 255,
        }
    }
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Default)]
pub enum SocketState {
    #[default]
    Created,
    Bound,
    Listening,
    Connected,
    Closed,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Socket {
    pub fd: u64,
    pub family: Afi,
    pub kind: SockKind,
    pub protocol: u16,
    pub state: SocketState,
    pub local: Option<SocketAddr>,
    pub peer: Option<SocketAddr>,
    pub backlog: usize,
}

impl Socket {
    pub fn new(fd: u64, family: Afi, kind: SockKind, protocol: u16) -> Self {
        Socket {
            fd,
            family,
            kind,
            protocol,
            state: SocketState::Created,
            local: None,
            peer: None,
            backlog: 0,
        }
    }

    pub fn is_open(&self) -> bool {
        self.state != SocketState::Closed
    }

    pub fn local_addr(&self) -> Option<SocketAddr> {
        self.local
    }

    pub fn peer_addr(&self) -> Option<SocketAddr> {
        self.peer
    }

    pub fn bind_addr(&mut self, addr: SocketAddr) -> Result<()> {
        if self.state != SocketState::Created {
            return Err(Error::Message(
                "socket is already bound or connected".to_owned(),
            ));
        }
        if !self.family.is_inet() {
            return Err(Error::Message(
                "unix sockets are served by the vfs".to_owned(),
            ));
        }
        if !is_guest_loopback(&addr) {
            return Err(Error::NetworkDenied(
                "guest sockets bind only on the guest loopback".to_owned(),
            ));
        }
        if !family_matches(self.family, addr.ip()) {
            return Err(Error::Message(
                "socket family does not match the bind address".to_owned(),
            ));
        }
        self.local = Some(addr);
        self.state = SocketState::Bound;
        Ok(())
    }

    pub fn listen_on(&mut self, backlog: usize) -> Result<()> {
        if self.state != SocketState::Bound {
            return Err(Error::Message(
                "socket must be bound before listening".to_owned(),
            ));
        }
        if !self.kind.is_stream() {
            return Err(Error::Message("only stream sockets listen".to_owned()));
        }
        self.backlog = backlog.min(MAX_QUEUE);
        self.state = SocketState::Listening;
        Ok(())
    }

    pub fn connect_to(&mut self, addr: SocketAddr) -> Result<()> {
        if self.state != SocketState::Created && self.state != SocketState::Bound {
            return Err(Error::Message(
                "socket cannot be connected in its current state".to_owned(),
            ));
        }
        if !is_guest_loopback(&addr) {
            return Err(Error::NetworkDenied(
                "guest sockets connect only on the guest loopback".to_owned(),
            ));
        }
        self.peer = Some(addr);
        self.state = SocketState::Connected;
        Ok(())
    }

    pub fn accepted(fd: u64, listener: &Socket, peer: SocketAddr) -> Result<Self> {
        if listener.state != SocketState::Listening {
            return Err(Error::Message(
                "accept requires a listening socket".to_owned(),
            ));
        }
        let mut socket = Socket::new(fd, listener.family, listener.kind, listener.protocol);
        socket.local = listener.local;
        socket.peer = Some(peer);
        socket.state = SocketState::Connected;
        Ok(socket)
    }

    pub fn close(&mut self) {
        self.state = SocketState::Closed;
        self.peer = None;
    }
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Message {
    pub from: Option<SocketAddr>,
    pub data: Vec<u8>,
}

#[derive(Debug, Default)]
struct TableInner {
    next_fd: u64,
    sockets: BTreeMap<u64, Socket>,
    bound: BTreeMap<(u16, u8, u8), u64>,
    links: BTreeMap<u64, u64>,
    pending: BTreeMap<u64, VecDeque<u64>>,
    queues: BTreeMap<u64, VecDeque<Message>>,
}

#[derive(Debug, Default)]
pub struct SocketTable {
    inner: Mutex<TableInner>,
}

impl SocketTable {
    pub fn new() -> Self {
        SocketTable::default()
    }

    fn guard(&self) -> Result<MutexGuard<'_, TableInner>> {
        self.inner
            .lock()
            .map_err(|_| Error::Message("socket table lock is poisoned".to_owned()))
    }

    pub fn create(&self, family: Afi, kind: SockKind, protocol: u16) -> Result<u64> {
        if family.is_inet() && kind == SockKind::Raw {
            return Err(Error::NetworkDenied(
                "raw inet sockets bypass the guest network policy".to_owned(),
            ));
        }
        let mut table = self.guard()?;
        if table.next_fd < 3 {
            table.next_fd = 3;
        }
        let fd = table.next_fd;
        table.next_fd = fd
            .checked_add(1)
            .ok_or_else(|| Error::Message("socket descriptor space exhausted".to_owned()))?;
        table
            .sockets
            .insert(fd, Socket::new(fd, family, kind, protocol));
        Ok(fd)
    }

    pub fn get(&self, fd: u64) -> Result<Socket> {
        let table = self.guard()?;
        table
            .sockets
            .get(&fd)
            .cloned()
            .ok_or_else(|| Error::NotFound(format!("socket {}", fd)))
    }

    pub fn len(&self) -> Result<usize> {
        let table = self.guard()?;
        Ok(table.sockets.len())
    }

    pub fn is_empty(&self) -> Result<bool> {
        Ok(self.len()? == 0)
    }

    pub fn bind(&self, fd: u64, addr: SocketAddr) -> Result<SocketAddr> {
        let mut table = self.guard()?;
        let current = table
            .sockets
            .get(&fd)
            .cloned()
            .ok_or_else(|| Error::NotFound(format!("socket {}", fd)))?;
        if current.state != SocketState::Created {
            return Err(Error::Message(
                "socket is already bound or connected".to_owned(),
            ));
        }
        if !current.family.is_inet() {
            return Err(Error::Message(
                "unix sockets are served by the vfs".to_owned(),
            ));
        }
        if !is_guest_loopback(&addr) {
            return Err(Error::NetworkDenied(
                "guest sockets bind only on the guest loopback".to_owned(),
            ));
        }
        if !family_matches(current.family, addr.ip()) {
            return Err(Error::Message(
                "socket family does not match the bind address".to_owned(),
            ));
        }
        let port = if addr.port() == 0 {
            ephemeral_port(&table, current.family, current.kind)?
        } else {
            addr.port()
        };
        let key = (port, current.family.code(), current.kind.code());
        if table.bound.contains_key(&key) {
            return Err(Error::Message(format!(
                "guest port {} is already bound",
                port
            )));
        }
        let ip = if is_unspecified_ip(addr.ip()) {
            current.family.loopback_ip()
        } else {
            addr.ip()
        };
        let bound_addr = SocketAddr::new(ip, port);
        {
            let socket = table
                .sockets
                .get_mut(&fd)
                .ok_or_else(|| Error::NotFound(format!("socket {}", fd)))?;
            socket.local = Some(bound_addr);
            socket.state = SocketState::Bound;
        }
        table.bound.insert(key, fd);
        Ok(bound_addr)
    }

    pub fn listen(&self, fd: u64, backlog: usize) -> Result<()> {
        let mut table = self.guard()?;
        let socket = table
            .sockets
            .get_mut(&fd)
            .ok_or_else(|| Error::NotFound(format!("socket {}", fd)))?;
        if socket.state != SocketState::Bound {
            return Err(Error::Message(
                "socket must be bound before listening".to_owned(),
            ));
        }
        if !socket.kind.is_stream() {
            return Err(Error::Message("only stream sockets listen".to_owned()));
        }
        socket.backlog = backlog.min(MAX_QUEUE);
        socket.state = SocketState::Listening;
        Ok(())
    }

    pub fn connect(&self, fd: u64, addr: SocketAddr) -> Result<()> {
        let mut table = self.guard()?;
        let current = table
            .sockets
            .get(&fd)
            .cloned()
            .ok_or_else(|| Error::NotFound(format!("socket {}", fd)))?;
        if current.state != SocketState::Created && current.state != SocketState::Bound {
            return Err(Error::Message(
                "socket cannot be connected in its current state".to_owned(),
            ));
        }
        if !is_guest_loopback(&addr) {
            return Err(Error::NetworkDenied(
                "guest sockets connect only on the guest loopback".to_owned(),
            ));
        }
        if !family_matches(current.family, addr.ip()) {
            return Err(Error::Message(
                "socket family does not match the connect address".to_owned(),
            ));
        }
        if !current.kind.is_stream() {
            let socket = table
                .sockets
                .get_mut(&fd)
                .ok_or_else(|| Error::NotFound(format!("socket {}", fd)))?;
            socket.peer = Some(addr);
            socket.state = SocketState::Connected;
            return Ok(());
        }
        let key = (addr.port(), current.family.code(), current.kind.code());
        let listener_fd = match table.bound.get(&key) {
            Some(value) => *value,
            None => return Err(Error::NotFound(format!("no guest listener on {}", addr))),
        };
        if listener_fd == fd {
            return Err(Error::Message(
                "a socket cannot connect to itself".to_owned(),
            ));
        }
        let listener = table
            .sockets
            .get(&listener_fd)
            .cloned()
            .ok_or_else(|| Error::NotFound(format!("socket {}", listener_fd)))?;
        if listener.state != SocketState::Listening {
            return Err(Error::NetworkDenied(
                "target socket is not listening".to_owned(),
            ));
        }
        let queued = table
            .pending
            .get(&listener_fd)
            .map(|queue| queue.len())
            .unwrap_or(0);
        if queued >= listener.backlog.max(1) {
            return Err(Error::Message("guest listen backlog is full".to_owned()));
        }
        let accepted_fd = allocate_fd(&mut table)?;
        let client_local = current
            .local
            .unwrap_or_else(|| SocketAddr::new(current.family.loopback_ip(), 0));
        let accepted = Socket::accepted(accepted_fd, &listener, client_local)?;
        {
            let socket = table
                .sockets
                .get_mut(&fd)
                .ok_or_else(|| Error::NotFound(format!("socket {}", fd)))?;
            socket.peer = Some(addr);
            socket.state = SocketState::Connected;
        }
        table.sockets.insert(accepted_fd, accepted);
        table.links.insert(fd, accepted_fd);
        table.links.insert(accepted_fd, fd);
        table
            .pending
            .entry(listener_fd)
            .or_default()
            .push_back(accepted_fd);
        Ok(())
    }

    pub fn accept(&self, fd: u64) -> Result<Option<u64>> {
        let mut table = self.guard()?;
        let listener = table
            .sockets
            .get(&fd)
            .cloned()
            .ok_or_else(|| Error::NotFound(format!("socket {}", fd)))?;
        if listener.state != SocketState::Listening {
            return Err(Error::Message(
                "accept requires a listening socket".to_owned(),
            ));
        }
        loop {
            match table.pending.entry(fd).or_default().pop_front() {
                Some(accepted_fd) => {
                    if let Some(socket) = table.sockets.get_mut(&accepted_fd) {
                        if socket.state != SocketState::Closed {
                            socket.state = SocketState::Connected;
                            return Ok(Some(accepted_fd));
                        }
                    }
                    // Dead queued descriptor; keep draining.
                }
                None => return Ok(None),
            }
        }
    }

    pub fn pending(&self, fd: u64) -> Result<usize> {
        let table = self.guard()?;
        if !table.sockets.contains_key(&fd) {
            return Err(Error::NotFound(format!("socket {}", fd)));
        }
        Ok(table.pending.get(&fd).map(|queue| queue.len()).unwrap_or(0))
    }

    pub fn send(&self, fd: u64, data: &[u8], to: Option<SocketAddr>) -> Result<usize> {
        if data.len() > MAX_DATAGRAM {
            return Err(Error::Message(
                "guest datagram exceeds 65535 bytes".to_owned(),
            ));
        }
        let mut table = self.guard()?;
        let current = table
            .sockets
            .get(&fd)
            .cloned()
            .ok_or_else(|| Error::NotFound(format!("socket {}", fd)))?;
        if current.state == SocketState::Closed {
            return Err(Error::Message("socket is closed".to_owned()));
        }
        let from = current.local;
        if current.kind.is_stream() {
            if current.state != SocketState::Connected {
                return Err(Error::Message("stream socket is not connected".to_owned()));
            }
            let peer_fd = *table
                .links
                .get(&fd)
                .ok_or_else(|| Error::NotFound("stream peer".to_owned()))?;
            if table
                .sockets
                .get(&peer_fd)
                .map(|peer| peer.state == SocketState::Closed)
                .unwrap_or(true)
            {
                return Err(Error::Message("stream peer is closed".to_owned()));
            }
            enqueue(
                &mut table,
                peer_fd,
                Message {
                    from,
                    data: data.to_vec(),
                },
            )?;
            return Ok(data.len());
        }
        let destination = match to.or(current.peer) {
            Some(value) => value,
            None => return Err(Error::Message("datagram destination is not set".to_owned())),
        };
        if !is_guest_loopback(&destination) {
            return Err(Error::NetworkDenied(
                "guest datagrams stay on the guest loopback".to_owned(),
            ));
        }
        let key = (
            destination.port(),
            current.family.code(),
            current.kind.code(),
        );
        let target_fd = match table.bound.get(&key) {
            Some(value) => *value,
            None => {
                return Err(Error::NotFound(format!(
                    "no guest socket bound on {}",
                    destination
                )))
            }
        };
        if let Some(socket) = table.sockets.get_mut(&fd) {
            if to.is_some() && socket.peer.is_none() {
                socket.peer = to;
            }
            if socket.state == SocketState::Created {
                socket.state = SocketState::Connected;
            }
        }
        enqueue(
            &mut table,
            target_fd,
            Message {
                from,
                data: data.to_vec(),
            },
        )?;
        Ok(data.len())
    }

    pub fn recv(&self, fd: u64, max: usize) -> Result<Option<Vec<u8>>> {
        if max == 0 {
            return Err(Error::Message("receive buffer length is zero".to_owned()));
        }
        let mut table = self.guard()?;
        if !table.sockets.contains_key(&fd) {
            return Err(Error::NotFound(format!("socket {}", fd)));
        }
        let queue = table.queues.entry(fd).or_default();
        match queue.pop_front() {
            None => Ok(None),
            Some(Message { data, .. }) if data.len() <= max => Ok(Some(data)),
            Some(Message { from, data }) => {
                let (head, tail) = data.split_at(max);
                let chunk = head.to_vec();
                queue.push_front(Message {
                    from,
                    data: tail.to_vec(),
                });
                Ok(Some(chunk))
            }
        }
    }

    pub fn recv_from(&self, fd: u64, max: usize) -> Result<Option<(SocketAddr, Vec<u8>)>> {
        if max == 0 {
            return Err(Error::Message("receive buffer length is zero".to_owned()));
        }
        let mut table = self.guard()?;
        let family = table
            .sockets
            .get(&fd)
            .map(|socket| socket.family)
            .ok_or_else(|| Error::NotFound(format!("socket {}", fd)))?;
        let queue = table.queues.entry(fd).or_default();
        match queue.pop_front() {
            None => Ok(None),
            Some(Message { from, data }) => {
                let source = from.unwrap_or_else(|| SocketAddr::new(family.loopback_ip(), 0));
                if data.len() <= max {
                    Ok(Some((source, data)))
                } else {
                    let (head, tail) = data.split_at(max);
                    let chunk = head.to_vec();
                    queue.push_front(Message {
                        from,
                        data: tail.to_vec(),
                    });
                    Ok(Some((source, chunk)))
                }
            }
        }
    }

    pub fn close(&self, fd: u64) -> Result<()> {
        let mut table = self.guard()?;
        {
            let socket = table
                .sockets
                .get_mut(&fd)
                .ok_or_else(|| Error::NotFound(format!("socket {}", fd)))?;
            socket.close();
        }
        let keys: Vec<(u16, u8, u8)> = table
            .bound
            .iter()
            .filter(|(_, owner)| **owner == fd)
            .map(|(key, _)| *key)
            .collect();
        for key in keys {
            table.bound.remove(&key);
        }
        table.queues.remove(&fd);
        table.pending.remove(&fd);
        if let Some(peer_fd) = table.links.remove(&fd) {
            table.links.remove(&peer_fd);
        }
        // A closed fd must not survive in another listener's accept queue:
        // popping it there used to set it back to Connected and return a dead
        // descriptor whose peer had been cleared. Removing it from the table
        // also stops the monotonic `next_fd` from leaking one Socket per close.
        for queue in table.pending.values_mut() {
            queue.retain(|queued| *queued != fd);
        }
        table.sockets.remove(&fd);
        Ok(())
    }
}

fn enqueue(table: &mut TableInner, fd: u64, message: Message) -> Result<()> {
    let queue = table.queues.entry(fd).or_default();
    if queue.len() >= MAX_QUEUE {
        return Err(Error::Message("guest receive queue is full".to_owned()));
    }
    queue.push_back(message);
    Ok(())
}

fn allocate_fd(table: &mut TableInner) -> Result<u64> {
    if table.next_fd < 3 {
        table.next_fd = 3;
    }
    let fd = table.next_fd;
    table.next_fd = fd
        .checked_add(1)
        .ok_or_else(|| Error::Message("socket descriptor space exhausted".to_owned()))?;
    Ok(fd)
}

fn ephemeral_port(table: &TableInner, family: Afi, kind: SockKind) -> Result<u16> {
    for port in EPHEMERAL_FIRST..=EPHEMERAL_LAST {
        let key = (port, family.code(), kind.code());
        if !table.bound.contains_key(&key) {
            return Ok(port);
        }
    }
    Err(Error::Message(
        "no ephemeral guest port available".to_owned(),
    ))
}

fn family_matches(family: Afi, ip: IpAddr) -> bool {
    matches!(
        (family, ip),
        (Afi::Inet, IpAddr::V4(_)) | (Afi::Inet6, IpAddr::V6(_))
    )
}

pub fn is_guest_loopback(addr: &SocketAddr) -> bool {
    is_loopback_ip(addr.ip()) || is_unspecified_ip(addr.ip())
}
