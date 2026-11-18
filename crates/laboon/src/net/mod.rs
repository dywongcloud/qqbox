pub mod dns;
pub mod forward;
pub mod policy;
pub mod ports;
pub mod socket;

pub use dns::{
    build_query, encode_name, is_internal, is_loopback_ip, is_unspecified_ip, is_valid_name,
    normalize_name, parse_query, parse_response, reply, validate_name, DnsCode, DnsMessage,
    DnsQuestion, DnsResolver, DnsResponse, IpCidr, CLASS_IN, DNS_PORT, TYPE_A, TYPE_AAAA,
    TYPE_CNAME,
};
pub use forward::{ForwardRule, Forwarder};
pub use policy::EgressPolicy;
pub use ports::{
    expand, parse_port_spec, parse_protocol, parse_published_port, protocol_name,
    render_published_port, validate_published_port,
};
pub use socket::{
    is_guest_loopback, Afi, Message, Socket, SocketState, SocketTable, SockKind,
};
