// The Pi extension API beyond tools and commands (REQ-EXT-006..): events, dialogs, the bus, messages, cancellation. A bidirectional
// session helper answers the requests the extension makes of the host.
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

// steps: [method, params] waits for the answer; ["!method", params] is a notification; ["~method", params] is a request that is not waited
// for (its answer is in the results all the same). answer(m) gives the result for a request the extension makes of the host.
async function session(src, steps, { init = {}, answer = () => ({}) } = {}) {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "koine-api-"));
  const file = path.join(dir, "ext.mjs");
  fs.writeFileSync(file, src);
  const child = spawn(process.execPath, [...runner, file], { env: { ...process.env, KOINE_CWD: dir, PIGO_CWD: dir } });
  const out = [];
  const asked = [];
  let buf = "";
  child.stdout.on("data", (d) => {
    buf += d;
    for (let i = buf.indexOf("\n"); i >= 0; i = buf.indexOf("\n")) {
      const m = JSON.parse(buf.slice(0, i));
      buf = buf.slice(i + 1);
      if (m.method && m.id !== undefined) {
        asked.push(m);
        child.stdin.write(`${JSON.stringify({ jsonrpc: "2.0", id: m.id, result: answer(m) })}\n`);
      } else out.push(m);
    }
  });
  const closed = new Promise((resolve) => child.on("close", resolve));
  const waitFor = async (pred) => {
    for (let n = 0; n < 300 && !out.some(pred); n++) await new Promise((r) => setTimeout(r, 25));
    return out.find(pred);
  };
  const ids = [];
  let id = 0;
  const send = (o) => child.stdin.write(`${JSON.stringify({ jsonrpc: "2.0", ...o })}\n`);
  send({ id: ++id, method: "initialize", params: { cwd: dir, ...init } });
  ids.push(id);
  await waitFor((m) => m.id === id);
  for (const [method, params] of steps) {
    if (method.startsWith("!")) {
      send({ method: method.slice(1), params });
      await new Promise((r) => setTimeout(r, 50));
    } else {
      const my = ++id;
      ids.push(my);
      send({ id: my, method: method.replace(/^~/, ""), params });
      if (!method.startsWith("~")) await waitFor((m) => m.id === my);
    }
  }
  for (const i of ids) await waitFor((m) => m.id === i);
  const results = ids.map((i) => out.find((m) => m.id === i));
  const notes = out.filter((m) => m.method);
  send({ method: "shutdown" });
  await closed;
  return { results, notes, asked };
}

const call = (toolName, input) => ["event", { name: "tool_call", payload: { toolName, callId: "c", input } }];

const dialogs = `
export default function (pi) {
  pi.on("tool_call", async (e, ctx) => {
    const r = [
      await ctx.ui.select("pick", ["a", "b"]),
      await ctx.ui.confirm("sure", "really?"),
      await ctx.ui.input("name", "who"),
      await ctx.ui.editor("text", "pre"),
    ];
    return { block: true, reason: JSON.stringify([ctx.mode, ctx.hasUI, ...r]) };
  });
}`;

test("ui: dialogs are requests to the host, whose answers become the return values", async () => {
  const answer = (m) => {
    const p = m.params;
    if (p.method === "select") return { value: p.options[1] };
    if (p.method === "confirm") return { confirmed: true };
    if (p.method === "input") return { cancelled: true };
    return { value: "edited" };
  };
  const r = await session(dialogs, [call("x", {})], { init: { mode: "rpc", hasUI: true }, answer });
  assert.equal(r.results[1].result.reason, JSON.stringify(["rpc", true, "b", true, null, "edited"]));
  assert.deepEqual(
    r.asked.map((m) => [m.method, m.params.method]),
    [
      ["ui.dialog", "select"],
      ["ui.dialog", "confirm"],
      ["ui.dialog", "input"],
      ["ui.dialog", "editor"],
    ],
  );
  assert.equal(r.asked[0].params.title, "pick");
  assert.equal(r.asked[3].params.prefill, "pre");
});

test("ui: without a frontend dialogs resolve at once and nothing is asked", async () => {
  const r = await session(dialogs, [call("x", {})]);
  assert.equal(r.results[1].result.reason, JSON.stringify(["print", false, null, false, null, null]));
  assert.equal(r.asked.length, 0);
});

const surfaces = `
export default function (pi) {
  pi.on("tool_call", async (e, ctx) => {
    ctx.ui.setStatus("k", "busy"); ctx.ui.setStatus("k", undefined);
    ctx.ui.setWidget("w", ["l1", "l2"], { placement: "belowEditor" }); ctx.ui.setWidget("f", () => ({}));
    ctx.ui.setTitle("T"); ctx.ui.setWorkingMessage("thinking"); ctx.ui.setWorkingMessage();
    ctx.ui.setEditorText("draft"); ctx.ui.notify("hello", "warning"); ctx.ui.setFooter("x");
  });
}`;

test("ui: surfaces are notifications when a frontend is there, and silent otherwise", async () => {
  const r = await session(surfaces, [call("x", {})], { init: { mode: "tui", hasUI: true } });
  const ui = r.notes.filter((n) => n.method === "ui").map((n) => n.params);
  assert.deepEqual(ui[0], { method: "setStatus", statusKey: "k", statusText: "busy" });
  assert.deepEqual(ui[1], { method: "setStatus", statusKey: "k" });
  assert.deepEqual(ui[2], { method: "setWidget", widgetKey: "w", widgetLines: ["l1", "l2"], widgetPlacement: "belowEditor" });
  assert.equal(ui[3].widgetLines, undefined);
  assert.deepEqual(
    ui.slice(4).map((u) => u.method),
    ["setTitle", "setWorkingMessage", "setWorkingMessage", "set_editor_text", "notify"],
  );
  assert.equal(ui[8].notifyType, "warning");
  const quiet = await session(surfaces, [call("x", {})]);
  assert.equal(quiet.notes.filter((n) => n.method === "ui").length, 0);
  assert.ok(quiet.notes.some((n) => n.method === "log" && n.params.message === "hello"));
});

const events = `
export default function (pi) {
  pi.on("tool_call", async (e) => { e.input.n = (e.input.n ?? 0) + 1; });
  pi.on("tool_call", async (e) => { if (e.input.n > 1) e.input.seen = e.input.n; });
  pi.on("tool_result", async (e) => ({ content: [{ type: "text", text: e.content[0].text + "1" }], details: { a: 1 } }));
  pi.on("tool_result", async (e) => ({ content: [{ type: "text", text: e.content[0].text + "2" }], isError: e.details?.a === 1 }));
  pi.on("input", async (e) => {
    if (e.text === "hide") return { action: "handled" };
    return { action: "transform", text: e.text + "!", images: [...e.images, { type: "image", data: "BB", mimeType: "image/jpeg" }] };
  });
  pi.on("input", async (e) => ({ action: "transform", text: e.text + "?" }));
  pi.on("before_agent_start", async (e) => ({ systemPrompt: e.systemPrompt + "+", message: { customType: "m", content: "c", display: true } }));
  pi.on("session_before_switch", async (e) => (e.reason === "new" ? { cancel: true } : undefined));
  pi.on("tool_execution_start", async () => { throw new Error("observer bug"); });
  pi.on("tool_call", async (e) => { if (e.toolName === "throws") throw new Error("gate bug"); });
}`;

test("events: handlers chain, mutate in place, and report their errors", async () => {
  const r = await session(events, [
    call("bash", { command: "ls" }),
    ["event", { name: "tool_result", payload: { toolName: "read", content: [{ type: "text", text: "o" }], input: {}, isError: false } }],
    ["event", { name: "tool_result", payload: { toolName: "read", content: [{ type: "text", text: "z" }], input: {} } }],
    ["event", { name: "input", payload: { text: "hi", images: [{ type: "image", data: "AA", mimeType: "image/png" }], source: "rpc" } }],
    ["event", { name: "input", payload: { text: "hide" } }],
    ["event", { name: "before_agent_start", payload: { prompt: "p", systemPrompt: "S" } }],
    ["event", { name: "session_before_switch", payload: { reason: "new" } }],
    ["event", { name: "session_before_switch", payload: { reason: "resume" } }],
    ["event", { name: "tool_execution_start", payload: { toolName: "x" } }],
    call("throws", {}),
  ]);
  const res = r.results.slice(1).map((m) => m.result ?? {});
  assert.deepEqual(res[0].args, { command: "ls", n: 1 }, "an in-place change of input becomes the new arguments");
  assert.equal(res[1].output, "o12");
  assert.deepEqual(res[1].details, { a: 1 });
  assert.equal(res[1].isError, true, "the second handler saw the first one's details");
  assert.equal(res[2].content[0].text, "z12");
  assert.equal(res[3].text, "hi!?");
  assert.deepEqual(
    res[3].images.map((i) => i.mimeType),
    ["image/png", "image/jpeg"],
  );
  assert.equal(res[4].handled, true);
  assert.equal(res[5].systemPrompt, "S+");
  assert.deepEqual(res[5].message, { customType: "m", content: "c", display: true });
  assert.equal(res[6].cancel, true);
  assert.equal(res[7].cancel, undefined);
  assert.deepEqual(res[8], {}, "an observer that throws changes nothing");
  assert.equal(res[9].block, true, "a throwing tool_call handler blocks the call");
  assert.match(res[9].reason, /gate bug/);
  const errs = r.notes.filter((n) => n.method === "ext_error").map((n) => n.params);
  assert.deepEqual(
    errs.map((e) => e.event),
    ["tool_execution_start", "tool_call"],
  );
  assert.match(errs[0].error, /observer bug/);
});

const bus = `
export default function (pi) {
  const seen = [];
  pi.events.on("ping", (d) => seen.push(d));
  pi.events.on("boom", () => { throw new Error("listener bug"); });
  pi.registerCommand("emit", { handler: async () => { pi.events.emit("ping", { local: 1 }); } });
  pi.registerCommand("seen", { handler: async (a, ctx) => ctx.ui.notify(JSON.stringify(seen)) });
}`;

test("bus: emits reach local listeners and the host; events from the host reach listeners", async () => {
  const r = await session(bus, [
    ["command.execute", { name: "emit", args: "" }],
    ["!bus_event", { channel: "ping", data: { remote: 2 } }],
    ["!bus_event", { channel: "boom", data: 1 }],
    ["command.execute", { name: "seen", args: "" }],
  ]);
  assert.deepEqual(r.notes.find((n) => n.method === "bus_emit").params, { channel: "ping", data: { local: 1 } });
  assert.equal(r.results[2].result.output, JSON.stringify([{ local: 1 }, { remote: 2 }]));
  assert.ok(r.notes.some((n) => n.method === "ext_error" && /listener bug/.test(n.params.error)));
});

const messages = `
export default function (pi) {
  pi.registerCommand("go", { handler: async (a, ctx) => {
    pi.sendMessage({ customType: "note", content: "hi", display: true, details: { k: 1 } }, { deliverAs: "nextTurn", triggerTurn: true });
    ctx.ui.notify(JSON.stringify([pi.getCommands().map((c) => c.name), pi.getActiveTools(), pi.getAllTools()]));
    ctx.shutdown();
  } });
  const complete = (p) => {
    if (p === "bad") return null;
    if (p === "throw") throw new Error("x");
    return ["one", { value: "two", label: "Two" }, 3];
  };
  pi.registerCommand("pick", { description: "d", getArgumentCompletions: complete });
}`;

test("messages: sendMessage, getCommands, shutdown and completions", async () => {
  const r = await session(messages, [
    ["!host_state", { commands: [{ name: "a", source: "extension" }], tools: [{ name: "read" }, { name: "bash" }] }],
    ["command.execute", { name: "go", args: "" }],
    ["command.complete", { name: "pick", prefix: "o" }],
    ["command.complete", { name: "pick", prefix: "bad" }],
    ["command.complete", { name: "pick", prefix: "throw" }],
    ["command.complete", { name: "go", prefix: "" }],
  ]);
  const m = r.notes.find((n) => n.method === "send_message").params;
  assert.equal(m.customType, "note");
  assert.equal(m.deliverAs, "nextTurn");
  assert.equal(m.triggerTurn, true);
  assert.deepEqual(m.details, { k: 1 });
  assert.equal(r.results[1].result.output, JSON.stringify([["a"], ["read", "bash"], ["read", "bash"]]));
  assert.ok(r.notes.some((n) => n.method === "shutdown_request"));
  assert.deepEqual(r.results[2].result.items, [
    { value: "one", label: "one" },
    { value: "two", label: "Two" },
  ]);
  assert.deepEqual(r.results[3].result.items, []);
  assert.deepEqual(r.results[4].result.items, []);
  assert.deepEqual(r.results[5].result.items, []);
  assert.ok(r.notes.some((n) => n.method === "register_command" && n.params.name === "pick" && n.params.hasCompletions === true));
});

const tools = `
export default function (pi) {
  pi.registerTool({ name: "wait", description: "d", parameters: { type: "object", properties: {} },
    async execute(id, p, signal, onUpdate, ctx) {
      await new Promise((resolve) => signal.addEventListener("abort", resolve, { once: true }));
      return { content: [{ type: "text", text: "aborted " + signal.aborted }] };
    } });
  pi.registerTool({ name: "nest", description: "d", parameters: { type: "object", properties: {} },
    async execute(id, p, signal, onUpdate, ctx) {
      const r = await ctx.executeTool("read", { path: "f" });
      return { content: [{ type: "text", text: JSON.stringify([ctx.tools, r]) }] };
    } });
}`;

test("tools: a cancel notification aborts the call's signal; ctx.executeTool asks the host", async () => {
  const r = await session(
    tools,
    [
      ["~tool.execute", { name: "wait", args: {} }],
      ["!cancel", { id: 2 }],
      ["tool.execute", { name: "nest", args: {} }],
    ],
    { answer: (m) => (m.method === "tools.execute" ? { content: `ran ${m.params.name}`, isError: false } : {}) },
  );
  assert.equal(r.results[1].result.content, "aborted true");
  assert.match(r.results[2].result.content, /ran read/);
  assert.equal(r.asked[0].params.name, "read");
  assert.deepEqual(r.asked[0].params.args, { path: "f" });
});

const queue = `
import { withFileMutationQueue } from "@earendil-works/pi-coding-agent";
export default function (pi) {
  const order = [];
  const step = (name, ms) => withFileMutationQueue("/tmp/same-file", async () => {
    order.push(name + ":start");
    await new Promise((r) => setTimeout(r, ms));
    order.push(name + ":end");
  });
  pi.registerTool({ name: "q", description: "d", parameters: { type: "object", properties: {} },
    async execute() {
      await Promise.all([step("a", 60), step("b", 1), withFileMutationQueue("/tmp/other", async () => order.push("o")).then(() => 0)]);
      await withFileMutationQueue("/tmp/same-file", async () => { throw new Error("late"); }).catch(() => order.push("recovered"));
      await withFileMutationQueue("/tmp/same-file", async () => order.push("after"));
      return { content: [{ type: "text", text: order.join(",") }] };
    } });
}`;

test("shim: withFileMutationQueue runs the calls for one file one after the other and survives a failure", async () => {
  const r = await session(queue, [["tool.execute", { name: "q", args: {} }]]);
  const order = r.results[1].result.content.split(",");
  assert.ok(order.indexOf("a:end") < order.indexOf("b:start"), order.join());
  assert.ok(order.indexOf("o") < order.indexOf("a:end"), "another file does not wait");
  assert.deepEqual(order.slice(-2), ["recovered", "after"]);
});
