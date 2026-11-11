package main

import (
	"fmt"
	"os"
	"strings"

	"koine/core/llm"
)

// cliArgs is the parsed command line: `koine [options] [--] [@files] [messages]`.
type cliArgs struct {
	Print        bool
	Mode         string // text | json
	Serve        string
	Dir          string
	Trust        bool
	Compat       bool
	Version      bool
	Help         bool
	Model        string
	System       string
	AppendSystem string
	Tools        []string
	NoTools      bool
	Thinking     string
	Continue     bool
	Resume       bool // list sessions and exit (line-mode stand-in for Pi's picker)
	Session      string
	SessionDir   string
	NoSession    bool
	Fork         string
	Name         string
	Files        []string // @file arguments, in order
	Message      []string // everything else
}

type flagKind int

const (
	boolFlag flagKind = iota
	valueFlag
)

// flagSpec names a flag by its canonical long name; Short and Aliases are other accepted spellings.
type flagSpec struct {
	Long  string
	Short string
	Kind  flagKind
}

var flagSpecs = []flagSpec{
	{"print", "p", boolFlag},
	{"mode", "", valueFlag},
	{"serve", "", valueFlag},
	{"cwd", "C", valueFlag},
	{"trust", "a", boolFlag},
	{"compat", "", boolFlag},
	{"version", "v", boolFlag},
	{"help", "h", boolFlag},
	{"model", "", valueFlag},
	{"system-prompt", "", valueFlag},
	{"append-system-prompt", "", valueFlag},
	{"tools", "t", valueFlag},
	{"no-tools", "nt", boolFlag},
	{"thinking", "", valueFlag},
	{"continue", "c", boolFlag},
	{"resume", "r", boolFlag},
	{"session", "", valueFlag},
	{"session-dir", "", valueFlag},
	{"no-session", "", boolFlag},
	{"fork", "", valueFlag},
	{"name", "n", valueFlag},
}

func lookup(name string) (flagSpec, bool) {
	for _, f := range flagSpecs {
		if name == f.Long || (f.Short != "" && name == f.Short) {
			return f, true
		}
	}
	return flagSpec{}, false
}

// parseArgs parses args. Long flags take `--name value` or `--name=value`; the older single-dash spelling (`-trust`, `-serve addr`) still works.
// `--` ends flag parsing. Arguments starting with @ name files to include; everything else is the message.
func parseArgs(args []string) (cliArgs, error) {
	var c cliArgs
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--":
			c.addPositional(args[i+1:])
			i = len(args)
			continue
		case len(a) < 2 || a[0] != '-':
			c.addPositional([]string{a})
			continue
		}
		name := strings.TrimLeft(a, "-")
		val, hasVal := "", false
		if k := strings.IndexByte(name, '='); k >= 0 {
			name, val, hasVal = name[:k], name[k+1:], true
		}
		f, ok := lookup(name)
		if !ok {
			return c, fmt.Errorf("unknown option %q (try --help)", a)
		}
		if f.Kind == valueFlag && !hasVal {
			if i+1 >= len(args) {
				return c, fmt.Errorf("option %s needs a value", a)
			}
			val = args[i+1]
			i++
		} else if f.Kind == boolFlag && hasVal {
			return c, fmt.Errorf("option %s does not take a value", a)
		}
		c.set(f.Long, val)
	}
	if c.Mode != "" && c.Mode != "text" && c.Mode != "json" {
		return c, fmt.Errorf("--mode must be text or json (got %q)", c.Mode)
	}
	if c.Thinking != "" && !llm.ValidLevel(c.Thinking) {
		return c, fmt.Errorf("--thinking must be one of %s (got %q)", strings.Join(llm.ThinkingLevels, ", "), c.Thinking)
	}
	return c, nil
}

func (c *cliArgs) set(long, val string) {
	switch long {
	case "print":
		c.Print = true
	case "mode":
		c.Mode = val
	case "serve":
		c.Serve = val
	case "cwd":
		c.Dir = val
	case "trust":
		c.Trust = true
	case "compat":
		c.Compat = true
	case "version":
		c.Version = true
	case "help":
		c.Help = true
	case "model":
		c.Model = val
	case "system-prompt":
		c.System = val
	case "append-system-prompt":
		c.AppendSystem = val
	case "tools":
		for _, t := range strings.Split(val, ",") {
			if t = strings.TrimSpace(t); t != "" {
				c.Tools = append(c.Tools, t)
			}
		}
	case "no-tools":
		c.NoTools = true
	case "thinking":
		c.Thinking = val
	case "continue":
		c.Continue = true
	case "resume":
		c.Resume = true
	case "session":
		c.Session = val
	case "session-dir":
		c.SessionDir = val
	case "no-session":
		c.NoSession = true
	case "fork":
		c.Fork = val
	case "name":
		c.Name = val
	}
}

func (c *cliArgs) addPositional(vals []string) {
	for _, v := range vals {
		if len(v) > 1 && v[0] == '@' {
			c.Files = append(c.Files, v[1:])
		} else {
			c.Message = append(c.Message, v)
		}
	}
}

// expandFiles renders the @file arguments as <file> blocks to put before the message.
func expandFiles(dir string, files []string) (string, error) {
	var b strings.Builder
	for _, f := range files {
		p := f
		if !strings.HasPrefix(p, "/") {
			p = dir + string(os.PathSeparator) + p
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return "", fmt.Errorf("@%s: %w", f, err)
		}
		fmt.Fprintf(&b, "<file name=%q>\n%s\n</file>\n", f, strings.TrimRight(string(data), "\n"))
	}
	return b.String(), nil
}

const usage = `usage: koine [options] [--] [@files] [message]

Runs one prompt and prints the answer when a message is given, stdin is piped, or stdout is redirected; otherwise starts the interactive session.
Piped stdin is put before the message. @file arguments are included as <file> blocks.

  -p, --print                  run non-interactively (default when stdin or stdout is not a terminal)
      --mode text|json         text: final answer on stdout (default); json: one event per line on stdout
      --model ID[:LEVEL]       model to use (must resolve to a configured provider); :LEVEL sets the thinking level
      --thinking LEVEL         thinking level: off, minimal, low, medium, high, xhigh or max (beats a :LEVEL suffix and the settings)
      --system-prompt TEXT     replace the base system prompt (text, or a file path)
      --append-system-prompt T add to the system prompt (text, or a file path)
  -t, --tools a,b              offer only these tools
  -nt, --no-tools              offer no tools
  -c, --continue               continue the most recent session in this project
  -r, --resume                 list this project's sessions (resume one with --session ID, or /resume ID in the session)
      --session ID|PATH|NAME   resume that session
      --fork ID|PATH|NAME      start a new session from a copy of that one
  -n, --name NAME              name the session
      --no-session             do not save the conversation
      --session-dir DIR        keep sessions here (default ~/.koine/sessions, or $KOINE_SESSION_DIR)
  -C, --cwd DIR                project directory (default: current)
  -a, --trust                  trust this folder for this run
      --serve ADDR             serve the HTTP/SSE API (e.g. 127.0.0.1:7777)
      --compat                 print Pi compatibility status
  -v, --version                print version
  -h, --help                   this text

Answers go to stdout; tool activity, notices and errors go to stderr. Exit codes: 0 ok, 1 failed, 2 usage.
`
