"""A PTY-free command console for the laboon XFCE desktop.

laboon wires guest stdio through pipes, and VTE/xfce4-terminal cannot paint a
PTY under laboon's synthetic X. This console runs commands with plain pipes
(laboon's native stdio) and serves a small HTML UI that Chromium displays as a
window on the desktop.

Security posture (hardened after adversarial review):
  * bind 127.0.0.1 only - the guest's loopback is its own, not the host's.
  * reject foreign Origin / Host headers (DNS-rebinding and simple-request
    cross-origin command injection).
  * require application/json, cap the request body, and return 400 (not 500)
    on malformed input.
  * cap stdout/stderr in the response and bound execution with a timeout.
  * never write outside the guest; commands stay inside the laboon sandbox.

Endpoints:
  GET  /            -> HTML console
  POST /run         -> {"cmd": "..."} -> {"stdout","stderr","exit","cmd"}
  GET  /healthz     -> {"ok": true}
"""
import json
import os
import subprocess
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

PORT = int(os.environ.get("CONSOLE_PORT", "8765"))
MAX_BODY = 64 * 1024
MAX_OUTPUT = 1024 * 1024
TIMEOUT = 30
ORIGINS = {
    "http://127.0.0.1:%d" % PORT,
    "http://localhost:%d" % PORT,
}
HOSTS = {"127.0.0.1:%d" % PORT, "localhost:%d" % PORT, "127.0.0.1", "localhost"}

HTML = r"""<!doctype html>
<html><head><meta charset="utf-8"><title>laboon terminal</title>
<style>
 :root { color-scheme: dark; }
 body { margin:0; background:#10131a; color:#e8edf5; font:14px/1.45 ui-monospace,SFMono-Regular,Menlo,monospace; }
 header { padding:10px 14px; background:#182030; border-bottom:1px solid #2b3650; font-weight:600; letter-spacing:.04em; }
 #out { height:calc(100vh - 118px); overflow:auto; padding:12px 14px; white-space:pre-wrap; }
 form { display:flex; gap:8px; padding:10px 14px; border-top:1px solid #2b3650; background:#141a26; }
 input { flex:1; background:#0c1018; color:#e8edf5; border:1px solid #2b3650; border-radius:4px; padding:9px 11px; font:inherit; }
 button { background:#2f6fb5; color:#fff; border:0; border-radius:4px; padding:0 18px; font:inherit; cursor:pointer; }
 .ok { color:#8fd3a2; } .err { color:#ff9a9a; } .cmd { color:#7fb2ff; }
</style></head><body>
<header>laboon terminal &mdash; PTY-free pipe console (guest /bin/sh)</header>
<div id="out"></div>
<form id="f"><input id="i" autocomplete="off" autofocus
 placeholder="type a command, e.g.  uname -a    (this console needs no PTY)">
<button>Run</button></form>
<script>
const out = document.getElementById('out'), i = document.getElementById('i');
function esc(s){return s.replace(/[&<>]/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;'}[c]));}
function add(html, cls){const d=document.createElement('div'); if(cls)d.className=cls;
 d.innerHTML=html; out.appendChild(d); out.scrollTop=out.scrollHeight;}
add('laboon pipe console ready. Commands run inside the sandbox as root.','ok');
document.getElementById('f').addEventListener('submit', async e=>{
 e.preventDefault(); const c=i.value.trim(); if(!c)return; i.value='';
 add('<span class="cmd">$ '+esc(c)+'</span>');
 try {
   const r=await fetch('/run',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({cmd:c})});
   const j=await r.json();
   if(j.stdout) add(esc(j.stdout));
   if(j.stderr) add(esc(j.stderr),'err');
   add('exit '+j.exit,'ok');
 } catch(e){ add('request failed: '+esc(String(e)),'err'); }
});
</script></body></html>"""


class Handler(BaseHTTPRequestHandler):
    server_version = "laboon-console/1.0"

    def log_message(self, fmt, *args):
        pass

    def _send(self, code, body, ctype="application/json"):
        data = body.encode() if isinstance(body, str) else body
        self.send_response(code)
        self.send_header("Content-Type", ctype)
        self.send_header("Content-Length", str(len(data)))
        self.send_header("Cache-Control", "no-store")
        self.send_header("X-Content-Type-Options", "nosniff")
        self.end_headers()
        self.wfile.write(data)

    def _json(self, code, obj):
        self._send(code, json.dumps(obj))

    def _gate(self):
        origin = self.headers.get("Origin")
        if origin and origin not in ORIGINS:
            self._json(403, {"error": "forbidden origin"})
            return False
        host = self.headers.get("Host")
        if host and host not in HOSTS:
            self._json(403, {"error": "forbidden host"})
            return False
        return True

    def do_GET(self):
        if not self._gate():
            return
        if self.path.startswith("/healthz"):
            return self._json(200, {"ok": True, "console": "pipe"})
        if self.path in ("/", "/index.html"):
            return self._send(200, HTML, "text/html; charset=utf-8")
        return self._json(404, {"error": "not found"})

    def do_POST(self):
        if not self._gate():
            return
        if self.path != "/run":
            return self._json(404, {"error": "not found"})
        if "application/json" not in self.headers.get("Content-Type", ""):
            return self._json(415, {"error": "content-type must be application/json"})
        cmd = ""
        try:
            length = int(self.headers.get("Content-Length", "0"))
        except ValueError:
            return self._json(400, {"error": "bad content-length"})
        if length <= 0 or length > MAX_BODY:
            return self._json(413, {"error": "body too large"})
        try:
            payload = json.loads(self.rfile.read(length).decode("utf-8"))
            cmd = payload.get("cmd", "")
            if not isinstance(cmd, str) or not cmd.strip():
                return self._json(400, {"error": "empty cmd"})
            if len(cmd) > 8192:
                return self._json(400, {"error": "cmd too long"})
            if "\x00" in cmd:
                return self._json(400, {"error": "embedded null byte"})
            cmd.encode("utf-8")  # reject surrogates before subprocess
            proc = subprocess.run(
                ["/bin/sh", "-c", cmd],
                stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=TIMEOUT,
            )
            stdout = proc.stdout[:MAX_OUTPUT].decode("utf-8", "replace")
            stderr = proc.stderr[:MAX_OUTPUT].decode("utf-8", "replace")
            if len(proc.stdout) > MAX_OUTPUT:
                stdout += "\n[truncated]"
            if len(proc.stderr) > MAX_OUTPUT:
                stderr += "\n[truncated]"
            return self._json(200, {
                "cmd": cmd, "stdout": stdout, "stderr": stderr,
                "exit": proc.returncode,
            })
        except subprocess.TimeoutExpired:
            return self._json(200, {"cmd": cmd, "stdout": "", "stderr": "timeout after 30s", "exit": 124})
        except (ValueError, UnicodeError, json.JSONDecodeError) as exc:
            return self._json(400, {"error": f"bad request: {type(exc).__name__}"})
        except Exception as exc:
            return self._json(500, {"error": f"{type(exc).__name__}: {exc}"})


if __name__ == "__main__":
    server = ThreadingHTTPServer(("127.0.0.1", PORT), Handler)
    print(f"laboon console listening on 127.0.0.1:{PORT}", flush=True)
    server.serve_forever()
