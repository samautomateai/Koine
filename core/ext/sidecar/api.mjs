// The object every exported function of an extension module receives: Pi's ExtensionAPI and opencode's PluginInput in one.
import fs from "node:fs";
import path from "node:path";
import { pathToFileURL } from "node:url";
import { execFile } from "node:child_process";
import { EventEmitter } from "node:events";
import { argsToSchema } from "./argschema.mjs";
import { $, makeClient } from "./shell.mjs";
import { degrade, inert, notify, state, textOf } from "./host.mjs";

// pi.events: a bus shared by all extensions. Delivery within this process is immediate; the host relays an emit to the other extensions.
const bus = new EventEmitter();
bus.setMaxListeners(0);
export const deliverBusEvent = (channel, data) => bus.emit(String(channel), data);
const events = {
  on(channel, fn) {
    const fail = (e) => notify("ext_error", { event: `events:${channel}`, error: String(e?.message ?? e) });
    const wrapped = (data) => {
      try {
        const r = fn(data);
        if (r && typeof r.catch === "function") r.catch(fail);
      } catch (e) {
        fail(e);
      }
    };
    bus.on(String(channel), wrapped);
    return () => bus.off(String(channel), wrapped);
  },
  emit(channel, data) {
    bus.emit(String(channel), data);
    notify("bus_emit", { channel: String(channel), data: data ?? null });
  },
};

// pi.sendMessage: a custom message for the conversation. Only plain text content travels.
function sendMessage(m, o) {
  if (!m || typeof m !== "object") return;
  notify("send_message", {
    customType: String(m.customType ?? ""),
    content: typeof m.content === "string" ? m.content : textOf(m.content),
    display: !!m.display,
    details: m.details ?? undefined,
    deliverAs: o?.deliverAs,
    triggerTurn: !!o?.triggerTurn,
  });
}

function makeApi() {
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
      notify("register_command", {
        name,
        description: opts.description ?? "",
        hasCompletions: typeof opts.getArgumentCompletions === "function",
      });
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
    sendMessage,
    sendUserMessage: (t, o) =>
      notify("send_user_message", { text: typeof t === "string" ? t : String(t?.text ?? t), deliverAs: o?.deliverAs }),
    appendEntry: (customType, data) => notify("append_entry", { customType: String(customType ?? ""), data: data ?? null }),
    setSessionName: inert("pi.setSessionName"),
    setLabel: inert("pi.setLabel"),
    getCommands: () => state.hostCommands.map((c) => ({ ...c })),
    getActiveTools: () => (state.hostTools.length ? state.hostTools.map((t) => t.name) : [...state.tools.keys()]),
    getAllTools: () => (state.hostTools.length ? state.hostTools.map((t) => t.name) : [...state.tools.keys()]),
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

export async function load() {
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
export function subscribed() {
  const set = new Set();
  // "context" carries Koine's message shape, which is not Pi's, so Pi handlers are never given it (gap row AGT-07)
  for (const k of state.handlers.keys()) if (k !== "context") set.add(k);
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
