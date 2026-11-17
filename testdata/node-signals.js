// Node signals and child processes; every line should end in true.
const { spawn, execFileSync } = require("child_process");
const os = require("os");
console.log("cpus", os.cpus().length > 0 && os.cpus()[0].model !== undefined);
console.log("execFileSync", execFileSync("/bin/echo", ["hi"]).toString() === "hi\n");
const keep = setTimeout(() => console.log("done", false), 20000);
process.on("SIGUSR2", () => {
  console.log("sigusr2", true);
  const c = spawn("/bin/sleep", ["5"]);
  c.on("exit", (code, sig) => {
    console.log("child killed", sig === "SIGTERM");
    clearTimeout(keep);
  });
  setTimeout(() => c.kill("SIGTERM"), 100);
});
process.kill(process.pid, "SIGUSR2");
