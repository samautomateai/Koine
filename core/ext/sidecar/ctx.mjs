// The context object handlers receive (Pi's ExtensionContext) and its `ui`.
import { degrade, inert, log, notify, request, state } from "./host.mjs";

// A dialog is answered by the frontend through the host. Without a frontend (print and json mode) it resolves at once with the
// cancelled value, as Pi does. A dialog the extension abandons (its signal aborts) resolves the same way.
async function dialog(method, fields, opts) {
  if (!state.hasUI) return undefined;
  const signal = opts?.signal;
  if (signal?.aborted) return undefined;
  const ask = request("ui.dialog", { method, ...fields, timeout: opts?.timeout }).catch(() => undefined);
  if (!signal) return ask;
  return Promise.race([ask, new Promise((resolve) => signal.addEventListener("abort", () => resolve(undefined), { once: true }))]);
}

// A surface that needs no answer (status, widget, title ...). Frontends without a screen ignore it.
const surface = (method, fields) => {
  if (state.hasUI) notify("ui", { method, ...fields });
};

function makeUi(sink) {
  const note = (message, type) => {
    if (sink) sink.push(String(message));
    else if (state.hasUI) notify("ui", { method: "notify", message: String(message), notifyType: type ?? "info" });
    else log(message);
  };
  return degrade(
    {
      notify: note,
      select: async (title, options, opts) => {
        const r = await dialog("select", { title, options: (options ?? []).map(String) }, opts);
        return r && !r.cancelled && typeof r.value === "string" ? r.value : undefined;
      },
      confirm: async (title, message, opts) => {
        const r = await dialog("confirm", { title, message }, opts);
        return !!r && !r.cancelled && r.confirmed === true;
      },
      input: async (title, placeholder, opts) => {
        const r = await dialog("input", { title, placeholder }, opts);
        return r && !r.cancelled && typeof r.value === "string" ? r.value : undefined;
      },
      editor: async (title, prefill, opts) => {
        const r = await dialog("editor", { title, prefill }, opts);
        return r && !r.cancelled && typeof r.value === "string" ? r.value : undefined;
      },
      setStatus: (key, text) => surface("setStatus", { statusKey: String(key), statusText: text == null ? undefined : String(text) }),
      setWidget: (key, content, opts) => {
        // component factories need a terminal UI; only plain lines travel
        const lines = Array.isArray(content) ? content.map(String) : undefined;
        surface("setWidget", { widgetKey: String(key), widgetLines: lines, widgetPlacement: opts?.placement });
      },
      setTitle: (title) => surface("setTitle", { title: String(title ?? "") }),
      setWorkingMessage: (message) => surface("setWorkingMessage", { message: message == null ? undefined : String(message) }),
      setEditorText: (text) => surface("set_editor_text", { text: String(text ?? "") }),
      setFooter: inert("ctx.ui.setFooter"),
      setHeader: inert("ctx.ui.setHeader"),
      setWorkingIndicator: inert("ctx.ui.setWorkingIndicator"),
      setWorkingVisible: inert("ctx.ui.setWorkingVisible"),
    },
    "ctx.ui",
  );
}

// piCtx builds the context for one handler call. sink collects ctx.ui.notify output for a command that returns it to the user. signal is
// the abort signal of the host request being served (undefined outside one). A tool's execute also gets tools and executeTool.
export function piCtx(sink, signal, forTool = false) {
  const base = {
    cwd: state.cwd,
    hasUI: state.hasUI,
    mode: state.mode,
    signal,
    ui: makeUi(sink),
    isProjectTrusted: () => state.trusted,
    sessionManager: { getEntries: () => [], getBranch: () => [] },
    isIdle: () => true,
    abort: inert("ctx.abort"),
    hasPendingMessages: () => false,
    shutdown: () => notify("shutdown_request", {}),
    getContextUsage: inert("ctx.getContextUsage"),
    compact: inert("ctx.compact"),
    getSystemPrompt: inert("ctx.getSystemPrompt", ""),
  };
  if (forTool) {
    base.tools = state.hostTools.map((t) => ({ ...t }));
    base.executeTool = async (name, args) => {
      try {
        const r = await request("tools.execute", { name: String(name), args: args ?? {} });
        return { content: [{ type: "text", text: r.content ?? "" }], isError: !!r.isError, details: {} };
      } catch (e) {
        return { content: [{ type: "text", text: String(e?.message ?? e) }], isError: true, details: {} };
      }
    };
  }
  return degrade(base, "ctx");
}
