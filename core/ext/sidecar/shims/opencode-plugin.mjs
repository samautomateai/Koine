import { z } from "./schema.mjs";

export function tool(def) { return def; }
tool.schema = z;

// Type-only names: exported so `import { Plugin }` survives type stripping.
export const Plugin = undefined;
export const PluginInput = undefined;
export const Hooks = undefined;
export const ToolContext = undefined;
export default { tool };
