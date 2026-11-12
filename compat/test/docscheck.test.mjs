import test from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";
import {
  parseSections,
  checkRequirements,
  checkDesign,
  checkAdrs,
  checkLinks,
  livingDocsViolation,
  parseDocsImpact,
  run,
  main,
} from "../../scripts/docscheck.mjs";

const repo = path.join(path.dirname(fileURLToPath(import.meta.url)), "..", "..");

const REQ = `# R
### REQ-A-001 — One
- Status: implemented
- Statement: s
- Design: DES-001
- Tests: TestThing; js:my js test
### REQ-A-002 — Two
- Status: planned
- Statement: later
- Design: none yet
`;
const DES = `## DES-001 — Design one
- Satisfies: REQ-A-001
- Code: a.go
`;

function tmp(files) {
  const d = fs.mkdtempSync(path.join(os.tmpdir(), "koine-docs-"));
  for (const [f, text] of Object.entries(files)) {
    fs.mkdirSync(path.dirname(path.join(d, f)), { recursive: true });
    fs.writeFileSync(path.join(d, f), text);
  }
  return d;
}
const good = () => ({
  "docs/requirements.md": REQ,
  "docs/design.md": DES,
  "a.go": "package a",
  "a_test.go": "package a\nfunc TestThing(t *testing.T) {}\n",
  "x.test.mjs": 'test("my js test (node)", () => {})\n',
});

test("docscheck validates the repository's own documents", () => {
  const { errs, counts } = run({ root: repo });
  assert.deepEqual(errs, []);
  assert.ok(counts.requirements >= 15 && counts.design >= 9);
});

test("docscheck validates a minimal consistent tree", () => {
  const d = tmp(good());
  assert.deepEqual(run({ root: d }).errs, []);
  fs.rmSync(d, { recursive: true, force: true });
});

test("parseSections reads ids, titles and fields", () => {
  const s = parseSections(REQ, "REQ-[A-Z]+-\\d{3}");
  assert.equal(s.length, 2);
  assert.equal(s[0].fields.status, "implemented");
  assert.equal(s[1].title, "Two");
});

test("docscheck rejects bad statuses, missing tests, unknown design and duplicates", () => {
  const reqs = parseSections(
    "### REQ-A-001 — X\n- Status: done\n- Statement: s\n" +
      "### REQ-A-001 — Y\n- Status: implemented\n- Statement: s\n- Design: DES-404\n- Tests: TestMissing; js:nope\n",
    "REQ-[A-Z]+-\\d{3}",
  );
  const errs = checkRequirements(reqs, [], new Set(), []).join("\n");
  for (const want of [
    "duplicate id",
    "status must be one of",
    "design DES-404 does not exist",
    "Go test TestMissing",
    'starts with "nope"',
  ]) {
    assert.match(errs, new RegExp(want));
  }
});

test("docscheck rejects an implemented requirement without tests or design", () => {
  const reqs = parseSections("### REQ-A-001 — X\n- Status: implemented\n- Statement: s\n", "REQ-[A-Z]+-\\d{3}");
  const errs = checkRequirements(reqs, [], new Set(), []).join("\n");
  assert.match(errs, /need a Design/);
  assert.match(errs, /need Tests/);
});

test("docscheck rejects design sections with dangling references", () => {
  const d = tmp({ "a.go": "x" });
  const reqs = parseSections(REQ, "REQ-[A-Z]+-\\d{3}");
  const bad = parseSections("## DES-001 — D\n- Satisfies: REQ-Z-999\n- Code: missing.go\n", "DES-\\d{3}");
  const errs = checkDesign(bad, reqs, d).join("\n");
  assert.match(errs, /REQ-Z-999 does not exist/);
  assert.match(errs, /missing\.go does not exist/);
  assert.match(errs, /does not list REQ-A-001/);
  fs.rmSync(d, { recursive: true, force: true });
});

test("docscheck rejects ADRs with a bad status, date or requirement", () => {
  const d = tmp({ "docs/adr/0001-x.md": "# ADR\n- Status: maybe\n- Requirements: REQ-NOPE-001\n" });
  const errs = checkAdrs(d, []).join("\n");
  assert.match(errs, /Status must be/);
  assert.match(errs, /needs "- Date/);
  assert.match(errs, /REQ-NOPE-001/);
  fs.rmSync(d, { recursive: true, force: true });
});

test("docscheck rejects two ADRs with the same number", () => {
  const ok = "# ADR\n- Status: accepted\n- Date: 2026-01-01\n";
  const d = tmp({ "docs/adr/0001-a.md": ok, "docs/adr/0001-b.md": ok, "docs/adr/0002-c.md": ok });
  assert.deepEqual(checkAdrs(d, []), ["adr: number 0001 is used by 0001-a.md, 0001-b.md; renumber one"]);
  fs.rmSync(d, { recursive: true, force: true });
});

test("docscheck finds broken relative links but ignores URLs, anchors and code blocks", () => {
  const d = tmp({
    "README.md": "[ok](docs/a.md#x) [url](https://x.y) [bad](docs/missing.md)\n```\n[fake](nope.md)\n```\n",
    "docs/a.md": "# a",
  });
  assert.deepEqual(checkLinks(d), ["README.md: broken link docs/missing.md"]);
  fs.rmSync(d, { recursive: true, force: true });
});

test("docscheck living-docs rule: code needs a changelog line and a tracker update unless opted out", () => {
  assert.match(livingDocsViolation(["core/app/app.go"], false), /no a line in CHANGELOG\.md and no an update to the requirement/);
  assert.match(livingDocsViolation(["core/app/app.go", "CHANGELOG.md"], false), /no an update to the requirement, design, SPEC/);
  assert.match(livingDocsViolation(["core/app/app.go", "docs/design.md"], false), /no a line in CHANGELOG\.md/);
  assert.equal(livingDocsViolation(["core/app/app.go", "docs/design.md", "CHANGELOG.md"], false), null);
  assert.equal(livingDocsViolation(["cmd/koine/main.go", "compat/settings.json", "CHANGELOG.md"], false), null, "a manifest is a tracker");
  assert.equal(livingDocsViolation(["core/app/app.go"], true), null);
  assert.equal(
    livingDocsViolation(["core/app/app_test.go", "README.md", "compat/pi.json", "compat/settings.json"], false),
    null,
    "tests, README and the manifests alone are not product changes",
  );
});

test("parseDocsImpact needs a real reason", () => {
  assert.deepEqual(parseDocsImpact("x\nDocs-Impact: none — internal rename, no behaviour change\n"), {
    none: true,
    reason: "internal rename, no behaviour change",
  });
  assert.equal(parseDocsImpact("Docs-Impact: none - test-only helper refactor").none, true);
  for (const bad of ["Docs-Impact: none", "Docs-Impact: none — <reason>", "Docs-Impact: none — n/a", "docs impact none", "", undefined]) {
    assert.equal(parseDocsImpact(bad).none, false, String(bad));
  }
});

test("docscheck main reports and returns an exit code", () => {
  const d = tmp({ ...good(), "docs/requirements.md": REQ.replace("TestThing", "TestGone") });
  const log = console.log,
    err = console.error;
  console.log = console.error = () => {};
  try {
    assert.equal(main(["--root", d]), 1);
    assert.equal(main(["--root", tmp(good())]), 0);
  } finally {
    console.log = log;
    console.error = err;
    fs.rmSync(d, { recursive: true, force: true });
  }
});
