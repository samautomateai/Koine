package ext

import "encoding/json"

// Flag is a command-line option an extension registered (pi.registerFlag). Type is "boolean" or "string".
type Flag struct {
	Name, Description, Type, Extension string
	Default                            any
}

// Flags returns the options the extensions registered, in the order they did.
func (h *Host) Flags() []Flag {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]Flag(nil), h.flags...)
}

// SetFlags tells every extension the command-line values of its options (a bool for a boolean flag, a string otherwise). It is sent
// after the extensions have loaded, because the command line cannot be read until their flags are known.
func (h *Host) SetFlags(values map[string]any) {
	if len(values) == 0 {
		return
	}
	params, err := json.Marshal(map[string]any{"values": values})
	if err != nil {
		return
	}
	h.mu.Lock()
	procs := append([]*Proc(nil), h.procs...)
	h.mu.Unlock()
	for _, p := range procs {
		p.write(msg{JSONRPC: "2.0", Method: "flags", Params: params})
	}
}
