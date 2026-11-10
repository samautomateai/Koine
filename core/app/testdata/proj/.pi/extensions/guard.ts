import type { ExtensionAPI } from "@mariozechner/pi-coding-agent";
import { Type } from "@sinclair/typebox";

export default function (pi: ExtensionAPI) {
  pi.registerTool({
    name: "pi_echo",
    label: "Pi Echo",
    description: "Echo text (Pi extension tool)",
    parameters: Type.Object({ text: Type.String() }),
    async execute(_id, params) {
      return { content: [{ type: "text", text: "pi:" + params.text }] };
    },
  });
  pi.on("tool_call", async (event) => {
    if (event.toolName === "bash" && String(event.input.command).includes("rm -rf")) {
      return { block: true, reason: "pi guard: destructive" };
    }
  });
}
