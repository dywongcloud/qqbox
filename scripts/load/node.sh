# A node HTTP server driven by a node client over loopback: 20000 requests on
# 50 keep-alive connections. Every response must answer its own request.
cat >/tmp/s.js <<'JS'
const http = require("http");
const s = http.createServer((req, res) => {
  let body = "";
  req.on("data", (d) => (body += d));
  req.on("end", () => res.end(JSON.stringify({ n: Number(req.url.slice(1)) })));
});
s.listen(0, "127.0.0.1", () => console.log(s.address().port));
JS
cat >/tmp/c.js <<'JS'
const http = require("http");
const N = 20000, C = 50;
const srv = require("child_process").spawn("node", ["/tmp/s.js"], { stdio: ["ignore", "pipe", "inherit"] });
srv.on("exit", (code, sig) => {
  console.log("server exited early: " + (sig || code));
  process.exit(1);
});
srv.stdout.once("data", (d) => {
  const port = Number(String(d).trim());
  const agent = new http.Agent({ keepAlive: true, maxSockets: C });
  const lat = [];
  let next = 0, done = 0, bad = 0;
  const t0 = process.hrtime.bigint();
  const finish = () => {
    const sec = Number(process.hrtime.bigint() - t0) / 1e9;
    lat.sort((a, b) => a - b);
    const q = (p) => (lat.length ? lat[Math.min(lat.length - 1, Math.floor(lat.length * p))].toFixed(2) : "-");
    console.log("LOAD node rps=" + Math.round(N / sec) + " p50=" + q(0.5) + "ms p99=" + q(0.99) + "ms bad=" + bad);
    srv.removeAllListeners("exit");
    srv.kill();
    process.exit(bad ? 1 : 0);
  };
  const settle = () => (++done === N ? finish() : one());
  const one = () => {
    if (next >= N) return;
    const i = next++;
    const s = process.hrtime.bigint();
    http.get({ host: "127.0.0.1", port, path: "/" + i, agent }, (res) => {
      let b = "";
      res.on("data", (d) => (b += d));
      res.on("end", () => {
        let ok = false;
        try { ok = res.statusCode === 200 && JSON.parse(b).n === i; } catch {}
        if (!ok) bad++;
        lat.push(Number(process.hrtime.bigint() - s) / 1e6);
        settle();
      });
    }).on("error", () => { bad++; settle(); });
  };
  for (let k = 0; k < C; k++) one();
});
JS
node /tmp/c.js
