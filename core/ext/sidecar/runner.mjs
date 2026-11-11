// koine JS sidecar: loads a Pi extension or opencode plugin and speaks the koine
// extension protocol (newline-delimited JSON-RPC 2.0) on stdin/stdout.
//
// One module can be both: every exported function is invoked with an object that
// satisfies Pi's ExtensionAPI *and* opencode's PluginInput. If the call returns
// an object, it is treated as opencode hooks.
import readline from "node:readline";
import fs from "node:fs";
import path from "node:path";
import { pathToFileURL, fileURLToPath } from "node:url";
import { execFile } from "node:child_process";
import { EventEmitter } from "node:events";

const here = path.dirname(fileURLToPath(import.meta.url));
const shimMap = JSON.parse(fs.readFileSync(path.join(here, "shims", "map.json"), "utf8"));

// stdout is the protocol channel; anything a plugin prints goes to stderr.
const proto = process.stdout;
console.log = console.info = console.debug = (...a) => process.stderr.write(a.map(String).join(" ") + "\n");

// ---- Bun: expose API packages as virtual modules (Node uses loader.mjs) -----
// Registered before any extension is imported. Subpath imports ("zod/v4") are
// listed explicitly in map.json.
if (globalThis.Bun) {
  const cwd = process.env.KOINE_CWD || process.cwd();
  const installed = (spec) => {
    try {
      globalThis.Bun.resolveSync(spec, cwd);
      return true;
    } catch {
      return false;
    }
  };
  globalThis.Bun.plugin({
    name: "koine-shims",
    setup(build) {
      const add = (spec, file) =>
        build.module(spec, async () => ({ exports: await import(pathToFileURL(path.join(here, "shims", file)).href), loader: "object" }));
      for (const [spec, file] of Object.entries(shimMap.api)) add(spec, file);
      for (const [spec, file] of Object.entries(shimMap.fallback)) if (!installed(spec)) add(spec, file);
    },
  });
}

// ---- JSON-RPC plumbing ------------------------------------------------------
const write = (o) => proto.write(JSON.stringify({ jsonrpc: "2.0", ...o }) + "\n");
const notify = (method, params) => write({ method, params });
const log = (message, level = "info") => notify("log", { level, message: String(message) });

const SESSION = "koine-session";

// ---- compatibility reporting ---------------------------------------------
// Unknown members of the Pi API object, ctx and ctx.ui never throw: they return
// a callable, chainable stand-in and are reported once to the host (/compat),
// so an extension that touches a newer Pi API still loads and mostly works.
const reported = new Set();
function report(kind, api) {
  if (reported.has(api)) return;
  reported.add(api);
  notify("compat", { kind, api });
}
const inert =
  (api, ret) =>
  (...a) => {
    report("inert", api);
    return typeof ret === "function" ? ret(...a) : ret;
  };
function standIn(api) {
  const f = () => {};
  return new Proxy(f, {
    get: (t, k) => {
      if (k === "call" || k === "apply" || k === "bind") return Reflect.get(t, k); // stand-ins stay callable the normal ways
      return k === "then" || k === "toJSON" || typeof k === "symbol" ? undefined : standIn(`${api}.${String(k)}`);
    },
    apply: () => {
      report("unsupported", api);
      return undefined;
    },
  });
}
// Which unimplemented members are *properties* (not methods) comes from the compat
// manifest via surface.json: those evaluate to undefined (or, for ui.theme, to an
// object whose helpers return their text unstyled) instead of a callable stand-in.
const surface = (() => {
  try {
    return JSON.parse(fs.readFileSync(path.join(here, "surface.json"), "utf8"));
  } catch {
    return {};
  }
})();
const undefinedProps = new Set(surface.undefinedProps ?? []);
const identityProps = new Set(surface.identityProps ?? []);
const identityObject = new Proxy({}, { get: (_t, k) => (typeof k === "symbol" || k === "then" ? undefined : (...a) => a[a.length - 1]) });

function degrade(obj, prefix) {
  return new Proxy(obj, {
    get(t, k, r) {
      if (k in t || typeof k === "symbol" || k === "then" || k === "toJSON") return Reflect.get(t, k, r);
      const full = `${prefix}.${String(k)}`;
      if (undefinedProps.has(full)) return undefined;
      if (identityProps.has(full)) return identityObject;
      return standIn(full);
    },
  });
}

const state = {
  trusted: false,
  cwd: process.env.KOINE_CWD || process.cwd(),
  handlers: new Map(), // Pi: event -> [fn]
  oc: [], // opencode hook objects
  tools: new Map(), // name -> {style, def}
  commands: new Map(),
  flags: new Map(), // name -> {type, description, default}, from pi.registerFlag
  flagValues: {}, // command-line values, sent by the host once every extension has loaded
  target: process.argv[2] || "",
};

// ---- schema helpers ---------------------------------------------------------
let zodMod;
async function loadZod() {
  if (zodMod !== undefined) return zodMod;
  try {
    zodMod = await import("zod");
  } catch {
    zodMod = null;
  }
  return zodMod;
}
async function schemaOf(v) {
  if (v && typeof v.toJSON === "function") return v.toJSON();
  const z = await loadZod();
  if (v && z && typeof z.toJSONSchema === "function") {
    try {
      const rest = { ...z.toJSONSchema(v) };
      delete rest.$schema;
      return rest;
    } catch {}
  }
  return {};
}
async function argsToSchema(args) {
  if (!args) return { type: "object", properties: {} };
  const properties = {};
  const required = [];
  for (const [k, v] of Object.entries(args)) {
    properties[k] = await schemaOf(v);
    const opt = v && (v.isOptional === true || (typeof v.isOptional === "function" && v.isOptional()));
    if (!opt) required.push(k);
  }
  const s = { type: "object", properties };
  if (required.length) s.required = required;
  return s;
}

// ---- host-facing shell helper ($ from opencode) -----------------------------
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
const $ = (strings, ...vals) => {
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
function makeClient(p = []) {
  return new Proxy(() => {}, {
    get: (_t, k) => (k === "then" || typeof k === "symbol" ? undefined : makeClient([...p, k])),
    apply: (_t, _this, a) => {
      if (p.join(".") === "app.log") log(a[0]?.body?.message ?? JSON.stringify(a[0]?.body ?? ""));
      return Promise.resolve({ data: {} });
    },
  });
}

// ---- Pi context / API -------------------------------------------------------
function piCtx(sink) {
  const note = (m) => (sink ? sink.push(String(m)) : log(m));
  const ui = degrade(
    {
      notify: note,
      setStatus: inert("ctx.ui.setStatus"),
      setWidget: inert("ctx.ui.setWidget"),
      setTitle: inert("ctx.ui.setTitle"),
      setFooter: inert("ctx.ui.setFooter"),
      setHeader: inert("ctx.ui.setHeader"),
      setWorkingMessage: inert("ctx.ui.setWorkingMessage"),
      setWorkingIndicator: inert("ctx.ui.setWorkingIndicator"),
      setWorkingVisible: inert("ctx.ui.setWorkingVisible"),
      confirm: inert("ctx.ui.confirm", false),
      select: inert("ctx.ui.select"),
      input: inert("ctx.ui.input"),
      editor: inert("ctx.ui.editor"),
    },
    "ctx.ui",
  );
  return degrade(
    {
      cwd: state.cwd,
      hasUI: false,
      mode: "print",
      ui,
      isProjectTrusted: () => state.trusted,
      sessionManager: { getEntries: () => [], getBranch: () => [] },
      isIdle: () => true,
      abort: inert("ctx.abort"),
      hasPendingMessages: () => false,
      shutdown: inert("ctx.shutdown"),
      getContextUsage: inert("ctx.getContextUsage"),
      compact: inert("ctx.compact"),
      getSystemPrompt: inert("ctx.getSystemPrompt", ""),
    },
    "ctx",
  );
}

function makeApi() {
  const events = new EventEmitter();
  const pi = {
    on(ev, fn) {
      (state.handlers.get(ev) ?? state.handlers.set(ev, []).get(ev)).push(fn);
    },
    registerTool(def) {
      state.tools.set(def.name, { style: "pi", def });
      notify("register_tool", {
        name: def.name,
        description: def.description ?? def.label ?? "",
        schema: def.parameters ?? { type: "object", properties: {} },
        promptSnippet: typeof def.promptSnippet === "string" ? def.promptSnippet : undefined,
        promptGuidelines: Array.isArray(def.promptGuidelines) ? def.promptGuidelines.filter((g) => typeof g === "string") : undefined,
      });
    },
    registerCommand(name, opts = {}) {
      state.commands.set(name, opts);
      notify("register_command", { name, description: opts.description ?? "" });
    },
    registerShortcut: inert("pi.registerShortcut"),
    registerFlag(name, opts = {}) {
      if (typeof name !== "string" || !name) return;
      const def = {
        type: opts?.type === "string" ? "string" : "boolean",
        description: String(opts?.description ?? ""),
        default: opts?.default,
      };
      state.flags.set(name, def);
      notify("register_flag", { name, ...def });
    },
    registerMessageRenderer: inert("pi.registerMessageRenderer"),
    registerProvider: inert("pi.registerProvider"),
    unregisterProvider: inert("pi.unregisterProvider"),
    getFlag: (name) => (name in state.flagValues ? state.flagValues[name] : state.flags.get(name)?.default),
    sendMessage: (m) => {
      report("inert", "pi.sendMessage");
      log(typeof m === "string" ? m : (m?.content ?? JSON.stringify(m)));
    },
    sendUserMessage: (t, o) =>
      notify("send_user_message", { text: typeof t === "string" ? t : String(t?.text ?? t), deliverAs: o?.deliverAs }),
    appendEntry: (customType, data) => notify("append_entry", { customType: String(customType ?? ""), data: data ?? null }),
    setSessionName: inert("pi.setSessionName"),
    setLabel: inert("pi.setLabel"),
    getActiveTools: () => [...state.tools.keys()],
    getAllTools: () => [...state.tools.keys()],
    setActiveTools: inert("pi.setActiveTools"),
    exec: (command, args = [], o = {}) =>
      new Promise((resolve) =>
        execFile(
          command,
          args,
          { cwd: o.cwd ?? state.cwd, timeout: o.timeout, signal: o.signal, maxBuffer: 64 << 20 },
          (err, stdout, stderr) =>
            resolve({ stdout, stderr, code: err ? (typeof err.code === "number" ? err.code : 1) : 0, killed: !!err?.killed }),
        ),
      ),
    events,
  };
  // opencode PluginInput fields on the same object
  return degrade(
    Object.assign(pi, {
      project: { id: "global", worktree: state.cwd, path: state.cwd },
      directory: state.cwd,
      worktree: state.cwd,
      client: makeClient(),
      $,
      serverUrl: new URL("http://localhost:0"),
    }),
    "pi",
  );
}

// ---- loading ----------------------------------------------------------------
function resolvePkg(spec, cwd) {
  const dir = path.join(cwd, "node_modules", spec);
  const pj = JSON.parse(fs.readFileSync(path.join(dir, "package.json"), "utf8"));
  let entry = pj.module || pj.main || "index.js";
  const ex = pj.exports;
  if (ex) {
    const root = typeof ex === "string" ? ex : (ex["."] ?? ex);
    const pick = (v) => (typeof v === "string" ? v : v && (pick(v.import) || pick(v.default) || pick(v.node)));
    entry = pick(root) || entry;
  }
  return path.join(dir, entry);
}

async function registerOpencode(h) {
  state.oc.push(h);
  for (const [name, def] of Object.entries(h.tool ?? {})) {
    state.tools.set(name, { style: "oc", def });
    notify("register_tool", { name, description: def.description ?? "", schema: await argsToSchema(def.args) });
  }
}

async function load() {
  let file = state.target;
  if (file.startsWith("pkg:")) file = resolvePkg(file.slice(4), state.cwd);
  const mod = await import(pathToFileURL(path.resolve(file)).href);
  const fns = [];
  if (typeof mod.default === "function") fns.push(mod.default);
  for (const [k, v] of Object.entries(mod)) if (k !== "default" && typeof v === "function") fns.push(v);
  if (fns.length === 0) throw new Error(`${file}: no exported function (expected a Pi extension or opencode plugin)`);
  for (const fn of fns) {
    const r = await fn(makeApi());
    if (r && typeof r === "object") await registerOpencode(r);
  }
}

// events this module can react to (host skips forwarding everything else)
function subscribed() {
  const set = new Set();
  const piToHost = { before_agent_start: "system_prompt" };
  for (const k of state.handlers.keys()) set.add(piToHost[k] ?? k);
  const ocToHost = {
    "tool.execute.before": "tool_call",
    "tool.execute.after": "tool_result",
    "chat.message": "input",
    "experimental.chat.system.transform": "system_prompt",
  };
  for (const h of state.oc) {
    for (const [k, v] of Object.entries(ocToHost)) if (typeof h[k] === "function") set.add(v);
    if (typeof h.event === "function") for (const e of ["session_start", "agent_end"]) set.add(e);
  }
  return [...set];
}

// ---- event dispatch ---------------------------------------------------------
const textOf = (content) => (Array.isArray(content) ? content.map((c) => c?.text ?? "").join("\n") : String(content ?? ""));

async function piEvent(name, p, d) {
  const alias = name === "system_prompt" ? "before_agent_start" : name;
  const hs = state.handlers.get(alias);
  if (!hs) return;
  let ev;
  switch (name) {
    case "tool_call":
      ev = { type: "tool_call", toolName: p.toolName, toolCallId: p.callId, input: p.input };
      break;
    case "tool_result":
      ev = {
        type: "tool_result",
        toolName: p.toolName,
        toolCallId: p.callId,
        input: p.input,
        content: [{ type: "text", text: p.output }],
        isError: !!p.isError,
        details: {},
      };
      break;
    case "input":
      ev = { type: "input", text: p.text, source: "interactive" };
      break;
    case "system_prompt":
      ev = { type: "before_agent_start", prompt: "", systemPrompt: p.system };
      break;
    default:
      ev = { type: name, ...p };
  }
  for (const h of hs) {
    let r;
    try {
      r = await h(ev, piCtx());
    } catch (e) {
      log(`${name} handler: ${e?.message ?? e}`, "error");
      continue;
    }
    if (!r || typeof r !== "object") continue;
    if (name === "tool_call" && r.block) {
      d.block = true;
      d.reason = r.reason ?? "blocked";
      return;
    }
    if (name === "tool_result" && r.content !== undefined) d.output = textOf(r.content);
    if (name === "input") {
      if (r.action === "handled") {
        d.block = true;
        d.reason = "handled by extension";
        return;
      }
      if (r.action === "transform" && typeof r.text === "string") d.text = r.text;
    }
    if (name === "system_prompt" && typeof r.systemPrompt === "string") {
      d.systemAppend = r.systemPrompt.startsWith(p.system) ? r.systemPrompt.slice(p.system.length).trim() : r.systemPrompt;
    }
  }
}

const OC_EVENT = { session_start: "session.created", agent_end: "session.idle" };

async function ocEvent(name, p, d) {
  for (const h of state.oc) {
    try {
      if (name === "tool_call" && h["tool.execute.before"]) {
        const output = { args: structuredClone(p.input ?? {}) };
        try {
          await h["tool.execute.before"]({ tool: p.toolName, sessionID: SESSION, callID: p.callId }, output);
        } catch (e) {
          d.block = true;
          d.reason = String(e?.message ?? e);
          return;
        }
        if (JSON.stringify(output.args) !== JSON.stringify(p.input)) d.args = output.args;
      } else if (name === "tool_result" && h["tool.execute.after"]) {
        const output = { title: p.toolName, output: p.output, metadata: {} };
        await h["tool.execute.after"]({ tool: p.toolName, sessionID: SESSION, callID: p.callId }, output);
        if (output.output !== p.output) d.output = String(output.output);
      } else if (name === "input" && h["chat.message"]) {
        const output = { message: { role: "user" }, parts: [{ type: "text", text: p.text }] };
        await h["chat.message"]({ sessionID: SESSION }, output);
        const t = output.parts?.[0]?.text;
        if (typeof t === "string" && t !== p.text) d.text = t;
      } else if (name === "system_prompt" && h["experimental.chat.system.transform"]) {
        const output = { system: [] };
        await h["experimental.chat.system.transform"]({}, output);
        if (output.system.length) d.systemAppend = output.system.join("\n");
      }
      if (OC_EVENT[name] && typeof h.event === "function") await h.event({ event: { type: OC_EVENT[name], properties: {} } });
    } catch (e) {
      log(`opencode hook (${name}): ${e?.message ?? e}`, "error");
    }
  }
}

async function onEvent({ name, payload }) {
  const d = {};
  await piEvent(name, payload ?? {}, d);
  if (!d.block) await ocEvent(name, payload ?? {}, d);
  return d;
}

async function onToolExecute({ name, args, callId }) {
  const t = state.tools.get(name);
  if (!t) throw new Error(`unknown tool ${name}`);
  if (t.style === "pi") {
    const onUpdate = (p) =>
      notify("tool_update", { callId: `${name}/${callId}`, text: typeof p === "string" ? p : textOf(p?.content ?? p) });
    const r = await t.def.execute(callId, args, new AbortController().signal, onUpdate, piCtx());
    return { content: typeof r === "string" ? r : textOf(r?.content ?? r), isError: !!r?.isError };
  }
  const r = await t.def.execute(args, {
    sessionID: SESSION,
    messageID: "",
    agent: "koine",
    directory: state.cwd,
    worktree: state.cwd,
    abort: new AbortController().signal,
    metadata() {},
  });
  return { content: typeof r === "string" ? r : (r?.output ?? JSON.stringify(r)) };
}

async function onCommand({ name, args }) {
  const c = state.commands.get(name);
  if (!c) throw new Error(`unknown command ${name}`);
  const sink = [];
  const r = await c.handler(args ?? "", piCtx(sink));
  if (typeof r === "string") sink.push(r);
  return { output: sink.join("\n") };
}

// ---- main loop --------------------------------------------------------------
const methods = {
  async initialize(p) {
    if (p?.cwd) state.cwd = p.cwd;
    state.trusted = !!p?.trusted;
    await load();
    return { name: path.basename(state.target), events: subscribed() };
  },
  event: onEvent,
  "tool.execute": onToolExecute,
  "command.execute": onCommand,
};

const rl = readline.createInterface({ input: process.stdin });
rl.on("line", async (line) => {
  if (!line.trim()) return;
  let m;
  try {
    m = JSON.parse(line);
  } catch {
    return;
  }
  if (m.method === "shutdown") process.exit(0);
  if (m.method === "flags") {
    Object.assign(state.flagValues, m.params?.values ?? {});
    return;
  }
  const fn = methods[m.method];
  if (m.id === undefined) return;
  if (!fn) return write({ id: m.id, error: { code: -32601, message: `method not found: ${m.method}` } });
  try {
    write({ id: m.id, result: (await fn(m.params ?? {})) ?? {} });
  } catch (e) {
    write({ id: m.id, error: { code: -32000, message: String(e?.message ?? e) } });
  }
});
rl.on("close", () => process.exit(0));
process.on("unhandledRejection", (e) => log(`unhandled rejection: ${e?.message ?? e}`, "error"));
