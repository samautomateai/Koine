package tools

import (
	"encoding/json"
	"os"
	"slices"
)

func schema(props map[string]any, required ...string) json.RawMessage {
	s := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		s["required"] = required
	}
	b, _ := json.Marshal(s)
	return b
}

func str(desc string) map[string]any  { return map[string]any{"type": "string", "description": desc} }
func num(desc string) map[string]any  { return map[string]any{"type": "number", "description": desc} }
func flag(desc string) map[string]any { return map[string]any{"type": "boolean", "description": desc} }

// Options configure the built-in tools.
type Options struct {
	Cwd                string
	Home               string // for "~" in paths (default: the user's home directory)
	ShellPath          string // the shellPath setting: the shell bash runs commands with (default bash, else sh)
	ShellCommandPrefix string // the shellCommandPrefix setting: put before every bash command
	Ripgrep            string // "" = use rg when it is on the PATH, "-" = never, otherwise the executable to use
	// ShellEnv is called for every command and returns "NAME=value" entries added to the process environment (the session and model).
	ShellEnv func() []string
}

// DefaultTools are the built-ins offered to the model unless the user chooses others, as in Pi. grep, find and ls are opt-in.
var DefaultTools = []string{"read", "bash", "edit", "write"}

// BuiltinNames lists every built-in tool, in the order they appear in the system prompt.
var BuiltinNames = []string{"read", "bash", "edit", "write", "grep", "find", "ls"}

// OptionalBuiltins are the built-ins that are off unless asked for: grep, find and ls.
func OptionalBuiltins() []string {
	var out []string
	for _, n := range BuiltinNames {
		if !slices.Contains(DefaultTools, n) {
			out = append(out, n)
		}
	}
	return out
}

// RegisterBuiltins adds read, bash, edit, write, grep, find and ls rooted at cwd.
func RegisterBuiltins(r *Registry, cwd string) { RegisterWith(r, Options{Cwd: cwd}) }

// RegisterBuiltinsEnv is RegisterBuiltins with extra environment for the bash tool. shellEnv is called for every command, so it can report
// the current session and model; the entries are "NAME=value" and come after the process environment.
func RegisterBuiltinsEnv(r *Registry, cwd string, shellEnv func() []string) {
	RegisterWith(r, Options{Cwd: cwd, ShellEnv: shellEnv})
}

// RegisterWith adds the built-in tools with the given options.
func RegisterWith(r *Registry, o Options) {
	if o.Home == "" {
		o.Home, _ = os.UserHomeDir()
	}
	f := fileTools{o}
	r.Register(&Func{N: "read", F: f.read,
		D: "Read the contents of a text file or an image (jpg, png, gif, webp, bmp; images are attached). Output is truncated to 2000 lines or " +
			"50KB (whichever is hit first); use offset/limit for large files, and continue with offset until the file is complete.",
		S: schema(map[string]any{
			"path": str("Path of the file to read (relative or absolute)"), "offset": num("Line to start from (1-based)"),
			"limit": num("Maximum number of lines to read"),
		}, "path"),
		Snippet:    "Read file contents",
		Guidelines: []string{"Use read to examine files instead of cat or sed."}})
	r.Register(&Func{N: "bash", F: o.bash,
		D: "Execute a bash command in the working directory. Returns the last 2000 lines or 50KB of stdout and stderr (whichever is hit first); " +
			"when the output is cut, the full output is saved to a temp file whose path is given. Default timeout 120 seconds.",
		S:       schema(map[string]any{"command": str("Command to execute"), "timeout": num("Timeout in seconds (default 120)")}, "command"),
		Snippet: "Execute bash commands (ls, grep, find, etc.)"})
	r.Register(&Func{N: "edit", F: f.edit,
		D: "Edit a file with exact text replacement. Each edits[].oldText must match a unique region of the original file; all edits are matched " +
			"against the original, must not overlap, and are applied together.",
		S: schema(map[string]any{"path": str("Path of the file to edit (relative or absolute)"), "edits": map[string]any{
			"type": "array", "description": "One or more replacements, each matched against the original file",
			"items": map[string]any{"type": "object", "properties": map[string]any{
				"oldText": str("Exact text to replace. Must be unique in the file and not overlap another edit."),
				"newText": str("Replacement text"),
			}, "required": []string{"oldText", "newText"}},
		}}, "path", "edits"),
		Snippet: "Make precise file edits with exact text replacement, including multiple disjoint edits in one call",
		Guidelines: []string{
			"Use edit for precise changes (edits[].oldText must match exactly).",
			"When changing multiple separate locations in one file, use one edit call with multiple entries in edits[] instead of multiple edit calls.",
			"Each edits[].oldText is matched against the original file, not after earlier edits are applied. " +
				"Do not emit overlapping or nested edits. Merge nearby changes into one edit.",
			"Keep edits[].oldText as small as possible while still being unique in the file. Do not pad with large unchanged regions.",
		}})
	r.Register(&Func{N: "write", F: f.write,
		D: "Write content to a file. Creates the file if it does not exist, overwrites it if it does, and creates parent directories.",
		S: schema(map[string]any{"path": str("Path of the file to write (relative or absolute)"), "content": str("Content to write")},
			"path", "content"),
		Snippet:    "Create or overwrite files",
		Guidelines: []string{"Use write only for new files or complete rewrites."}})
	r.Register(&Func{N: "grep", F: o.grep,
		D: "Search file contents for a pattern (a regular expression unless literal is set). Returns matching lines with file paths and line " +
			"numbers. Respects .gitignore. Output is limited to 100 matches (limit) or 50KB, and lines to 500 characters.",
		S: schema(map[string]any{
			"pattern":    str("Search pattern (regular expression, or a literal string with literal=true)"),
			"path":       str("Directory or file to search (default: current directory)"),
			"glob":       str("Only search files matching this glob, e.g. '*.ts' or '**/*.spec.ts'"),
			"ignoreCase": flag("Case-insensitive search (default false)"),
			"literal":    flag("Treat the pattern as a literal string instead of a regular expression (default false)"),
			"context":    num("Lines to show before and after each match (default 0)"),
			"limit":      num("Maximum number of matches (default 100)"),
		}, "pattern"),
		Snippet: "Search file contents for patterns (respects .gitignore)"})
	r.Register(&Func{N: "find", F: o.find,
		D: "Find files by glob pattern, e.g. '*.ts' or 'src/**/*.spec.ts'. Returns paths relative to the search directory. Respects .gitignore. " +
			"Output is limited to 1000 results (limit) or 50KB.",
		S: schema(map[string]any{
			"pattern": str("Glob pattern to match files"), "path": str("Directory to search (default: current directory)"),
			"limit": num("Maximum number of results (default 1000)"),
		}, "pattern"),
		Snippet: "Find files by glob pattern (respects .gitignore)"})
	r.Register(&Func{N: "ls", F: o.ls,
		D: "List directory contents, sorted alphabetically, with a '/' suffix on directories. Includes dotfiles. " +
			"Output is limited to 500 entries (limit) or 50KB.",
		S:       schema(map[string]any{"path": str("Directory to list (default: current directory)"), "limit": num("Maximum number of entries (default 500)")}),
		Snippet: "List directory contents"})
}
