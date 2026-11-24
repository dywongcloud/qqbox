//! MCP server for qqbox. Implements JSON-RPC 2.0 `initialize`,
//! `tools/list` and `tools/call` over stdio (newline-delimited JSON) or
//! streamable HTTP. The `run` tool calls the Rust library directly; the general
//! `qqbox run` path still returns `UnsupportedPlatform`, so it reports the
//! library's diagnostic as a tool error rather than pretending the guest ran.
//! The Linux trap-to-supervisor backend IS ported (see docs/NATIVE_BACKEND.md);
//! `qqbox backend exec-elf` is its witnessed executable path.

use clap::Parser;
use qqbox::daemon::Daemon;
use qqbox::security;
use qqbox::{run_image, Context, Options};
use serde_json::{json, Value};
use std::io::{self, BufRead, Read as _, Write};

const PROTOCOL: &str = "2025-06-18";
/// Same ceiling as qqboxd: one byte past the limit is enough to reject.
pub const MAX_BODY_BYTES: usize = 16 * 1024 * 1024;

#[derive(Parser)]
#[command(name = "qqbox-mcp", version, about = "MCP server for qqbox sandboxes")]
struct Cli {
    /// Streamable HTTP bind address instead of stdio, for example 127.0.0.1:8080.
    #[arg(long)]
    http: Option<String>,
    /// Bearer token REQUIRED when --http is used. Stdio mode needs no token
    /// because the transport is the calling process's private pipe.
    #[arg(long)]
    token: Option<String>,
    /// Allow the `run` tool to request guest networking.
    #[arg(long)]
    allow_net: bool,
    /// Default per-run timeout in seconds (informational until a backend is ported).
    #[arg(long, default_value_t = 120)]
    timeout: u64,
    /// Per-stream output cap in bytes.
    #[arg(long, default_value_t = 1048576)]
    max_output: usize,
}

fn tool_list() -> Value {
    json!([
        {
            "name": "run",
            "description": "Run a command in a disposable sandbox created from an OCI image. The general qqbox run path still returns UnsupportedPlatform; the Linux trap-to-supervisor backend is ported and witnessed via qqbox backend exec-elf.",
            "inputSchema": {
                "type": "object",
                "properties": {
                    "image": {"type": "string", "description": "OCI image reference"},
                    "command": {"type": "array", "items": {"type": "string"}},
                    "network": {"type": "boolean", "description": "request external networking"}
                },
                "required": ["image", "command"]
            }
        },
        {
            "name": "qqbox_invariants",
            "description": "List the enforced qqbox security invariants.",
            "inputSchema": {"type": "object", "properties": {}}
        },
        {
            "name": "qqbox_routes",
            "description": "List the Docker Engine API routes served by qqboxd.",
            "inputSchema": {"type": "object", "properties": {}}
        },
        {
            "name": "qqbox_runtime_spec_validate",
            "description": "Parse and validate an OCI runtime-spec config.json; unsupported security constructs are rejected.",
            "inputSchema": {"type": "object", "properties": {"path": {"type": "string"}}, "required": ["path"]}
        },
        {
            "name": "qqbox_config_validate",
            "description": "Parse and print every construct of an OCI image config JSON file.",
            "inputSchema": {"type": "object", "properties": {"path": {"type": "string"}}, "required": ["path"]}
        },
        {
            "name": "qqbox_ports_check",
            "description": "Parse publish specs such as 8080:80 and print the forwarding plan.",
            "inputSchema": {"type": "object", "properties": {"specs": {"type": "array", "items": {"type": "string"}}}, "required": ["specs"]}
        },
        {
            "name": "qqbox_compose_plan",
            "description": "Lower a Compose YAML/JSON file to a dependency-ordered instance plan.",
            "inputSchema": {"type": "object", "properties": {"path": {"type": "string"}, "project": {"type": "string"}}, "required": ["path"]}
        },
        {
            "name": "qqbox_version",
            "description": "Report the qqbox version, host OS and architecture.",
            "inputSchema": {"type": "object", "properties": {}}
        }
    ])
}

fn text_result(text: String, is_error: bool) -> Value {
    json!({"content": [{"type": "text", "text": text}], "isError": is_error})
}

fn call_tool(name: &str, args: &Value, cli: &Cli) -> Value {
    match name {
        "run" => {
            let image = args.get("image").and_then(Value::as_str).unwrap_or("");
            let command: Vec<String> = args
                .get("command")
                .and_then(Value::as_array)
                .map(|a| {
                    a.iter()
                        .filter_map(|v| v.as_str().map(str::to_owned))
                        .collect()
                })
                .unwrap_or_default();
            let wants_net = args
                .get("network")
                .and_then(Value::as_bool)
                .unwrap_or(false);
            if image.is_empty() || command.is_empty() {
                return text_result("image and a non-empty command are required".into(), true);
            }
            if wants_net && !cli.allow_net {
                return text_result(
                    "network=true requires the server to start with --allow-net".into(),
                    true,
                );
            }
            let opts = Options {
                args: Some(command.clone()),
                net: wants_net,
                ..Options::default()
            };
            let ctx = Context::default();
            match run_image(&ctx, image, &opts) {
                Ok(code) => text_result(
                    json!({"exitCode": code, "stdout": "", "stderr": ""}).to_string(),
                    false,
                ),
                Err(err) => {
                    let platform = qqbox::backend::current();
                    let boundary = match platform.capabilities() {
                        Ok(c) => json!({
                            "host": c.host, "arch": c.arch,
                            "sigsysHandler": c.sigsys_handler,
                            "implementedSyscalls": c.implemented_syscalls,
                            "note": c.note
                        }),
                        Err(e) => json!({"host": platform.name(), "error": e.to_string()}),
                    };
                    text_result(
                        json!({
                            "exitCode": null,
                            "stdout": "",
                            "stderr": err.to_string(),
                            "qqboxDiagnostic": err.to_string(),
                            "nativeBackend": boundary
                        })
                        .to_string(),
                        true,
                    )
                }
            }
        }
        "qqbox_invariants" => text_result(security::audit().join("\n"), false),
        "qqbox_routes" => text_result(Daemon::routes().join("\n"), false),
        "qqbox_runtime_spec_validate" => {
            let path = args.get("path").and_then(Value::as_str).unwrap_or("");
            match std::fs::read(path)
                .map_err(qqbox::Error::from)
                .and_then(|b| qqbox::oci::runtime::parse(&b))
                .and_then(|spec| qqbox::oci::runtime::validate(&spec).map(|_| spec))
            {
                Ok(spec) => text_result(
                    json!({"valid": true, "ociVersion": spec.oci_version, "root": spec.root.path})
                        .to_string(),
                    false,
                ),
                Err(err) => text_result(err.to_string(), true),
            }
        }
        "qqbox_config_validate" => {
            let path = args.get("path").and_then(Value::as_str).unwrap_or("");
            match std::fs::read(path)
                .map_err(qqbox::Error::from)
                .and_then(|b| qqbox::oci::parse_image_config(&b))
            {
                Ok(cfg) => text_result(
                    serde_json::to_string_pretty(&cfg).unwrap_or_default(),
                    false,
                ),
                Err(err) => text_result(err.to_string(), true),
            }
        }
        "qqbox_ports_check" => {
            let specs: Vec<String> = args
                .get("specs")
                .and_then(Value::as_array)
                .map(|a| {
                    a.iter()
                        .filter_map(|v| v.as_str().map(str::to_owned))
                        .collect()
                })
                .unwrap_or_default();
            let mut lines = Vec::new();
            for s in specs {
                match qqbox::net::ports::parse_published_port(&s)
                    .and_then(|p| qqbox::net::ports::expand(&p).map(|e| (p, e)))
                {
                    Ok((p, e)) => lines.push(format!(
                        "{s} -> {e:?} ({})",
                        qqbox::net::ports::render_published_port(&p)
                    )),
                    Err(err) => lines.push(format!("{s} -> ERROR: {err}")),
                }
            }
            text_result(lines.join("\n"), false)
        }
        "qqbox_compose_plan" => {
            let path = args.get("path").and_then(Value::as_str).unwrap_or("");
            let project = args
                .get("project")
                .and_then(Value::as_str)
                .unwrap_or("qqbox");
            match std::fs::read(path)
                .map_err(qqbox::Error::from)
                .and_then(|b| qqbox::compose::parse(&b))
                .and_then(|f| qqbox::compose::validate(&f).map(|_| f))
                .and_then(|f| qqbox::compose::plan(&f, project))
                .and_then(|p| qqbox::compose::to_json(&p))
            {
                Ok(s) => text_result(s, false),
                Err(err) => text_result(err.to_string(), true),
            }
        }
        "qqbox_version" => text_result(
            json!({
                "version": env!("CARGO_PKG_VERSION"),
                "host": std::env::consts::OS,
                "arch": std::env::consts::ARCH,
                "timeoutSeconds": cli.timeout,
                "maxOutputBytes": cli.max_output
            })
            .to_string(),
            false,
        ),
        other => text_result(format!("unknown tool: {other}"), true),
    }
}

fn handle(req: &Value, cli: &Cli) -> Option<Value> {
    let method = req.get("method").and_then(Value::as_str).unwrap_or("");
    let id = req.get("id").cloned();
    id.as_ref()?; // notification
    let result = match method {
        "initialize" => json!({
            "protocolVersion": PROTOCOL,
            "capabilities": {"tools": {"listChanged": false}},
            "serverInfo": {"name": "qqbox-mcp", "version": env!("CARGO_PKG_VERSION")}
        }),
        "ping" => json!({}),
        "tools/list" => json!({"tools": tool_list()}),
        "tools/call" => {
            let params = req.get("params").cloned().unwrap_or_else(|| json!({}));
            let name = params.get("name").and_then(Value::as_str).unwrap_or("");
            let args = params
                .get("arguments")
                .cloned()
                .unwrap_or_else(|| json!({}));
            call_tool(name, &args, cli)
        }
        _ => {
            return Some(json!({
                "jsonrpc": "2.0",
                "id": id,
                "error": {"code": -32601, "message": format!("method not found: {method}")}
            }))
        }
    };
    Some(json!({"jsonrpc": "2.0", "id": id, "result": result}))
}

fn stdio(cli: &Cli) -> io::Result<()> {
    let stdin = io::stdin();
    let mut out = io::stdout();
    let mut reader = stdin.lock();
    loop {
        let mut line = String::new();
        // Newline-delimited JSON-RPC: read ONE line per request. `read_to_string`
        // on a Take waits for EOF, so a caller that keeps its pipe open (the
        // normal MCP transport) would never get a reply. `read_line` returns at
        // the first newline; `.take()` bounds a frame without a timeout.
        // Two extra bytes leave room for the framing newline and an optional CR,
        // so a body of EXACTLY MAX_BODY_BYTES is accepted rather than rejected
        // for its delimiter.
        let n = reader
            .by_ref()
            .take(MAX_BODY_BYTES as u64 + 2)
            .read_line(&mut line)?;
        if n == 0 {
            break;
        }
        let framed = line.ends_with('\n');
        if framed {
            line.pop();
            if line.ends_with('\r') {
                line.pop();
            }
        }
        // The ceiling applies to the JSON body, never to its delimiter: a
        // body of exactly MAX_BODY_BYTES plus "\n" is accepted, while a frame
        // that hit the Take limit is rejected because line.len() is > MAX.
        if line.len() > MAX_BODY_BYTES {
            return Err(io::Error::other("MCP stdio request exceeds MAX_BODY_BYTES"));
        }
        let trimmed = line.trim();
        if trimmed.is_empty() {
            continue;
        }
        let req: Value = match serde_json::from_str(trimmed) {
            Ok(v) => v,
            Err(_) => continue,
        };
        if let Some(resp) = handle(&req, cli) {
            writeln!(out, "{resp}")?;
            out.flush()?;
        }
    }
    Ok(())
}

fn http(cli: &Cli, addr: &str) -> io::Result<()> {
    use qqbox::net::http::{self, HttpResponse};

    let token = cli.token.as_deref().ok_or_else(|| {
        io::Error::other(
            "MCP HTTP mode requires --token (zero-trust: no anonymous HTTP). Failing closed.",
        )
    })?;
    if token.is_empty() || token.trim() != token {
        return Err(io::Error::other(
            "MCP --token must be non-empty without surrounding whitespace",
        ));
    }
    let want = format!("Bearer {token}").into_bytes();
    eprintln!("qqbox-mcp listening on http://{addr}/ (bearer auth required)");
    http::serve(addr, MAX_BODY_BYTES, |req| {
        let auth = req
            .header("authorization")
            .map(|v| v.as_bytes().to_vec());
        let authorized = auth
            .as_deref()
            .map(|a| security::secure_eq(a, &want))
            .unwrap_or(false);
        if !authorized {
            return HttpResponse::json(401, br#"{"error":"unauthorized"}"#.to_vec());
        }
        let parsed: Result<Value, _> = serde_json::from_slice(&req.body);
        let (status, payload) = match parsed {
            Ok(v) => match handle(&v, cli) {
                Some(resp) => (200, resp.to_string()),
                None => (202, String::new()),
            },
            Err(err) => (
                400,
                json!({"jsonrpc":"2.0","id":null,"error":{"code":-32700,"message":err.to_string()}})
                    .to_string(),
            ),
        };
        let data = if status == 202 {
            "accepted".to_string()
        } else {
            payload
        };
        // The advertised --max-output cap is enforced rather than merely
        // echoed by `qqbox_version`.
        if data.len() > cli.max_output {
            let too_large = json!({"jsonrpc":"2.0","id":null,"error":{"code":-32000,"message":"tool output exceeds --max-output"}});
            return HttpResponse::json(200, too_large.to_string().into_bytes());
        }
        HttpResponse::json(status, data.into_bytes())
    })
    .map_err(|e| io::Error::other(e.to_string()))
}

fn main() {
    let cli = Cli::parse();
    let result = match &cli.http {
        Some(addr) => http(&cli, addr),
        None => stdio(&cli),
    };
    if let Err(err) = result {
        eprintln!("qqbox-mcp: {err}");
        std::process::exit(1);
    }
}
