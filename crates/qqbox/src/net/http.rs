//! Small HTTP/1.1 server with a real per-socket read deadline.
//!
//! tiny_http 0.12 drains an unread request body in `EqualReader::drop`: a peer
//! that declares a large Content-Length and then stalls keeps the worker
//! blocked until it closes. Owning the accepted socket here fixes the
//! synchronization instead: a declared body over the ceiling gets 413 before
//! any body read, the connection is closed by dropping the stream (no drain),
//! and every read has a socket deadline so a peer that stalls mid-header or
//! mid-body releases its slot. Chunked request bodies are decoded under the
//! same ceiling.
//!
//! The deadline is a bound on untrusted network input, like MAX_BODY_BYTES,
//! not a correctness fix or a periodic recheck: request correctness comes from
//! bounded header/body parsing and from the handler, never from waiting.
//!
//! Responses always carry `Connection: close`; the daemon and MCP control
//! planes do not multiplex, and closing is what releases a stalled peer.

use crate::error::{Error, Result};
use std::io::{self, BufRead, BufReader, Read, Write};
use std::net::{TcpListener, TcpStream, ToSocketAddrs};
use std::sync::atomic::{AtomicUsize, Ordering};
use std::time::Duration;

/// A read that blocks longer than this is a stalled peer, not a slow client.
/// A local control plane never needs more than this to deliver headers or a
/// bounded body. This is a bound, not a wait-for-correctness loop.
pub const READ_DEADLINE: Duration = Duration::from_secs(5);

const MAX_LINE: u64 = 16 * 1024;
const MAX_HEADER_BYTES: usize = 64 * 1024;
const MAX_HEADERS: usize = 128;
const MAX_IN_FLIGHT: usize = 64;

#[derive(Debug, Clone)]
pub struct HttpRequest {
    pub method: String,
    pub path: String,
    pub query: String,
    pub headers: Vec<(String, String)>,
    pub body: Vec<u8>,
}

impl HttpRequest {
    pub fn header(&self, name: &str) -> Option<&str> {
        self.headers
            .iter()
            .find(|(k, _)| k.eq_ignore_ascii_case(name))
            .map(|(_, v)| v.as_str())
    }
}

#[derive(Debug, Clone)]
pub struct HttpResponse {
    pub status: u16,
    pub content_type: &'static str,
    pub body: Vec<u8>,
}

impl HttpResponse {
    pub fn json(status: u16, body: impl Into<Vec<u8>>) -> Self {
        Self {
            status,
            content_type: "application/json",
            body: body.into(),
        }
    }
}

struct InFlight<'a>(&'a AtomicUsize);

impl Drop for InFlight<'_> {
    fn drop(&mut self) {
        self.0.fetch_sub(1, Ordering::SeqCst);
    }
}

fn reason(status: u16) -> &'static str {
    match status {
        200 => "OK",
        201 => "Created",
        202 => "Accepted",
        400 => "Bad Request",
        401 => "Unauthorized",
        403 => "Forbidden",
        404 => "Not Found",
        405 => "Method Not Allowed",
        413 => "Payload Too Large",
        431 => "Request Header Fields Too Large",
        500 => "Internal Server Error",
        501 => "Not Implemented",
        503 => "Service Unavailable",
        _ => "Unknown",
    }
}

fn write_http<W: Write>(w: &mut W, response: &HttpResponse) -> io::Result<()> {
    let head = format!(
        "HTTP/1.1 {} {}\r\nContent-Type: {}\r\nContent-Length: {}\r\nConnection: close\r\n\r\n",
        response.status,
        reason(response.status),
        response.content_type,
        response.body.len()
    );
    // Keep the write deadline meaningful: one header write, then one body
    // write. A peer that never reads cannot hold the worker indefinitely.
    w.write_all(head.as_bytes())?;
    w.write_all(&response.body)?;
    w.flush()
}

fn write_status<W: Write>(w: &mut W, status: u16) -> io::Result<()> {
    let body = match status {
        400 => r#"{"message":"malformed HTTP request"}"#,
        413 => r#"{"message":"request body exceeds MAX_BODY_BYTES"}"#,
        431 => r#"{"message":"request headers exceed the ceiling"}"#,
        503 => r#"{"message":"too many in-flight requests"}"#,
        _ => r#"{"message":"request rejected"}"#,
    };
    write_http(w, &HttpResponse::json(status, body.as_bytes().to_vec()))
}

fn header<'a>(headers: &'a [(String, String)], name: &str) -> Option<&'a str> {
    headers
        .iter()
        .find(|(k, _)| k.eq_ignore_ascii_case(name))
        .map(|(_, v)| v.as_str())
}

/// Decode a chunked request body under `max_body`. Returns the body or a
/// rejection status. Every read is bounded by the caller's socket deadline.
fn read_chunked<R: BufRead>(
    reader: &mut R,
    max_body: usize,
) -> Result<std::result::Result<Vec<u8>, u16>> {
    let mut out = Vec::new();
    loop {
        let mut size_line = String::new();
        let n = reader
            .by_ref()
            .take(MAX_LINE)
            .read_line(&mut size_line)
            .map_err(Error::Io)?;
        if n == 0 || !size_line.ends_with('\n') {
            return Ok(Err(400));
        }
        let hex = size_line.trim().split(';').next().unwrap_or("").trim();
        let size = match usize::from_str_radix(hex, 16) {
            Ok(v) => v,
            Err(_) => return Ok(Err(400)),
        };
        if size == 0 {
            // Consume trailers up to the blank line; they are bounded by the
            // same header ceiling.
            let mut trailer_bytes = 0usize;
            loop {
                let mut line = String::new();
                let m = reader
                    .by_ref()
                    .take(MAX_LINE)
                    .read_line(&mut line)
                    .map_err(Error::Io)?;
                if m == 0 || line.trim_end_matches(['\r', '\n']).is_empty() {
                    break;
                }
                trailer_bytes += m;
                if trailer_bytes > MAX_HEADER_BYTES {
                    return Ok(Err(431));
                }
            }
            break;
        }
        if out.len().checked_add(size).is_none_or(|n| n > max_body) {
            return Ok(Err(413));
        }
        let start = out.len();
        out.resize(start + size, 0);
        reader.read_exact(&mut out[start..]).map_err(Error::Io)?;
        let mut crlf = [0u8; 2];
        reader.read_exact(&mut crlf).map_err(Error::Io)?;
        if crlf != *b"\r\n" {
            return Ok(Err(400));
        }
    }
    Ok(Ok(out))
}

fn handle_connection<F>(stream: &mut TcpStream, max_body: usize, handler: &F) -> Result<()>
where
    F: Fn(HttpRequest) -> HttpResponse + Sync,
{
    stream
        .set_read_timeout(Some(READ_DEADLINE))
        .map_err(Error::Io)?;
    stream
        .set_write_timeout(Some(READ_DEADLINE))
        .map_err(Error::Io)?;

    let mut writer = &*stream;
    let mut reader = BufReader::new(&*stream);

    let mut line = String::new();
    let n = reader
        .by_ref()
        .take(MAX_LINE)
        .read_line(&mut line)
        .map_err(Error::Io)?;
    if n == 0 {
        return Ok(()); // clean close before a request
    }
    if !line.ends_with('\n') {
        return write_status(&mut writer, 400).map_err(Error::Io);
    }
    let request_line = line.trim_end_matches(['\r', '\n']).to_string();
    let mut parts = request_line.split_whitespace();
    let method = parts.next().unwrap_or("").to_string();
    let target = parts.next().unwrap_or("").to_string();
    let version = parts.next().unwrap_or("");
    if method.is_empty()
        || target.is_empty()
        || parts.next().is_some()
        || (version != "HTTP/1.1" && version != "HTTP/1.0")
    {
        return write_status(&mut writer, 400).map_err(Error::Io);
    }

    let mut headers: Vec<(String, String)> = Vec::new();
    let mut header_bytes = n;
    loop {
        line.clear();
        let m = reader
            .by_ref()
            .take(MAX_LINE)
            .read_line(&mut line)
            .map_err(Error::Io)?;
        if m == 0 {
            return write_status(&mut writer, 400).map_err(Error::Io);
        }
        if !line.ends_with('\n') {
            return write_status(&mut writer, 400).map_err(Error::Io);
        }
        header_bytes += m;
        if header_bytes > MAX_HEADER_BYTES || headers.len() >= MAX_HEADERS {
            return write_status(&mut writer, 431).map_err(Error::Io);
        }
        let text = line.trim_end_matches(['\r', '\n']);
        if text.is_empty() {
            break;
        }
        let Some((key, value)) = text.split_once(':') else {
            return write_status(&mut writer, 400).map_err(Error::Io);
        };
        headers.push((key.trim().to_ascii_lowercase(), value.trim().to_string()));
    }

    let chunked =
        header(&headers, "transfer-encoding").is_some_and(|v| v.eq_ignore_ascii_case("chunked"));
    let declared = header(&headers, "content-length")
        .map(|v| v.parse::<usize>().map_err(|_| ()))
        .transpose()
        .map_err(|_| Error::Message("invalid Content-Length".into()))?;
    if chunked && declared.is_some() {
        return write_status(&mut writer, 400).map_err(Error::Io);
    }

    // Reject an oversize DECLARED body before touching the stream. This is the
    // synchronization fix: no incomplete body is ever left to be drained.
    if let Some(len) = declared {
        if len > max_body {
            return write_status(&mut writer, 413).map_err(Error::Io);
        }
    }

    if header(&headers, "expect").is_some_and(|v| v.eq_ignore_ascii_case("100-continue")) {
        writer
            .write_all(b"HTTP/1.1 100 Continue\r\n\r\n")
            .map_err(Error::Io)?;
        writer.flush().map_err(Error::Io)?;
    }

    let body = if let Some(len) = declared {
        let mut buf = vec![0u8; len];
        reader.read_exact(&mut buf).map_err(Error::Io)?;
        buf
    } else if chunked {
        match read_chunked(&mut reader, max_body)? {
            Ok(bytes) => bytes,
            Err(status) => return write_status(&mut writer, status).map_err(Error::Io),
        }
    } else {
        Vec::new()
    };

    let (path, query) = match target.split_once('?') {
        Some((p, q)) => (p.to_string(), q.to_string()),
        None => (target.clone(), String::new()),
    };

    let response = handler(HttpRequest {
        method,
        path,
        query,
        headers,
        body,
    });
    write_http(&mut writer, &response).map_err(Error::Io)
}

/// Serve HTTP/1.1 requests until the process exits. `handler` runs on bounded
/// scoped threads; saturation is a typed 503 with no wait, never a sleep or a
/// periodic retry.
pub fn serve<A, F>(addr: A, max_body: usize, handler: F) -> Result<()>
where
    A: ToSocketAddrs,
    F: Fn(HttpRequest) -> HttpResponse + Sync,
{
    let listener = TcpListener::bind(addr).map_err(Error::Io)?;
    let in_flight = AtomicUsize::new(0);
    let handler = &handler;
    std::thread::scope(|scope| -> Result<()> {
        for connection in listener.incoming() {
            let mut stream = match connection {
                Ok(stream) => stream,
                Err(_) => continue,
            };
            let mut current = in_flight.load(Ordering::SeqCst);
            let reserved = loop {
                if current >= MAX_IN_FLIGHT {
                    break false;
                }
                match in_flight.compare_exchange_weak(
                    current,
                    current + 1,
                    Ordering::SeqCst,
                    Ordering::SeqCst,
                ) {
                    Ok(_) => break true,
                    Err(next) => current = next,
                }
            };
            if !reserved {
                let mut writer = &stream;
                let _ = write_status(&mut writer, 503);
                continue;
            }
            let guard = InFlight(&in_flight);
            scope.spawn(move || {
                let _guard = guard;
                let _ = handle_connection(&mut stream, max_body, handler);
            });
        }
        Ok(())
    })
}
