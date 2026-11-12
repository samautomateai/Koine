// opencode compatibility helpers: the `$` shell tag and the `client` stand-in.
import { execFile } from "node:child_process";
import { log, state } from "./host.mjs";

const shq = (v) => "'" + String(v).replace(/'/g, "'\\''") + "'";
class ShellPromise {
  constructor(cmd) {
    this.cmd = cmd;
    this._nothrow = false;
    this._cwd = state.cwd;
    this._env = {};
    this._p = null;
  }
  quiet() {
    return this;
  }
  nothrow() {
    this._nothrow = true;
    return this;
  }
  cwd(d) {
    this._cwd = d;
    return this;
  }
  env(e) {
    this._env = e;
    return this;
  }
  run() {
    this._p ??= new Promise((resolve, reject) => {
      execFile("sh", ["-c", this.cmd], { cwd: this._cwd, env: { ...process.env, ...this._env }, maxBuffer: 64 << 20 }, (err, so, se) => {
        const out = {
          exitCode: err ? (typeof err.code === "number" ? err.code : 1) : 0,
          stdout: Buffer.from(so),
          stderr: Buffer.from(se),
          text: () => so,
          json: () => JSON.parse(so),
          toString: () => so,
        };
        if (err && !this._nothrow) reject(Object.assign(new Error(`Command failed (${out.exitCode}): ${this.cmd}\n${se}`), out));
        else resolve(out);
      });
    });
    return this._p;
  }
  // Intentionally thenable: `await $\`cmd\`` must work like Bun's shell.
  // biome-ignore lint/suspicious/noThenProperty: Bun $ compatibility
  then(a, b) {
    return this.run().then(a, b);
  }
  catch(b) {
    return this.run().catch(b);
  }
  text() {
    return this.run().then((r) => r.text());
  }
  json() {
    return this.run().then((r) => r.json());
  }
  lines() {
    return this.run().then((r) => r.text().split("\n").filter(Boolean));
  }
}
export const $ = (strings, ...vals) => {
  let cmd = "";
  strings.forEach((s, i) => {
    cmd += s;
    if (i < vals.length) {
      const v = vals[i];
      cmd += Array.isArray(v) ? v.map(shq).join(" ") : v && v.raw !== undefined ? v.raw : shq(v);
    }
  });
  return new ShellPromise(cmd);
};
$.cwd = () => $;
$.env = () => $;
$.nothrow = () => $;
$.throws = () => $;

// opencode `client`: calls resolve to empty data; app.log is forwarded.
export function makeClient(p = []) {
  return new Proxy(() => {}, {
    get: (_t, k) => (k === "then" || typeof k === "symbol" ? undefined : makeClient([...p, k])),
    apply: (_t, _this, a) => {
      if (p.join(".") === "app.log") log(a[0]?.body?.message ?? JSON.stringify(a[0]?.body ?? ""));
      return Promise.resolve({ data: {} });
    },
  });
}
