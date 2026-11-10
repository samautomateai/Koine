// API surface Pi extensions import at runtime. Types are exported as undefined
// so type-stripped `import { ExtensionAPI }` statements still resolve.
export const ExtensionAPI = undefined;
export const ExtensionContext = undefined;
export const ExtensionFactory = undefined;
export const Theme = undefined;
export const ToolDefinition = undefined;

export const defineTool = (d) => d;
export const isToolCallEventType = (name, event) => event?.toolName === name;
export const isBashToolResult = (e) => e?.toolName === "bash";
export const isReadToolResult = (e) => e?.toolName === "read";
export const isEditToolResult = (e) => e?.toolName === "edit";
export const isWriteToolResult = (e) => e?.toolName === "write";
export const getAgentDir = () => (process.env.HOME || "") + "/.pi/agent";
export const VERSION = "koine-compat";
export default {};
