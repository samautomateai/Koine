package pkgs

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"koine/core/agentdir"
)

// Scope says which settings file a package is declared in.
type Scope string

// The two scopes.
const (
	User    Scope = "user"
	Project Scope = "project"
)

// Entry is one item of the `packages` setting: a source string, or an object that narrows what the package loads.
type Entry struct {
	Source string
	// Resource filters. nil loads everything of that kind, an empty list loads none; see Filter for the patterns.
	Extensions, Skills, Prompts, Themes []string
	// Autoload false makes a project entry a filter over the user's entry for the same package instead of a package of its own.
	Autoload *bool
}

// Filtered says whether the entry narrows anything, which decides whether it is written as an object.
func (e Entry) Filtered() bool {
	return e.Extensions != nil || e.Skills != nil || e.Prompts != nil || e.Themes != nil || e.Autoload != nil
}

// UnmarshalJSON accepts the string form and the object form.
func (e *Entry) UnmarshalJSON(b []byte) error {
	var s string
	if json.Unmarshal(b, &s) == nil {
		*e = Entry{Source: s}
		return nil
	}
	var o struct {
		Source     string    `json:"source"`
		Extensions *[]string `json:"extensions"`
		Skills     *[]string `json:"skills"`
		Prompts    *[]string `json:"prompts"`
		Themes     *[]string `json:"themes"`
		Autoload   *bool     `json:"autoload"`
	}
	if err := json.Unmarshal(b, &o); err != nil {
		return fmt.Errorf("a package is a source string or an object with a source: %w", err)
	}
	if o.Source == "" {
		return errors.New("a package object needs a \"source\"")
	}
	*e = Entry{Source: o.Source, Autoload: o.Autoload}
	for dst, src := range map[*[]string]*[]string{&e.Extensions: o.Extensions, &e.Skills: o.Skills, &e.Prompts: o.Prompts, &e.Themes: o.Themes} {
		if src != nil {
			*dst = append([]string{}, *src...)
		}
	}
	return nil
}

// MarshalJSON writes a plain entry as its source string and a filtered one as an object.
func (e Entry) MarshalJSON() ([]byte, error) {
	if !e.Filtered() {
		return json.Marshal(e.Source)
	}
	type obj struct {
		Source     string    `json:"source"`
		Extensions *[]string `json:"extensions,omitempty"`
		Skills     *[]string `json:"skills,omitempty"`
		Prompts    *[]string `json:"prompts,omitempty"`
		Themes     *[]string `json:"themes,omitempty"`
		Autoload   *bool     `json:"autoload,omitempty"`
	}
	o := obj{Source: e.Source, Autoload: e.Autoload}
	for dst, src := range map[**[]string][]string{&o.Extensions: e.Extensions, &o.Skills: e.Skills, &o.Prompts: e.Prompts, &o.Themes: e.Themes} {
		if src != nil {
			s := src
			*dst = &s
		}
	}
	return json.Marshal(o)
}

// Dirs are the places of one scope: its settings file, the directory packages are installed into, and its lockfile.
type Dirs struct {
	Scope    Scope
	Settings string
	Root     string
	Lock     string
}

// DirsFor is where a scope keeps its files: the agent directory for the user, `<project>/.koine` for the project.
func DirsFor(s Scope, home, cwd string) Dirs {
	base := agentdir.Of(home)
	if s == Project {
		base = filepath.Join(cwd, ".koine")
	}
	if base == "" { // no home directory: there is nowhere to keep user packages
		return Dirs{Scope: s}
	}
	return Dirs{
		Scope: s, Settings: filepath.Join(base, "settings.json"), Root: filepath.Join(base, "packages"), Lock: filepath.Join(base, "packages.lock.json"),
	}
}

// BaseDir is what relative local paths in the scope's settings resolve from: the directory of the settings file.
func (d Dirs) BaseDir() string { return filepath.Dir(d.Settings) }

var jsonc = regexp.MustCompile(`(?m)^\s*//.*$|,\s*([}\]])`)

// ReadEntries reads the `packages` array of a settings file. A missing file or key is no packages. Entries that are not a string or an
// object are skipped and reported. Comments and trailing commas are tolerated, as everywhere else Koine reads settings.
func ReadEntries(path string) (entries []Entry, problems []string, err error) {
	doc, err := readDoc(path, true)
	if err != nil {
		return nil, nil, err
	}
	raw, ok := doc.vals["packages"]
	if !ok {
		return nil, nil, nil
	}
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, nil, fmt.Errorf("%s: packages should be an array", path)
	}
	for i, it := range items {
		var e Entry
		if err := json.Unmarshal(it, &e); err != nil {
			problems = append(problems, fmt.Sprintf("%s: packages[%d]: %v", path, i, err))
			continue
		}
		entries = append(entries, e)
	}
	return entries, problems, nil
}

// doc is a settings file as an ordered list of top-level keys, so rewriting `packages` leaves the rest as it was.
type doc struct {
	keys []string
	vals map[string]json.RawMessage
}

func readDoc(path string, lenient bool) (*doc, error) {
	d := &doc{vals: map[string]json.RawMessage{}}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) || (err == nil && len(bytes.TrimSpace(b)) == 0) {
		return d, nil
	}
	if err != nil {
		return nil, err
	}
	if perr := d.parse(b); perr != nil {
		if !lenient {
			if d.parse(jsonc.ReplaceAll(b, []byte("$1"))) == nil {
				return nil, fmt.Errorf("%s has comments or trailing commas, which Koine would lose when rewriting it; edit the packages array by hand", path)
			}
			return nil, fmt.Errorf("%s: %w", path, perr)
		}
		if perr = d.parse(jsonc.ReplaceAll(b, []byte("$1"))); perr != nil {
			return nil, fmt.Errorf("%s: %w", path, perr)
		}
	}
	return d, nil
}

func (d *doc) parse(b []byte) error {
	d.keys, d.vals = nil, map[string]json.RawMessage{}
	dec := json.NewDecoder(bytes.NewReader(b))
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return errors.New("settings must be a JSON object")
	}
	for dec.More() {
		k, err := dec.Token()
		if err != nil {
			return err
		}
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return err
		}
		key := k.(string)
		if _, dup := d.vals[key]; !dup {
			d.keys = append(d.keys, key)
		}
		d.vals[key] = v
	}
	_, err := dec.Token()
	return err
}

func (d *doc) marshal() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteString("{")
	for i, k := range d.keys {
		if i > 0 {
			buf.WriteString(",")
		}
		kb, _ := json.Marshal(k)
		buf.WriteString("\n  ")
		buf.Write(kb)
		buf.WriteString(": ")
		var ind bytes.Buffer
		if err := json.Indent(&ind, d.vals[k], "  ", "  "); err != nil {
			return nil, err
		}
		buf.Write(ind.Bytes())
	}
	if len(d.keys) > 0 {
		buf.WriteString("\n")
	}
	buf.WriteString("}\n")
	return buf.Bytes(), nil
}

// UpdateEntries reads the `packages` array of a settings file, lets fn change it and writes it back, leaving every other key where it
// was. The file is created when it does not exist. It refuses to rewrite a file that has comments, which a rewrite would lose.
func UpdateEntries(path string, fn func([]Entry) []Entry) error {
	d, err := readDoc(path, false)
	if err != nil {
		return err
	}
	var entries []Entry
	if raw, ok := d.vals["packages"]; ok {
		if err := json.Unmarshal(raw, &entries); err != nil {
			return fmt.Errorf("%s: packages should be an array of sources or objects: %w", path, err)
		}
	}
	entries = fn(entries)
	if len(entries) == 0 {
		delete(d.vals, "packages")
		for i, k := range d.keys {
			if k == "packages" {
				d.keys = append(d.keys[:i], d.keys[i+1:]...)
				break
			}
		}
	} else {
		b, err := json.Marshal(entries)
		if err != nil {
			return err
		}
		if _, ok := d.vals["packages"]; !ok {
			d.keys = append(d.keys, "packages")
		}
		d.vals["packages"] = b
	}
	out, err := d.marshal()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	mode := os.FileMode(0o644)
	if st, err := os.Stat(path); err == nil {
		mode = st.Mode().Perm()
	}
	return writeFileAtomic(path, out, mode)
}

func writeFileAtomic(path string, b []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".koine-*.tmp")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), mode); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
