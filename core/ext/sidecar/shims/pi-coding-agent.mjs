// API surface Pi extensions import at runtime. Types are exported as undefined
// so type-stripped `import { ExtensionAPI }` statements still resolve.
import path from "node:path";

export const ExtensionAPI = undefined;
export const ExtensionContext = undefined;
export const ExtensionFactory = undefined;
export const Theme = undefined;
export const ToolDefinition = undefined;

export const defineTool = (d) => d;

// Runs fn after the earlier calls for the same file finished, so an overriding edit or write tool does not interleave its own steps. The queue
// is per extension process: it does not wait for Koine's built-in edit and write tools.
const fileQueues = new Map();
export function withFileMutationQueue(filePath, fn) {
  const key = path.resolve(String(filePath));
  const run = (fileQueues.get(key) ?? Promise.resolve()).then(fn);
  const tail = run.then(
    () => {},
    () => {},
  );
  fileQueues.set(key, tail);
  tail.then(() => fileQueues.get(key) === tail && fileQueues.delete(key));
  return run;
}
export const isToolCallEventType = (name, event) => event?.toolName === name;
export const isBashToolResult = (e) => e?.toolName === "bash";
export const isReadToolResult = (e) => e?.toolName === "read";
export const isEditToolResult = (e) => e?.toolName === "edit";
export const isWriteToolResult = (e) => e?.toolName === "write";
export const getAgentDir = () => (process.env.HOME || "") + "/.pi/agent";
export const VERSION = "koine-compat";
export default {};
