#!/usr/bin/env node
// Diff upstream Pi's documented settings keys against compat/settings.json (Koine's mapping of Pi's settings schema).
//
//   node scripts/settings-check.mjs --doc settings.md     a copy of Pi's docs/settings.md
//   node scripts/settings-check.mjs --src <dir>           a checkout or unpacked npm package containing docs/settings.md
//   node scripts/settings-check.mjs                       `npm pack <package>@latest` (needs network)
//   add --rebase to rewrite compat/settings.json: new keys are added as `todo`, vanished keys are marked, the baseline moves
//   add --json for machine output
//
// Exit codes: 0 no drift · 1 drift found · 3 extraction failed (upstream moved or reformatted the settings page)
import { execFileSync } from "node:child_process";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";

/** Keys documented in the markdown tables of a settings page: rows that start with a backticked key. Returns Map<key, typeText>. */
export function parseSettingsDoc(md) {
  const keys = new Map();
  for (const line of md.split("\n")) {
    const m = /^\|\s*`([A-Za-z][\w.$-]*)`\s*\|\s*([^|]*?)\s*\|/.exec(line);
    if (m && !keys.has(m[1])) keys.set(m[1], m[2]);
  }
  return keys;
}

/** Turns the type column of the doc into the manifest's type syntax; anything unfamiliar becomes "any" so triage is not blocked. */
export function typeOf(text) {
  const t = text.trim();
  const enumM = /^enum:\s*(.+)$/i.exec(t);
  if (enumM)
    return enumM[1]
      .split(/,\s*/)
      .map((x) => `"${x.replace(/[`"]/g, "")}"`)
      .join("|");
  if (/^(boolean|string|number|object|array|string\[\])$/.test(t)) return t;
  return "any";
}

export function diff(upstream, manifest) {
  const have = new Set(manifest.keys.map((k) => k.key));
  const koine = new Set((manifest.koine ?? []).map((k) => k.key));
  const added = [...upstream.keys()].filter((k) => !have.has(k) && !koine.has(k));
  const removed = manifest.keys.filter((k) => !upstream.has(k.key) && !k.removedUpstream);
  return { added, removed };
}

export function rebase(manifest, upstream, d, version) {
  const next = structuredClone(manifest);
  for (const k of d.added) next.keys.push({ key: k, type: typeOf(upstream.get(k)), status: "todo", note: `New in ${version}; triage.` });
  for (const r of d.removed) next.keys.find((k) => k.key === r.key).removedUpstream = version;
  next.upstream.baseline = version;
  return next;
}

export function report({ version, baseline, d }) {
  const lines = [`# Settings drift: ${version ?? "?"} vs baseline ${baseline}`, ""];
  if (d.added.length) lines.push(`## New upstream settings (${d.added.length})`, d.added.map((k) => `- \`${k}\``).join("\n"), "");
  if (d.removed.length)
    lines.push(
      `## In compat/settings.json but not upstream (${d.removed.length}) — renamed or removed?`,
      d.removed.map((k) => `- \`${k.key}\` (koine status: ${k.status})`).join("\n"),
      "",
    );
  if (!d.added.length && !d.removed.length)
    lines.push(version === baseline ? "No drift." : `Keys unchanged; only the version moved (${baseline} → ${version}).`);
  return lines.join("\n");
}

function findDoc(dir) {
  for (const p of ["docs/settings.md", "package/docs/settings.md", "packages/coding-agent/docs/settings.md"]) {
    const f = path.join(dir, p);
    if (fs.existsSync(f)) return { doc: f, pkg: path.join(path.dirname(path.dirname(f)), "package.json") };
  }
  return null;
}

export async function main(argv, log = console.log) {
  const arg = (k) => {
    const i = argv.indexOf(k);
    return i >= 0 ? argv[i + 1] : undefined;
  };
  const here = path.dirname(fileURLToPath(import.meta.url));
  const manifestPath = arg("--manifest") ?? path.join(here, "..", "compat", "settings.json");
  const manifest = JSON.parse(fs.readFileSync(manifestPath, "utf8"));
  let docFile = arg("--doc");
  let pkgFile;
  let tmp;
  if (!docFile) {
    let dir = arg("--src");
    if (!dir) {
      tmp = fs.mkdtempSync(path.join(os.tmpdir(), "koine-settings-"));
      const out = execFileSync("npm", ["pack", `${manifest.upstream.package}@latest`, "--pack-destination", tmp, "--silent"], {
        encoding: "utf8",
      })
        .trim()
        .split("\n")
        .pop();
      execFileSync("tar", ["-xzf", path.join(tmp, out), "-C", tmp]);
      dir = tmp;
    }
    const found = findDoc(dir);
    if (!found) {
      console.error(`extraction failed: no docs/settings.md under ${dir}`);
      return 3;
    }
    docFile = found.doc;
    pkgFile = found.pkg;
  }
  let version;
  try {
    version = JSON.parse(fs.readFileSync(pkgFile ?? "", "utf8")).version;
  } catch {}
  version = arg("--version") ?? version;
  const upstream = parseSettingsDoc(fs.readFileSync(docFile, "utf8"));
  if (upstream.size < 10) {
    console.error(`extraction failed: found only ${upstream.size} settings keys in ${docFile}; did the page format change?`);
    return 3;
  }
  const d = diff(upstream, manifest);
  const baseline = manifest.upstream.baseline;
  log(
    argv.includes("--json")
      ? JSON.stringify({ version, baseline, added: d.added, removed: d.removed.map((r) => r.key) }, null, 1)
      : report({ version, baseline, d }),
  );
  if (argv.includes("--rebase")) {
    if (!version) {
      console.error("cannot rebase: upstream version not found (pass --version)");
      return 3;
    }
    fs.writeFileSync(manifestPath, JSON.stringify(rebase(manifest, upstream, d, version), null, 1) + "\n");
    console.error(`rebased ${manifestPath}: ${baseline} -> ${version}`);
  }
  if (tmp) fs.rmSync(tmp, { recursive: true, force: true });
  return d.added.length || d.removed.length || (version && version !== baseline) ? 1 : 0;
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
