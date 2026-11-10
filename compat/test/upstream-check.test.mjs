import test from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { spawnSync } from "node:child_process";
import {
  stripComments,
  interfaceBody,
  members,
  onEvents,
  slashCommands,
  extract,
  diff,
  rebase,
  surfaceFile,
  readSource,
  findVersion,
  report,
} from "../../scripts/upstream-check.mjs";

const here = path.dirname(fileURLToPath(import.meta.url));
const root = path.join(here, "..", "..");
const fixture = path.join(here, "fixture", "pkg");
const manifestPath = path.join(root, "compat", "pi.json");
const manifest = JSON.parse(fs.readFileSync(manifestPath, "utf8"));

test("stripComments drops comments but keeps strings", () => {
  const s = stripComments('a // x\n/* interface Fake { } */ b "// not a comment" c');
  assert.ok(!s.includes("interface Fake"));
  assert.ok(s.includes('"// not a comment"'));
});

test("members: methods, props, generics, optional, readonly; overloads merge to method", () => {
  const body = interfaceBody(stripComments(fs.readFileSync(path.join(fixture, "types.ts"), "utf8")), "ExtensionUIContext");
  const m = Object.fromEntries(members(body).map((x) => [x.name, x.shape]));
  assert.deepEqual(m, { notify: "method", theme: "prop", select: "method", brandNewWidget: "method" });
});

test("onEvents handles multi-line overloads", () => {
  const body = interfaceBody(stripComments(fs.readFileSync(path.join(fixture, "types.ts"), "utf8")), "ExtensionAPI");
  assert.deepEqual(onEvents(body), ["session_start", "tool_call", "brand_new_event"]);
});

test("slashCommands", () => {
  assert.deepEqual(slashCommands(fs.readFileSync(path.join(fixture, "slash-commands.ts"), "utf8")), ["settings", "model"]);
  assert.equal(slashCommands("nothing here"), null);
});

test("interfaceBody returns null for a missing interface", () => {
  assert.equal(interfaceBody("interface A { x: 1 }", "B"), null);
});

test("extract + diff against the real manifest reports new and removed surface", () => {
  const { surface, missing } = extract(readSource(fixture));
  assert.deepEqual(missing, []);
  assert.equal(surface.api.get("pi.surprise"), "method");
  assert.equal(surface.api.get("pi.events"), "prop");
  assert.equal(surface.ui.get("ctx.ui.theme"), "prop");
  const d = diff(surface, manifest);
  assert.ok(d.added.api.includes("pi.surprise"));
  assert.ok(d.added.event.includes("brand_new_event"));
  assert.ok(d.removed.length > 0, "fixture is a tiny surface, so most manifest items look removed");
});

test("rebase adds unsupported items, flags removed ones, never deletes", () => {
  const { surface } = extract(readSource(fixture));
  const next = rebase(manifest, surface, "9.9.9", "fixture");
  assert.equal(next.upstream.baseline, "9.9.9");
  const added = next.items.find((i) => i.name === "pi.surprise");
  assert.equal(added.status, "unsupported");
  assert.equal(next.items.filter((i) => i.removedUpstream === "9.9.9").length > 0, true);
  assert.ok(next.items.length >= manifest.items.length);
  // idempotent: rebasing again adds nothing
  assert.equal(rebase(next, surface, "9.9.9", "fixture").items.length, next.items.length);
  assert.equal(manifest.upstream.baseline === "9.9.9", false, "input must not be mutated");
});

test("findVersion reads the fixture package", () => {
  assert.deepEqual(findVersion(fixture), { name: "@fixture/coding-agent", version: "9.9.9" });
  assert.equal(findVersion(path.join(here, "nonexistent")), null);
});

test("report: drift vs no drift", () => {
  const empty = { added: { event: [], api: [], ctx: [], ui: [], tool: [], command: [] }, removed: [] };
  assert.match(report({ version: "1", name: "n", baseline: "1", d: empty, missing: [] }), /No drift/);
  assert.match(report({ version: "2", name: "n", baseline: "1", d: empty, missing: [] }), /only the version moved/);
  const drift = { ...empty, added: { ...empty.added, api: ["pi.x"] } };
  assert.match(report({ version: "2", name: "n", baseline: "1", d: drift, missing: ["Foo"] }), /pi\.x[\s\S]*|Extraction problems/);
});

test("committed surface.json equals what the manifest generates (run `task compat:sync` if this fails)", () => {
  const onDisk = JSON.parse(fs.readFileSync(path.join(root, "core", "ext", "sidecar", "surface.json"), "utf8"));
  assert.deepEqual(onDisk, surfaceFile(manifest));
});

test("CLI: fixture package drifts (exit 1) and --json is parseable", () => {
  const r = spawnSync("node", [path.join(root, "scripts", "upstream-check.mjs"), "--src", fixture, "--json"], { encoding: "utf8" });
  assert.equal(r.status, 1, r.stderr);
  const j = JSON.parse(r.stdout);
  assert.equal(j.version, "9.9.9");
  assert.ok(j.added.api.includes("pi.surprise"));
});

test("CLI: extraction failure exits 3", () => {
  const dir = fs.mkdtempSync(path.join(fs.realpathSync(process.env.TMPDIR || "/tmp"), "koine-empty-"));
  fs.writeFileSync(path.join(dir, "package.json"), '{"name":"x","version":"1.0.0"}');
  fs.writeFileSync(path.join(dir, "a.ts"), "interface ExtensionNothing {}");
  const r = spawnSync("node", [path.join(root, "scripts", "upstream-check.mjs"), "--src", dir], { encoding: "utf8" });
  fs.rmSync(dir, { recursive: true, force: true });
  assert.equal(r.status, 3, r.stdout + r.stderr);
});

test("CLI: --rebase on a copy rewrites manifest and surface.json", () => {
  const tmp = fs.mkdtempSync(path.join(fs.realpathSync(process.env.TMPDIR || "/tmp"), "koine-rebase-"));
  fs.mkdirSync(path.join(tmp, "compat"));
  fs.mkdirSync(path.join(tmp, "core/ext/sidecar"), { recursive: true });
  fs.copyFileSync(manifestPath, path.join(tmp, "compat", "pi.json"));
  const r = spawnSync(
    "node",
    [path.join(root, "scripts", "upstream-check.mjs"), "--src", fixture, "--rebase", "--manifest", path.join(tmp, "compat", "pi.json")],
    { encoding: "utf8" },
  );
  assert.equal(r.status, 1, r.stderr);
  const m = JSON.parse(fs.readFileSync(path.join(tmp, "compat", "pi.json"), "utf8"));
  assert.equal(m.upstream.baseline, "9.9.9");
  assert.ok(fs.existsSync(path.join(tmp, "core/ext/sidecar/surface.json")));
  fs.rmSync(tmp, { recursive: true, force: true });
});
