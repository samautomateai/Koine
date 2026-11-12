package ext

import (
	"context"
	"encoding/json"
	"strconv"

	"koine/core/srcinfo"
)

// Command is a slash command an extension registered (pi.registerCommand).
type Command struct {
	Name, Description string
	// Invocation is what the user types after the slash. It is Name, unless several extensions registered the same name: Pi then keeps
	// them all and numbers them in load order, "review:1", "review:2".
	Invocation     string
	HasCompletions bool // the command offers argument completion (CompleteCommand)
	Source         srcinfo.Info
	proc           *Proc
}

// CompletionItem is one suggestion for the argument of a command.
type CompletionItem struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// addCommand records a command. A second registration of the same name by the same extension replaces the first.
func (h *Host) addCommand(c Command) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for i := range h.commands {
		if h.commands[i].proc == c.proc && h.commands[i].Name == c.Name {
			h.commands[i] = c
			return
		}
	}
	h.commands = append(h.commands, c)
}

// Commands lists the registered commands in load order, each with the name to invoke it by.
func (h *Host) Commands() []Command {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.invocations()
}

func (h *Host) invocations() []Command {
	count := map[string]int{}
	for _, c := range h.commands {
		count[c.Name]++
	}
	seen := map[string]int{}
	out := make([]Command, 0, len(h.commands))
	for _, c := range h.commands {
		c.Invocation = c.Name
		if count[c.Name] > 1 {
			seen[c.Name]++
			c.Invocation = c.Name + ":" + strconv.Itoa(seen[c.Name])
		}
		out = append(out, c)
	}
	return out
}

func (h *Host) command(invocation string) (Command, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, c := range h.invocations() {
		if c.Invocation == invocation {
			return c, true
		}
	}
	return Command{}, false
}

// RunCommand runs an extension-registered slash command by the name the user typed.
func (h *Host) RunCommand(ctx context.Context, name, args string) (string, bool, error) {
	c, ok := h.command(name)
	if !ok {
		return "", false, nil
	}
	res, err := c.proc.call(ctx, "command.execute", map[string]any{"name": c.Name, "args": args})
	if err != nil {
		return "", true, err
	}
	var r struct{ Output string }
	_ = json.Unmarshal(res, &r)
	return r.Output, true, nil
}

// CompleteCommand asks the command for suggestions for the argument typed so far. A command without completions, or one that fails,
// offers none.
func (h *Host) CompleteCommand(ctx context.Context, name, prefix string) []CompletionItem {
	c, ok := h.command(name)
	if !ok || !c.HasCompletions {
		return nil
	}
	res, err := c.proc.call(ctx, "command.complete", map[string]any{"name": c.Name, "prefix": prefix})
	if err != nil {
		return nil
	}
	var r struct{ Items []CompletionItem }
	_ = json.Unmarshal(res, &r)
	return r.Items
}
