// Agent-loop decisions (REQ-AGT-001): what a Pi handler's return value becomes in the Decision the Go loop reads.
import test from "node:test";
import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import path from "node:path";
import fs from "node:fs";
import os from "node:os";
import { fileURLToPath } from "node:url";

const sidecar = path.join(path.dirname(fileURLToPath(import.meta.url)), "..", "sidecar");
const runner = [
  "--experimental-strip-types",
  "--no-warnings",
  "--import",
  path.join(sidecar, "register.mjs"),
  path.join(sidecar, "runner.mjs"),
];

async function session(src, steps) {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "koine-loop-"));
  const file = path.join(dir, "ext.mjs");
  fs.writeFileSync(file, src);
  const child = spawn(process.execPath, [...runner, file], { env: { ...process.env, KOINE_CWD: dir, PIGO_CWD: dir } });
  const out = [];
  let buf = "";
  child.stdout.on("data", (d) => {
    buf += d;
    for (let i = buf.indexOf("\n"); i >= 0; i = buf.indexOf("\n")) {
      out.push(JSON.parse(buf.slice(0, i)));
      buf = buf.slice(i + 1);
    }
  });
  const closed = new Promise((resolve) => child.on("close", resolve));
  const results = [];
  let id = 0;
  for (const [method, params] of steps) {
    const my = ++id;
    child.stdin.write(JSON.stringify({ jsonrpc: "2.0", id: my, method, params }) + "\n");
    for (let n = 0; n < 300 && !out.some((m) => m.id === my); n++) await new Promise((r) => setTimeout(r, 25));
    results.push(out.find((m) => m.id === my));
  }
  child.stdin.write(JSON.stringify({ jsonrpc: "2.0", method: "shutdown" }) + "\n");
  await closed;
  return results;
}

const decisions = `
export default function (pi) {
  pi.on("tool_call", async (e) => {
    if (e.toolName === "stop") return { block: true, reason: "enough", terminate: true };
    if (e.toolName === "deny") return { block: true };
  });
  pi.on("tool_result", async (e) => {
    if (e.toolName !== "flip") return;
    return { isError: true, content: [{ type: "text", text: "changed" }] };
  });
}`;

const decisionSteps = [
  ["initialize", {}],
  ["event", { name: "tool_call", payload: { toolName: "stop", callId: "1", input: {} } }],
  ["event", { name: "tool_call", payload: { toolName: "deny", callId: "2", input: {} } }],
  ["event", { name: "tool_result", payload: { toolName: "flip", callId: "3", input: {}, output: "o", isError: false } }],
  ["event", { name: "tool_result", payload: { toolName: "same", callId: "4", input: {}, output: "o", isError: false } }],
];

test("loop: tool_call block with terminate, and tool_result isError, reach the decision", async () => {
  const results = await session(decisions, decisionSteps);
  assert.deepEqual(results[1].result, { block: true, reason: "enough", terminate: true });
  assert.deepEqual(results[2].result, { block: true, reason: "blocked" });
  assert.deepEqual(results[3].result, { isError: true, output: "changed" });
  assert.deepEqual(results[4].result ?? {}, {});
});

const contextExt = `
export default function (pi) {
  pi.on("context", async () => ({ messages: [] }));
  pi.on("turn_end", async () => {});
}`;

test("loop: a Pi context handler is never given Koine's message shape", async () => {
  const results = await session(contextExt, [["initialize", {}]]);
  assert.ok(results[0].result.events.includes("turn_end"));
  assert.ok(!results[0].result.events.includes("context"), `subscribed: ${results[0].result.events}`);
});
