import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { diff, main, parseSettingsDoc, rebase, report, typeOf } from "../../scripts/settings-check.mjs";

const DOC = `# Settings
| Key | Type | Default | Meaning |
|---|---|---|---|
| \`theme\` | string | \`system\` | Theme. |
| \`tuiMode\` | enum: regular, fullscreen | \`fullscreen\` | Mode. |
| \`compaction.enabled\` | boolean | \`true\` | Auto. |
| \`brandNew\` | number | 3 | New. |
| \`weird\` | boolean or "x" | | Odd. |
Not a row: | \`ignored\` | x |
${Array.from({ length: 10 }, (_, i) => `| \`pad${i}\` | string | | |`).join("\n")}
`;

const manifest = () => ({
  schema: 1,
  upstream: { package: "p", baseline: "1.0.0" },
  keys: [
    { key: "theme", type: "string", status: "inert" },
    { key: "tuiMode", type: '"regular"|"fullscreen"', status: "inert" },
    { key: "compaction.enabled", type: "boolean", status: "verified", test: "T" },
    { key: "gone", type: "string", status: "todo" },
    ...Array.from({ length: 10 }, (_, i) => ({ key: `pad${i}`, type: "string", status: "todo" })),
  ],
  koine: [{ key: "maxTurns", type: "number" }],
});

test("parseSettingsDoc reads table rows that start with a backticked key", () => {
  const keys = parseSettingsDoc(DOC);
  assert.equal(keys.get("compaction.enabled"), "boolean");
  assert.equal(keys.get("tuiMode"), "enum: regular, fullscreen");
  assert.ok(!keys.has("ignored") && !keys.has("Key"));
});

test("typeOf converts the doc's type column, falling back to any", () => {
  assert.equal(typeOf("enum: a, b"), '"a"|"b"');
  assert.equal(typeOf("string[]"), "string[]");
  assert.equal(typeOf('boolean or "x"'), "any");
});

test("diff finds new and vanished keys and ignores Koine-only ones", () => {
  const d = diff(parseSettingsDoc(DOC), manifest());
  assert.deepEqual(d.added.sort(), ["brandNew", "weird"]);
  assert.deepEqual(
    d.removed.map((r) => r.key),
    ["gone"],
  );
  const m = manifest();
  m.koine.push({ key: "brandNew", type: "number" });
  assert.ok(!diff(parseSettingsDoc(DOC), m).added.includes("brandNew"));
});

test("rebase adds todo keys, marks vanished keys and moves the baseline", () => {
  const up = parseSettingsDoc(DOC);
  const next = rebase(manifest(), up, diff(up, manifest()), "2.0.0");
  assert.equal(next.upstream.baseline, "2.0.0");
  assert.deepEqual(
    next.keys.find((k) => k.key === "brandNew"),
    { key: "brandNew", type: "number", status: "todo", note: "New in 2.0.0; triage." },
  );
  assert.equal(next.keys.find((k) => k.key === "weird").type, "any");
  assert.equal(next.keys.find((k) => k.key === "gone").removedUpstream, "2.0.0");
  assert.equal(diff(up, next).removed.length, 0, "a marked key is not reported again");
});

test("report words", () => {
  const up = parseSettingsDoc(DOC);
  assert.match(
    report({ version: "1.0.0", baseline: "1.0.0", d: diff(up, manifest()) }),
    /New upstream settings \(2\)[\s\S]*renamed or removed/,
  );
  assert.match(report({ version: "1.0.0", baseline: "1.0.0", d: { added: [], removed: [] } }), /No drift/);
  assert.match(report({ version: "1.1.0", baseline: "1.0.0", d: { added: [], removed: [] } }), /only the version moved/);
});

test("main: exit codes, --rebase and extraction failure", async () => {
  const d = fs.mkdtempSync(path.join(os.tmpdir(), "koine-sc-"));
  const mf = path.join(d, "settings.json");
  const doc = path.join(d, "settings.md");
  fs.writeFileSync(mf, JSON.stringify(manifest()));
  fs.writeFileSync(doc, DOC);
  const out = [];
  const log = (s) => out.push(s);
  assert.equal(await main(["--manifest", mf, "--doc", doc, "--version", "1.0.0"], log), 1);
  assert.equal(await main(["--manifest", mf, "--doc", doc, "--version", "1.0.0", "--json"], log), 1);
  assert.deepEqual(JSON.parse(out.at(-1)).added.sort(), ["brandNew", "weird"]);
  const err = console.error;
  console.error = () => {};
  try {
    assert.equal(await main(["--manifest", mf, "--doc", doc, "--rebase"], log), 3, "rebase needs a version");
    assert.equal(await main(["--manifest", mf, "--doc", doc, "--version", "2.0.0", "--rebase"], log), 1);
    assert.equal(await main(["--manifest", mf, "--doc", doc, "--version", "2.0.0"], log), 0);
    fs.writeFileSync(doc, "# nothing here\n");
    assert.equal(await main(["--manifest", mf, "--doc", doc], log), 3);
    assert.equal(await main(["--manifest", mf, "--src", d], log), 3, "no docs/settings.md in --src");
    fs.mkdirSync(path.join(d, "docs"));
    fs.writeFileSync(path.join(d, "docs", "settings.md"), DOC);
    fs.writeFileSync(path.join(d, "package.json"), '{"version":"2.0.0"}');
    assert.equal(await main(["--manifest", mf, "--src", d], log), 0, "found through --src with the version from package.json");
  } finally {
    console.error = err;
    fs.rmSync(d, { recursive: true, force: true });
  }
});
