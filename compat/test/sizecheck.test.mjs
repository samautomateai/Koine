import test from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { check, walk, main } from "../../scripts/sizecheck.mjs";

const opts = { maxLine: 10, maxFile: 3, tab: 4 };

test("flags long lines and counts tabs as columns", () => {
  assert.deepEqual(check("short\n", opts), []);
  assert.equal(check("12345678901\n", opts)[0].line, 1);
  assert.equal(check("\t\t\tx\n", opts).length, 1, "3 tabs = 12 columns");
});

test("flags long files; a trailing newline is not a line", () => {
  assert.equal(check("a\nb\nc\n", opts).length, 0);
  assert.match(check("a\nb\nc\nd\n", opts)[0].msg, /4 lines/);
});

test("walk skips vendored, generated and non-source files", () => {
  const d = fs.mkdtempSync(path.join(os.tmpdir(), "koine-size-"));
  for (const f of ["a.go", "b.mjs", "node_modules/x.js", "testdata/y.go", "z.txt", "api.pb.go"]) {
    fs.mkdirSync(path.dirname(path.join(d, f)), { recursive: true });
    fs.writeFileSync(path.join(d, f), "x\n");
  }
  assert.deepEqual([...walk(d)].map((p) => path.basename(p)).sort(), ["a.go", "b.mjs"]);
  fs.rmSync(d, { recursive: true, force: true });
});

test("main exits 1 on a violation and 0 when clean", () => {
  const d = fs.mkdtempSync(path.join(os.tmpdir(), "koine-size-"));
  fs.writeFileSync(path.join(d, "a.go"), "x".repeat(50) + "\n");
  const log = console.log,
    err = console.error;
  console.log = console.error = () => {};
  try {
    assert.equal(main([d, "--max-line", "20"]), 1);
    assert.equal(main([d, "--max-line", "80"]), 0);
  } finally {
    console.log = log;
    console.error = err;
    fs.rmSync(d, { recursive: true, force: true });
  }
});
