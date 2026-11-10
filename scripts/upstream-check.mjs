#!/usr/bin/env node
// Diff upstream Pi's real extension surface against compat/pi.json.
//
//   node scripts/upstream-check.mjs --src <dir>        a checkout (packages/coding-agent/...) or unpacked npm package
//   node scripts/upstream-check.mjs --tarball f.tgz    an `npm pack` tarball
//   node scripts/upstream-check.mjs                    `npm pack <package>@latest` (needs network)
//   add --rebase to rewrite compat/pi.json: new surface is added as `unsupported`, baseline moves
//   add --json for machine output
//
// Exit codes: 0 no drift · 1 drift found · 3 extraction failed (upstream renamed an interface?)
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { execFileSync } from "node:child_process";
import { fileURLToPath, pathToFileURL } from "node:url";

// ---- TypeScript-source scanning (works on .ts and .d.ts) ---------------------

// Remove line and block comments, keeping string literals intact.
export function stripComments(src) {
  let out = "";
  for (let i = 0; i < src.length; i++) {
    const c = src[i],
      n = src[i + 1];
    if (c === '"' || c === "'" || c === "`") {
      let j = i + 1;
      while (j < src.length && src[j] !== c) j += src[j] === "\\" ? 2 : 1;
      out += src.slice(i, j + 1);
      i = j;
    } else if (c === "/" && n === "/") {
      while (i < src.length && src[i] !== "\n") i++;
      out += "\n";
    } else if (c === "/" && n === "*") {
      i = src.indexOf("*/", i + 2);
      if (i < 0) break;
      i++;
    } else out += c;
  }
  return out;
}

/** The text between the braces of `interface <name> ... { ... }`, or null. */
export function interfaceBody(src, name) {
  const m = new RegExp(`\\binterface\\s+${name}\\b[^{]*\\{`).exec(src);
  if (!m) return null;
  let depth = 1,
    i = m.index + m[0].length;
  const start = i;
  for (; i < src.length && depth > 0; i++) {
    const c = src[i];
    if (c === '"' || c === "'" || c === "`") {
      i++;
      while (i < src.length && src[i] !== c) i += src[i] === "\\" ? 2 : 1;
    } else if (c === "{") depth++;
    else if (c === "}") depth--;
  }
  return depth === 0 ? src.slice(start, i - 1) : null;
}

/** Top-level members of an interface body: [{name, shape: "method"|"prop"}], overloads merged. */
export function members(body) {
  const chunks = [];
  let depth = 0,
    cur = "";
  for (let i = 0; i < body.length; i++) {
    const c = body[i];
    if (c === '"' || c === "'" || c === "`") {
      let j = i + 1;
      while (j < body.length && body[j] !== c) j += body[j] === "\\" ? 2 : 1;
      cur += body.slice(i, j + 1);
      i = j;
      continue;
    }
    if (c === "{" || c === "(" || c === "[") depth++;
    else if (c === "}" || c === ")" || c === "]") depth--;
    if (c === ";" && depth === 0) {
      chunks.push(cur);
      cur = "";
    } else cur += c;
  }
  chunks.push(cur);
  const seen = new Map();
  for (const ch of chunks) {
    const m = /^\s*(?:readonly\s+)?(?:(?:get|set)\s+)?([A-Za-z_$][\w$]*)\s*\??\s*([(<:])/.exec(ch);
    if (!m) continue; // call/index signatures, blank
    const rest = ch.slice(m[0].length).trimStart();
    const shape = m[2] === ":" && !rest.startsWith("(") ? "prop" : "method";
    if (!seen.has(m[1]) || shape === "method") seen.set(m[1], shape);
  }
  return [...seen].map(([name, shape]) => ({ name, shape }));
}

/** Event names from `on(event: "x", …)` overloads (single- or multi-line). */
export function onEvents(body) {
  return [...new Set([...body.matchAll(/\bon\(\s*event:\s*"([^"]+)"/g)].map((m) => m[1]))];
}

/** Names from the BUILTIN_SLASH_COMMANDS array literal. */
export function slashCommands(src) {
  const i = src.indexOf("BUILTIN_SLASH_COMMANDS");
  if (i < 0) return null;
  const open = src.indexOf("[", src.indexOf("=", i));
  const close = src.indexOf("\n];", open);
  if (open < 0 || close < 0) return null;
  return [...src.slice(open, close).matchAll(/\bname:\s*"([^"]+)"/g)].map((m) => m[1]);
}

// ---- surface extraction -------------------------------------------------------

const cfgDefault = {
  api: ["ExtensionAPI"],
  ctx: ["ExtensionContext", "ExtensionToolContext", "ExtensionCommandContext"],
  ui: ["ExtensionUIContext"],
  tool: ["ToolDefinition"],
};
const prefix = { api: "pi.", ctx: "ctx.", ui: "ctx.ui.", tool: "" };

export function extract(files, cfg = cfgDefault) {
  const missing = [];
  const surface = { event: new Map(), api: new Map(), ctx: new Map(), ui: new Map(), tool: new Map(), command: new Map() };
  const clean = files.map((f) => ({ ...f, text: stripComments(f.text) }));
  for (const kind of ["api", "ctx", "ui", "tool"]) {
    for (const iface of cfg[kind] ?? cfgDefault[kind]) {
      let found = false;
      for (const f of clean) {
        const body = interfaceBody(f.text, iface);
        if (body == null) continue;
        found = true;
        for (const mem of members(body)) surface[kind].set(prefix[kind] + mem.name, mem.shape);
        if (kind === "api") for (const e of onEvents(body)) surface.event.set(e, "event");
      }
      if (!found) missing.push(iface);
    }
  }
  for (const f of clean) {
    const cmds = slashCommands(f.text);
    if (cmds) for (const c of cmds) surface.command.set("/" + c, "command");
  }
  if (surface.command.size === 0) missing.push("BUILTIN_SLASH_COMMANDS");
  if (surface.event.size === 0) missing.push("ExtensionAPI.on(event: …) overloads");
  return { surface, missing };
}

const CHECKED = ["event", "api", "ctx", "ui", "tool", "command"];

/** Compare upstream surface with the manifest items. */
export function diff(surface, manifest) {
  const added = {},
    removed = [];
  for (const kind of CHECKED) {
    const have = new Set(manifest.items.filter((i) => i.kind === kind).map((i) => i.name));
    added[kind] = [...surface[kind].keys()].filter((n) => !have.has(n)).sort();
    for (const it of manifest.items) {
      // result.* fields and dynamic commands such as /skill:name are hand-tracked, not declared in the extracted interfaces
      if (it.kind !== kind || it.name.startsWith("result.") || (kind === "command" && it.name.includes(":"))) continue;
      if (!surface[kind].has(it.name)) removed.push({ kind, name: it.name, status: it.status });
    }
  }
  return { added, removed };
}

/** Apply a rebase: new surface becomes `unsupported`, vanished items are flagged, never deleted. */
export function rebase(manifest, surface, version, source) {
  const m = structuredClone(manifest);
  const from = m.upstream.baseline;
  const d = diff(surface, m);
  for (const kind of CHECKED) {
    for (const name of d.added[kind]) {
      const it = { kind, name, status: "unsupported", note: `new upstream surface since ${from}; triage me` };
      const shape = surface[kind].get(name);
      if (shape === "method" || shape === "prop") it.shape = shape;
      m.items.push(it);
    }
  }
  for (const r of d.removed) {
    const it = m.items.find((i) => i.kind === r.kind && i.name === r.name);
    it.removedUpstream = version;
  }
  for (const it of m.items) if (it.removedUpstream && surface[it.kind]?.has(it.name)) delete it.removedUpstream;
  m.upstream.baseline = version;
  m.upstream.baselineSource = source;
  return m;
}

/** Property behaviour the runner must apply, derived from the manifest (single source of truth). */
export function surfaceFile(manifest) {
  const props = manifest.items.filter((i) => ["api", "ctx", "ui"].includes(i.kind) && i.shape === "prop");
  return {
    generatedFrom: "compat/pi.json — do not edit; run `task compat:sync`",
    undefinedProps: props
      .filter((i) => i.status === "unsupported" && (i.behavior ?? "undefined") === "undefined")
      .map((i) => i.name)
      .sort(),
    identityProps: props
      .filter((i) => i.behavior === "identity")
      .map((i) => i.name)
      .sort(),
  };
}

export const surfacePath = (manifestPath) => path.join(path.dirname(manifestPath), "..", "core", "ext", "sidecar", "surface.json");

// ---- package / source discovery ----------------------------------------------

function listSources(dir) {
  const out = [];
  (function walk(d, depth) {
    if (depth > 12 || out.length > 5000) return;
    for (const e of fs.readdirSync(d, { withFileTypes: true })) {
      if (e.name === "node_modules" || e.name === ".git" || e.name === "test" || e.name === "tests") continue;
      const p = path.join(d, e.name);
      if (e.isDirectory()) walk(p, depth + 1);
      else if (/\.(d\.)?ts$/.test(e.name) && !/\.(test|spec)\.ts$/.test(e.name)) out.push(p);
    }
  })(dir, 0);
  return out;
}

export function readSource(dir) {
  return listSources(dir)
    .map((p) => ({ path: p, text: fs.readFileSync(p, "utf8") }))
    .filter((f) => /interface (Extension|ToolDefinition)|BUILTIN_SLASH_COMMANDS/.test(f.text));
}

export function findVersion(dir, pkgName) {
  const cands = ["package.json", "package/package.json", "packages/coding-agent/package.json"].map((p) => path.join(dir, p));
  for (const c of cands) {
    try {
      const j = JSON.parse(fs.readFileSync(c, "utf8"));
      if (!pkgName || j.name === pkgName) return { name: j.name, version: j.version };
    } catch {}
  }
  return null;
}

// ---- CLI ------------------------------------------------------------------------

export function report({ version, name, baseline, d, missing }) {
  const lines = [`# Upstream drift: ${name ?? "?"} ${version ?? "?"} vs baseline ${baseline}`, ""];
  if (missing.length) lines.push(`**Extraction problems** (upstream may have renamed these): ${missing.join(", ")}`, "");
  let n = 0;
  for (const kind of CHECKED) {
    if (d.added[kind].length) {
      lines.push(`## New upstream ${kind} (${d.added[kind].length})`, d.added[kind].map((x) => `- \`${x}\``).join("\n"), "");
      n += d.added[kind].length;
    }
  }
  if (d.removed.length) {
    lines.push(
      `## In manifest but not upstream (${d.removed.length}) — renamed or removed?`,
      d.removed.map((r) => `- ${r.kind} \`${r.name}\` (koine status: ${r.status})`).join("\n"),
      "",
    );
    n += d.removed.length;
  }
  if (!n && baseline === version && !missing.length) lines.push("No drift.");
  else if (!n) lines.push(`Surface unchanged; only the version moved (${baseline} → ${version}). Run with --rebase to record it.`);
  return lines.join("\n");
}

async function main(argv) {
  const arg = (k) => {
    const i = argv.indexOf(k);
    return i >= 0 ? argv[i + 1] : undefined;
  };
  const here = path.dirname(fileURLToPath(import.meta.url));
  const manifestPath = arg("--manifest") ?? path.join(here, "..", "compat", "pi.json");
  const manifest = JSON.parse(fs.readFileSync(manifestPath, "utf8"));
  if (argv.includes("--sync-surface")) {
    fs.writeFileSync(surfacePath(manifestPath), JSON.stringify(surfaceFile(manifest), null, 1) + "\n");
    console.error("wrote " + surfacePath(manifestPath));
    return 0;
  }
  let dir = arg("--src");
  let tmp;
  if (!dir) {
    tmp = fs.mkdtempSync(path.join(os.tmpdir(), "koine-upstream-"));
    let tgz = arg("--tarball");
    if (!tgz) {
      const out = execFileSync("npm", ["pack", `${manifest.upstream.package}@latest`, "--pack-destination", tmp, "--silent"], {
        encoding: "utf8",
      })
        .trim()
        .split("\n")
        .pop();
      tgz = path.join(tmp, out);
    }
    execFileSync("tar", ["-xzf", tgz, "-C", tmp]);
    dir = tmp;
  }
  const v = findVersion(dir, manifest.upstream.package) ?? findVersion(dir, manifest.upstream.legacyPackage) ?? findVersion(dir);
  const { surface, missing } = extract(
    readSource(dir),
    manifest.extract && Object.keys(manifest.extract).some((k) => ["api", "ctx", "ui"].includes(k))
      ? { ...cfgDefault, ...manifest.extract }
      : cfgDefault,
  );
  if (missing.length && missing.includes("ExtensionAPI")) {
    console.error(`extraction failed: could not find ${missing.join(", ")} in ${dir}`);
    return 3;
  }
  const d = diff(surface, manifest);
  const baseline = manifest.upstream.baseline;
  if (argv.includes("--json"))
    console.log(JSON.stringify({ version: v?.version, baseline, added: d.added, removed: d.removed, missing }, null, 1));
  else console.log(report({ version: v?.version, name: v?.name, baseline, d, missing }));
  if (argv.includes("--rebase")) {
    if (!v) {
      console.error("cannot rebase: upstream version not found");
      return 3;
    }
    const next = rebase(
      manifest,
      surface,
      v.version,
      `extracted from ${v.name} ${v.version} (types.ts / slash-commands.ts) on ${new Date().toISOString().slice(0, 10)}`,
    );
    fs.writeFileSync(manifestPath, JSON.stringify(next, null, 1) + "\n");
    fs.writeFileSync(surfacePath(manifestPath), JSON.stringify(surfaceFile(next), null, 1) + "\n");
    console.error(`rebased ${manifestPath}: ${baseline} -> ${v.version}`);
  }
  if (tmp) fs.rmSync(tmp, { recursive: true, force: true });
  const drift = Object.values(d.added).some((a) => a.length) || d.removed.length || (v && v.version !== baseline);
  return missing.length && !drift ? 3 : drift ? 1 : 0;
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  main(process.argv.slice(2)).then(
    (c) => process.exit(c),
    (e) => {
      console.error(e.message);
      process.exit(3);
    },
  );
}
