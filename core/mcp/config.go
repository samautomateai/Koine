package mcp

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"koine/core/agentdir"
)

// Exposure modes for a server's tools (Pi's names). Koine has no codemode sandbox, so `codemode` (Pi's default) is read as `deferred`.
const (
	ExposureDirect   = "direct"   // declared to the model
	ExposureDeferred = "deferred" // found with tool_search, declared from the next model call on
	ExposureHidden   = "hidden"   // never offered
)

// Source says which file a server came from.
type Source string

// Where servers are configured, in the order they are read; a later one replaces an earlier one with the same name.
const (
	SourceUser    Source = "user"    // <agent dir>/mcp.json, and opencode's user config
	SourceProject Source = "project" // .koine/mcp.json, .pi/mcp.json, .mcp.json and opencode.json in the project: code, so trust-gated
)

// OAuthConfig is the `oauth` object of an HTTP server.
type OAuthConfig struct {
	ClientID              string `json:"clientId,omitempty"`
	ClientSecret          string `json:"clientSecret,omitempty"` // may be ${ENV} or !command
	CallbackPort          int    `json:"callbackPort,omitempty"`
	CallbackURL           string `json:"callbackUrl,omitempty"`
	Scope                 string `json:"scope,omitempty"`
	ClientName            string `json:"clientName,omitempty"`
	ClientRegistration    string `json:"clientRegistration,omitempty"` // auto, dcr or cimd
	AuthServerMetadataURL string `json:"authServerMetadataUrl,omitempty"`
}

// ToolExposure is the toolExposure object: patterns in file order, because among patterns the first match wins.
type ToolExposure struct {
	Keys []string
	Mode map[string]string
}

// UnmarshalJSON keeps the key order of the object.
func (te *ToolExposure) UnmarshalJSON(b []byte) error {
	te.Keys, te.Mode = nil, map[string]string{}
	dec := json.NewDecoder(bytes.NewReader(b))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return errors.New("toolExposure must be an object")
	}
	for dec.More() {
		k, err := dec.Token()
		if err != nil {
			return err
		}
		var v string
		if err := dec.Decode(&v); err != nil {
			return errors.New("toolExposure values must be strings")
		}
		key := k.(string)
		if _, dup := te.Mode[key]; !dup {
			te.Keys = append(te.Keys, key)
		}
		te.Mode[key] = v
	}
	return nil
}

// MarshalJSON writes the keys in order.
func (te ToolExposure) MarshalJSON() ([]byte, error) {
	var sb strings.Builder
	sb.WriteByte('{')
	for i, k := range te.Keys {
		if i > 0 {
			sb.WriteByte(',')
		}
		kb, _ := json.Marshal(k)
		vb, _ := json.Marshal(te.Mode[k])
		sb.Write(kb)
		sb.WriteByte(':')
		sb.Write(vb)
	}
	sb.WriteByte('}')
	return []byte(sb.String()), nil
}

// ModeFor is the exposure of one tool: an exact key wins over patterns, and among patterns the first match wins. A key is matched against the
// tool's own name and against its registered name (mcp__server__tool). def is the server's exposure.
func (te ToolExposure) ModeFor(tool, registered, def string) string {
	for _, k := range te.Keys {
		if !strings.ContainsAny(k, "*?") && (k == tool || k == registered) {
			return te.Mode[k]
		}
	}
	for _, k := range te.Keys {
		if strings.ContainsAny(k, "*?") && (globMatch(k, tool) || globMatch(k, registered)) {
			return te.Mode[k]
		}
	}
	return def
}

func globMatch(pattern, s string) bool {
	// QuoteMeta escapes the wildcards, so turn the escaped forms into their regexp meaning
	re := "^" + strings.ReplaceAll(strings.ReplaceAll(regexp.QuoteMeta(pattern), `\*`, ".*"), `\?`, ".") + "$"
	ok, _ := regexp.MatchString(re, s)
	return ok
}

// ServerConfig is one entry of mcpServers. It is the Claude Desktop, Claude Code and Cursor format plus Pi's additions.
type ServerConfig struct {
	Type    string            `json:"type,omitempty"`
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	Cwd     string            `json:"cwd,omitempty"`

	URL     string            `json:"url,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
	OAuth   *OAuthConfig      `json:"oauth,omitempty"`

	Timeout      float64       `json:"timeout,omitempty"` // seconds
	Enabled      *bool         `json:"enabled,omitempty"`
	Exposure     string        `json:"exposure,omitempty"`
	ToolExposure *ToolExposure `json:"toolExposure,omitempty"`
	Description  string        `json:"description,omitempty"`
}

// IsHTTP says whether the server is reached over streamable HTTP.
func (c ServerConfig) IsHTTP() bool { return c.URL != "" }

// Entry is a configured server with where it came from.
type Entry struct {
	Name   string
	Config ServerConfig
	Source Source
	File   string
}

// Disabled reports whether the entry is switched off.
func (e Entry) Disabled() bool { return e.Config.Enabled != nil && !*e.Config.Enabled }

// Problem is an invalid entry or unreadable file. The entry is skipped; the other servers still connect.
type Problem struct {
	File, Name, Msg string
}

func (p Problem) String() string {
	if p.Name == "" {
		return p.File + ": " + p.Msg
	}
	return fmt.Sprintf("%s: server %q: %s", p.File, p.Name, p.Msg)
}

var nameRE = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// Normalize is the identity of a server name: names that differ only in - and _ are the same server.
func Normalize(name string) string { return strings.ReplaceAll(name, "-", "_") }

var overrideKeys = map[string]bool{"enabled": true, "exposure": true, "toolExposure": true}

func validExposure(m string) bool {
	switch m {
	case "", ExposureDirect, ExposureDeferred, ExposureHidden, "codemode", "codemode-deferred":
		return true
	}
	return false
}

// Validate checks one entry and fills in the transport type.
func Validate(c *ServerConfig) error {
	switch c.Type {
	case "sse":
		return errors.New(`type "sse" (the legacy SSE transport) is not supported; use the streamable HTTP endpoint, often /mcp`)
	case "streamable-http", "http":
		c.Type = "http"
	case "stdio", "":
	default:
		return fmt.Errorf("unknown type %q (use stdio or http)", c.Type)
	}
	switch {
	case c.Command != "" && c.URL != "":
		return errors.New("has both command and url")
	case c.Command != "":
		if c.Type == "http" {
			return errors.New(`type "http" needs a url`)
		}
		c.Type = "stdio"
	case c.URL != "":
		if c.Type == "stdio" {
			return errors.New(`type "stdio" needs a command`)
		}
		c.Type = "http"
		if !strings.HasPrefix(c.URL, "http://") && !strings.HasPrefix(c.URL, "https://") && !strings.HasPrefix(c.URL, "${") {
			return fmt.Errorf("url %q must start with http:// or https://", c.URL)
		}
	default:
		return errors.New("needs a command (stdio) or a url (http)")
	}
	if !validExposure(c.Exposure) {
		return fmt.Errorf("unknown exposure %q (direct, deferred, hidden)", c.Exposure)
	}
	if c.ToolExposure != nil {
		for k, v := range c.ToolExposure.Mode {
			if !validExposure(v) || v == "" {
				return fmt.Errorf("toolExposure %q: unknown exposure %q", k, v)
			}
		}
	}
	if c.Timeout < 0 {
		return errors.New("timeout must not be negative")
	}
	return nil
}

// fileServers reads the mcpServers object of one file. A missing file is not an error.
func fileServers(path string) (map[string]json.RawMessage, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var top struct {
		MCPServers map[string]json.RawMessage `json:"mcpServers"`
	}
	if err := json.Unmarshal(b, &top); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}
	return top.MCPServers, nil
}

// Loaded is the result of reading all configuration.
type Loaded struct {
	Servers  []Entry // valid entries, sorted by name; disabled ones included
	Problems []Problem
	Skipped  int // project entries left out because the folder is untrusted
}

// UserFile is the user-level mcp.json.
func UserFile(home string) string { return filepath.Join(agentdir.Of(home), "mcp.json") }

// ProjectFile is the project-level mcp.json that `koine mcp add --local` writes.
func ProjectFile(cwd string) string { return filepath.Join(cwd, ".koine", "mcp.json") }

type source struct {
	path     string
	src      Source
	opencode bool
}

// sources lists the files in reading order: user files first, then project files (.pi, .koine, Claude's .mcp.json, opencode.json).
func sources(cwd, home string) []source {
	var out []source
	if d := agentdir.Of(home); d != "" {
		out = append(out, source{path: filepath.Join(d, "mcp.json"), src: SourceUser})
	}
	if home != "" {
		out = append(out, source{path: filepath.Join(home, ".pi", "agent", "mcp.json"), src: SourceUser})
		out = append(out, source{path: filepath.Join(home, ".config", "opencode", "opencode.json"), src: SourceUser, opencode: true})
	}
	out = append(out,
		source{path: filepath.Join(cwd, ".mcp.json"), src: SourceProject},
		source{path: filepath.Join(cwd, "opencode.json"), src: SourceProject, opencode: true},
		source{path: filepath.Join(cwd, ".pi", "mcp.json"), src: SourceProject},
		source{path: filepath.Join(cwd, ".koine", "mcp.json"), src: SourceProject})
	return out
}

// Load reads user and project configuration. Project files are only read when trusted; otherwise their servers are counted in Skipped (the
// files are still opened to count them, never acted on). A project entry replaces a user entry of the same name, except that an entry with
// only enabled, exposure or toolExposure changes just those fields of the earlier one.
func Load(cwd, home string, trusted bool) Loaded {
	var res Loaded
	merged := map[string]*Entry{}
	owner := map[string]string{} // normalized name -> name as first written
	for _, s := range sources(cwd, home) {
		var raw map[string]json.RawMessage
		var err error
		if s.opencode {
			raw, err = opencodeServers(s.path)
		} else {
			raw, err = fileServers(s.path)
		}
		if err != nil {
			res.Problems = append(res.Problems, Problem{File: s.path, Msg: err.Error()})
			continue
		}
		if s.src == SourceProject && !trusted {
			res.Skipped += len(raw)
			continue
		}
		names := make([]string, 0, len(raw))
		for n := range raw {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, name := range names {
			e, err := buildEntry(name, raw[name], merged, owner, s)
			if err != nil {
				res.Problems = append(res.Problems, Problem{File: s.path, Name: name, Msg: err.Error()})
				continue
			}
			merged[Normalize(name)] = e
			owner[Normalize(name)] = e.Name
		}
	}
	for _, e := range merged {
		res.Servers = append(res.Servers, *e)
	}
	sort.Slice(res.Servers, func(i, j int) bool { return res.Servers[i].Name < res.Servers[j].Name })
	return res
}

func buildEntry(name string, raw json.RawMessage, merged map[string]*Entry, owner map[string]string, s source) (*Entry, error) {
	if !nameRE.MatchString(name) {
		return nil, errors.New("name may only use letters, digits, _ and -")
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(raw, &keys); err != nil {
		return nil, errors.New("entry must be an object")
	}
	key := Normalize(name)
	prev := merged[key]
	if prev != nil && owner[key] != name && prev.File == s.path {
		return nil, fmt.Errorf("same server as %q (names differing only in - and _ are the same)", owner[key])
	}
	cfg := ServerConfig{}
	overrideOnly := len(keys) > 0
	for k := range keys {
		if !overrideKeys[k] {
			overrideOnly = false
		}
	}
	if overrideOnly && prev != nil {
		cfg = prev.Config
	}
	if err := json.Unmarshal(raw, &cfg); err != nil { // fields other clients add (alwaysAllow, autoApprove …) are ignored
		return nil, err
	}
	if err := Validate(&cfg); err != nil {
		return nil, err
	}
	e := &Entry{Name: name, Config: cfg, Source: s.src, File: s.path}
	if overrideOnly && prev != nil { // a switch-only entry changes how the server is offered, not where it comes from
		e.Name, e.Source, e.File = prev.Name, prev.Source, prev.File
	}
	return e, nil
}

// opencodeServers reads the `mcp` block of an opencode.json and converts it to the mcpServers shape: `local` (command array, environment)
// becomes stdio and `remote` becomes HTTP; {env:NAME} becomes ${NAME}.
func opencodeServers(path string) (map[string]json.RawMessage, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var top struct {
		MCP map[string]struct {
			Type        string            `json:"type"`
			Command     []string          `json:"command"`
			Environment map[string]string `json:"environment"`
			URL         string            `json:"url"`
			Headers     map[string]string `json:"headers"`
			Enabled     *bool             `json:"enabled"`
			Timeout     float64           `json:"timeout"`
		} `json:"mcp"`
	}
	if err := json.Unmarshal(stripJSONC(b), &top); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}
	envRef := regexp.MustCompile(`\{env:([A-Za-z_][A-Za-z0-9_]*)\}`)
	conv := func(m map[string]string) map[string]string {
		if m == nil {
			return nil
		}
		out := map[string]string{}
		for k, v := range m {
			out[k] = envRef.ReplaceAllString(v, "$${$1}")
		}
		return out
	}
	out := map[string]json.RawMessage{}
	for name, s := range top.MCP {
		c := ServerConfig{Enabled: s.Enabled}
		if s.Timeout > 0 {
			c.Timeout = s.Timeout / 1000 // opencode counts milliseconds
		}
		switch s.Type {
		case "local":
			if len(s.Command) == 0 {
				continue
			}
			c.Command, c.Args, c.Env = s.Command[0], s.Command[1:], conv(s.Environment)
		case "remote":
			c.URL, c.Headers = s.URL, conv(s.Headers)
		default:
			continue
		}
		jb, _ := json.Marshal(c)
		out[name] = jb
	}
	return out, nil
}

// stripJSONC removes // and /* */ comments and trailing commas, which opencode.json may contain.
func stripJSONC(b []byte) []byte {
	var out bytes.Buffer
	inStr, esc := false, false
	for i := 0; i < len(b); i++ {
		c := b[i]
		switch {
		case inStr:
			out.WriteByte(c)
			if esc {
				esc = false
			} else if c == '\\' {
				esc = true
			} else if c == '"' {
				inStr = false
			}
		case c == '"':
			inStr = true
			out.WriteByte(c)
		case c == '/' && i+1 < len(b) && b[i+1] == '/':
			for i < len(b) && b[i] != '\n' {
				i++
			}
			out.WriteByte('\n')
		case c == '/' && i+1 < len(b) && b[i+1] == '*':
			i += 2
			for i+1 < len(b) && (b[i] != '*' || b[i+1] != '/') {
				i++
			}
			i++
		default:
			out.WriteByte(c)
		}
	}
	return regexp.MustCompile(`,(\s*[}\]])`).ReplaceAll(out.Bytes(), []byte("$1"))
}
