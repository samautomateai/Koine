// Plumbing shared by every part of the sidecar: the JSON-RPC channel to the host, requests the extension sends to the host,
// the state of the loaded module and the compatibility stand-ins.
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const here = path.dirname(fileURLToPath(import.meta.url));

// stdout is the protocol channel; anything a plugin prints goes to stderr.
export const proto = process.stdout;
console.log = console.info = console.debug = (...a) => process.stderr.write(a.map(String).join(" ") + "\n");

export const SESSION = "koine-session";

// ---- JSON-RPC plumbing ------------------------------------------------------
export const write = (o) => proto.write(JSON.stringify({ jsonrpc: "2.0", ...o }) + "\n");
export const notify = (method, params) => write({ method, params });
export const log = (message, level = "info") => notify("log", { level, message: String(message) });

// A request from the extension to the host (dialogs, nested tool calls). It rejects when the host answers with an error.
let nextRequest = 0;
const pending = new Map();
export function request(method, params) {
  const id = ++nextRequest;
  return new Promise((resolve, reject) => {
    pending.set(id, { resolve, reject });
    write({ id, method, params });
  });
}
// The host's answer to a request made above. It reports whether the message was one.
export function answer(m) {
  const p = pending.get(m.id);
  if (!p) return false;
  pending.delete(m.id);
  if (m.error) p.reject(new Error(m.error.message ?? "host error"));
  else p.resolve(m.result ?? {});
  return true;
}

// ---- compatibility reporting ---------------------------------------------
// Unknown members of the Pi API object, ctx and ctx.ui never throw: they return
// a callable, chainable stand-in and are reported once to the host (/compat),
// so an extension that touches a newer Pi API still loads and mostly works.
const reported = new Set();
export function report(kind, api) {
  if (reported.has(api)) return;
  reported.add(api);
  notify("compat", { kind, api });
}
export const inert =
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

export function degrade(obj, prefix) {
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

export const state = {
  trusted: false,
  mode: "print", // tui | rpc | json | print: what the host runs as (ctx.mode)
  hasUI: false, // a frontend can answer dialogs (ctx.hasUI)
  cwd: process.env.KOINE_CWD || process.cwd(),
  handlers: new Map(), // Pi: event -> [fn]
  oc: [], // opencode hook objects
  tools: new Map(), // name -> {style, def}
  commands: new Map(),
  flags: new Map(), // name -> {type, description, default}, from pi.registerFlag
  flagValues: {}, // command-line values, sent by the host once every extension has loaded
  hostCommands: [], // every slash command, as the host last listed them (pi.getCommands)
  hostTools: [], // every tool, as the host last listed them
  target: process.argv[2] || "",
};

// The text of Pi content blocks (or of a plain value).
export function textOf(content) {
  if (!Array.isArray(content)) return String(content ?? "");
  const parts = [];
  for (const c of content) if (typeof c?.text === "string") parts.push(c.text);
  return parts.join("\n");
}
