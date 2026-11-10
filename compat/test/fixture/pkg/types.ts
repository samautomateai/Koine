/* block comment mentioning interface ExtensionAPI { fake(): void; } */
export interface ExtensionUIContext {
  notify(message: string, type?: "info" | "error"): void;
  readonly theme: Theme; // trailing comment
  select<T>(title: string, options: T[]): Promise<T | undefined>;
  brandNewWidget(): void;
}
export interface ExtensionContext {
  ui: ExtensionUIContext;
  cwd: string;
  signal?: AbortSignal;
}
export interface ExtensionAPI {
  on(event: "session_start", handler: (e: unknown) => void): void;
  on(
    event: "tool_call",
    handler: (e: unknown, ctx: ExtensionContext) => Promise<void>,
  ): void;
  on(event: "brand_new_event", handler: () => void): void;
  registerTool<T extends object>(tool: T): void;
  registerCommand(name: string, opts: { description: string; handler: () => void }): void;
  readonly events: Record<string, string>;
  surprise(): void;
}
export interface ToolDefinition {
  name: string;
  label?: string;
  execute(id: string, params: unknown): Promise<unknown>;
}
export interface ExtensionToolContext {
  cwd: string;
}
export interface ExtensionCommandContext {
  waitForIdle(): Promise<void>;
}
