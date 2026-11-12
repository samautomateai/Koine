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
import { answer, log, notify, state, textOf, write, SESSION } from "./host.mjs";
import { deliverBusEvent, load, subscribed } from "./api.mjs";
import { piCtx } from "./ctx.mjs";
import { onEvent } from "./events.mjs";

const here = path.dirname(fileURLToPath(import.meta.url));
const shimMap = JSON.parse(fs.readFileSync(path.join(here, "shims", "map.json"), "utf8"));

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

// ---- requests from the host ---------------------------------------------------
async function onToolExecute({ name, args, callId }, signal) {
  const t = state.tools.get(name);
  if (!t) throw new Error(`unknown tool ${name}`);
  if (t.style === "pi") {
    const onUpdate = (p) => {
      const text = typeof p === "string" ? p : textOf(p?.content ?? p);
      notify("tool_update", { callId: `${name}/${callId}`, text });
    };
    const r = await t.def.execute(callId, args, signal, onUpdate, piCtx(null, signal, true));
    return { content: typeof r === "string" ? r : textOf(r?.content ?? r), isError: !!r?.isError };
  }
  const r = await t.def.execute(args, {
    sessionID: SESSION,
    messageID: "",
    agent: "koine",
    directory: state.cwd,
    worktree: state.cwd,
    abort: signal,
    metadata() {},
  });
  return { content: typeof r === "string" ? r : (r?.output ?? JSON.stringify(r)) };
}

async function onCommand({ name, args }, signal) {
  const c = state.commands.get(name);
  if (!c) throw new Error(`unknown command ${name}`);
  // outside RPC mode what the command notifies is the command's output; RPC clients get each notification as a UI request
  const sink = state.mode === "rpc" ? null : [];
  const r = await c.handler(args ?? "", piCtx(sink, signal));
  if (typeof r === "string") sink?.push(r);
  return { output: sink ? sink.join("\n") : "" };
}

// Argument completion for /name: items are { value, label }; a command without completions, or one that fails, offers none.
async function onComplete({ name, prefix }) {
  const c = state.commands.get(name);
  if (typeof c?.getArgumentCompletions !== "function") return { items: [] };
  let items;
  try {
    items = await c.getArgumentCompletions(String(prefix ?? ""));
  } catch {
    return { items: [] };
  }
  if (!Array.isArray(items)) return { items: [] };
  const out = [];
  for (const i of items) {
    if (typeof i === "string") out.push({ value: i, label: i });
    else if (typeof i?.value === "string") out.push({ value: i.value, label: String(i.label ?? i.value) });
  }
  return { items: out };
}

const methods = {
  async initialize(p) {
    if (p?.cwd) state.cwd = p.cwd;
    state.trusted = !!p?.trusted;
    state.mode = typeof p?.mode === "string" ? p.mode : "print";
    state.hasUI = !!p?.hasUI;
    await load();
    return { name: path.basename(state.target), events: subscribed() };
  },
  event: onEvent,
  "tool.execute": onToolExecute,
  "command.execute": onCommand,
  "command.complete": onComplete,
};

// notifications from the host
const notifications = {
  shutdown: () => process.exit(0),
  flags: (p) => Object.assign(state.flagValues, p?.values ?? {}),
  bus_event: (p) => deliverBusEvent(p?.channel, p?.data),
  host_state: (p) => {
    if (Array.isArray(p?.commands)) state.hostCommands = p.commands;
    if (Array.isArray(p?.tools)) state.hostTools = p.tools;
  },
  cancel: (p) => inflight.get(p?.id)?.abort(),
};

// ---- main loop --------------------------------------------------------------
const inflight = new Map(); // id of a host request being served -> its AbortController

async function serve(m, fn) {
  const ac = new AbortController();
  inflight.set(m.id, ac);
  try {
    write({ id: m.id, result: (await fn(m.params ?? {}, ac.signal)) ?? {} });
  } catch (e) {
    write({ id: m.id, error: { code: -32000, message: String(e?.message ?? e) } });
  } finally {
    inflight.delete(m.id);
  }
}

const rl = readline.createInterface({ input: process.stdin });
rl.on("line", (line) => {
  if (!line.trim()) return;
  let m;
  try {
    m = JSON.parse(line);
  } catch {
    return;
  }
  if (m.method === undefined) {
    if (m.id !== undefined) answer(m);
    return;
  }
  if (m.id === undefined) {
    notifications[m.method]?.(m.params);
    return;
  }
  const fn = methods[m.method];
  if (!fn) return write({ id: m.id, error: { code: -32601, message: `method not found: ${m.method}` } });
  serve(m, fn);
});
rl.on("close", () => process.exit(0));
process.on("unhandledRejection", (e) => log(`unhandled rejection: ${e?.message ?? e}`, "error"));
