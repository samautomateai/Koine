import { type Plugin, tool } from "@opencode-ai/plugin";

export const MyPlugin: Plugin = async ({ directory, $ }) => {
  return {
    tool: {
      oc_upper: tool({
        description: "Uppercase text (opencode plugin tool)",
        args: { text: tool.schema.string().describe("text to uppercase") },
        async execute(args) {
          return args.text.toUpperCase() + "@" + directory.split("/").pop();
        },
      }),
    },
    "tool.execute.after": async (input, output) => {
      if (input.tool === "bash" && output.output.includes("listed")) output.output += "[oc-after]";
    },
  };
};
