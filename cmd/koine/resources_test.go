package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"koine/core/app"
)

// recorder is a fake Messages API that records what it was sent. With tool set it first asks for that tool, then answers with its result.
type recorder struct {
	*httptest.Server
	mu   sync.Mutex
	Reqs []map[string]any
	Keys []string
}

func newRecorder(t *testing.T, tool string, input map[string]any) *recorder {
	r := &recorder{}
	r.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		b, _ := io.ReadAll(req.Body)
		var body map[string]any
		_ = json.Unmarshal(b, &body)
		r.mu.Lock()
		r.Reqs = append(r.Reqs, body)
		r.Keys = append(r.Keys, req.Header.Get("x-api-key"))
		r.mu.Unlock()
		msgs := body["messages"].([]any)
		last := msgs[len(msgs)-1].(map[string]any)["content"].([]any)[0].(map[string]any)
		w.Header().Set("content-type", "application/json")
		if tool != "" && last["type"] != "tool_result" {
			_ = json.NewEncoder(w).Encode(map[string]any{"stop_reason": "tool_use", "content": []any{
				map[string]any{"type": "tool_use", "id": "call1", "name": tool, "input": input}}})
			return
		}
		text := "ok"
		if last["type"] == "tool_result" {
			text = "RESULT: " + last["content"].(string)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"content": []any{map[string]any{"type": "text", "text": text}}})
	}))
	t.Cleanup(r.Close)
	return r
}

func (r *recorder) toolNames(i int) []string {
	var names []string
	tl, _ := r.Reqs[i]["tools"].([]any)
	for _, td := range tl {
		names = append(names, td.(map[string]any)["name"].(string))
	}
	return names
}

func has(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func TestParseResourceArgs(t *testing.T) {
	good := []struct {
		name string
		in   []string
		want cliArgs
	}{
		{"exclude and builtin", []string{"-xt", "bash, write", "--exclude-tools=mcp__*", "-nbt"},
			cliArgs{resourceArgs: resourceArgs{ExcludeTools: []string{"bash", "write", "mcp__*"}, NoBuiltinTools: true}}},
		{"repeatable resources", []string{"-e", "a.ts", "--extension", "b", "--skill", "s", "--skill=t", "--prompt-template", "p", "-ne", "-ns", "-np", "-nc"},
			cliArgs{resourceArgs: resourceArgs{Extensions: []string{"a.ts", "b"}, Skills: []string{"s", "t"}, PromptTemplates: []string{"p"},
				NoExtensions: true, NoSkills: true, NoPromptTemplates: true, NoContextFiles: true}}},
		{"themes", []string{"--theme", "x", "--use-theme", "dark", "--no-themes"},
			cliArgs{resourceArgs: resourceArgs{Themes: []string{"x"}, UseTheme: "dark", NoThemes: true}}},
		{"models", []string{"--model", "sonnet", "--provider", "anthropic", "--api-key", "k", "--models", "a*,b", "--models=c"},
			cliArgs{Model: "sonnet", resourceArgs: resourceArgs{Provider: "anthropic", APIKey: "k", Models: []string{"a*", "b", "c"}}}},
		{"list models alone", []string{"--list-models"}, cliArgs{resourceArgs: resourceArgs{ListModels: true}}},
		{"list models with search", []string{"--list-models", "sonnet"}, cliArgs{resourceArgs: resourceArgs{ListModels: true, ListSearch: "sonnet"}}},
		{"list models then option", []string{"--list-models", "-p", "hi"},
			cliArgs{Print: true, Message: []string{"hi"}, resourceArgs: resourceArgs{ListModels: true}}},
		{"list models then file", []string{"--list-models=gpt", "@f"},
			cliArgs{Files: []string{"f"}, resourceArgs: resourceArgs{ListModels: true, ListSearch: "gpt"}}},
		{"tool adjustments", []string{"-t", "+codemode,-write"}, cliArgs{Tools: []string{"+codemode", "-write"}}},
		{"unknown long options are kept for extensions", []string{"--plan", "--label=x", "go"},
			cliArgs{Message: []string{"go"}, resourceArgs: resourceArgs{
				ExtFlags: map[string]any{"plan": true, "label": "x"}, UnknownLong: []string{"plan", "label"}}}},
	}
	for _, c := range good {
		got, err := parseArgs(c.in)
		if err != nil || !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: got %+v, %v\nwant %+v", c.name, got, err, c.want)
		}
	}
	for name, in := range map[string][]string{
		"provider needs model":   {"--provider", "anthropic"},
		"api key needs model":    {"--api-key", "k"},
		"mixed tool list":        {"-t", "read,+grep"},
		"unknown short option":   {"-zz"},
		"unknown single dash":    {"-nope"},
		"exclude needs a value":  {"-xt"},
		"extension needs a path": {"--extension"},
	} {
		if _, err := parseArgs(in); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
	if _, err := parseArgs([]string{"--api-key", "k", "--models", "x"}); err != nil {
		t.Errorf("--api-key with --models is allowed: %v", err)
	}
}

func TestParseWithExtensionTypes(t *testing.T) {
	types := map[string]string{"plan": "boolean", "label": "string", "quiet": "boolean"}
	c, err := parseArgsWith([]string{"--plan", "--label", "my label", "--quiet=false", "do it"}, types)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"plan": true, "label": "my label", "quiet": false}
	if !reflect.DeepEqual(c.ExtFlags, want) || !reflect.DeepEqual(c.Message, []string{"do it"}) {
		t.Errorf("%+v message %v", c.ExtFlags, c.Message)
	}
	if c, err = parseArgsWith([]string{"--label=x=y"}, types); err != nil || c.ExtFlags["label"] != "x=y" {
		t.Errorf("%+v %v", c.ExtFlags, err)
	}
	for _, bad := range [][]string{{"--other"}, {"--label"}} {
		if _, err := parseArgsWith(bad, types); err == nil {
			t.Errorf("%v: want an error", bad)
		}
	}
}

func TestToolOptionsEndToEnd(t *testing.T) {
	api := newRecorder(t, "", nil)
	isolate(t, api.URL)
	dir := t.TempDir()
	check := func(want, notWant []string, args ...string) {
		t.Helper()
		api.Reqs = nil
		code, _, errs := cli("", append(append([]string{"-C", dir}, args...), "hi")...)
		if code != 0 {
			t.Fatalf("%v: %d %s", args, code, errs)
		}
		names := api.toolNames(0)
		for _, w := range want {
			if !has(names, w) {
				t.Errorf("%v: %q missing from %v", args, w, names)
			}
		}
		for _, n := range notWant {
			if has(names, n) {
				t.Errorf("%v: %q should be absent from %v", args, n, names)
			}
		}
	}
	check([]string{"read", "bash", "grep"}, []string{"write"}, "-xt", "write")
	check([]string{"read"}, []string{"write", "bash", "edit"}, "-xt", "WRITE", "-xt", "b*,e?it")
	check([]string{"read", "grep"}, []string{"bash", "write", "edit", "ls", "glob"}, "-t", "r*,gr?p")
	check([]string{"read", "grep"}, []string{"grep2", "bash"}, "-t", "read,grep", "-xt", "bash")
	check([]string{"read", "bash"}, []string{"write", "edit"}, "-t", "-write,-edit,-ls,-glob,-grep")
	check([]string{"read", "bash", "write", "edit", "grep"}, []string{"ls"}, "-t", "-ls,+read")
	check(nil, []string{"read", "bash", "write", "edit", "grep", "ls", "glob"}, "-nbt")
	check(nil, []string{"read", "bash"}, "-nt", "-xt", "nothing")
	check([]string{"read"}, []string{"bash"}, "-nbt", "-t", "read")
}

func TestExtensionAndResourceFlags(t *testing.T) {
	api := newRecorder(t, "", nil)
	isolate(t, api.URL)
	dir := t.TempDir()
	put := func(rel, content string) string {
		p := filepath.Join(dir, rel)
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	// project-local discovery, which an untrusted folder must not load
	put(".koine/skills/found/SKILL.md", "---\nname: found\ndescription: discovered skill\n---\nbody")
	put(".koine/prompts/found.md", "found template")
	put("AGENTS.md", "CONTEXT FILE TEXT")
	ext := put("my-ext.mjs", `export default function (pi) {
  pi.registerTool({ name: "from_flag_ext", description: "d", parameters: { type: "object", properties: {} },
    execute: async () => ({ content: [{ type: "text", text: "ran" }] }) });
}`)
	put(".koine/extensions/disc.mjs", `export default function (pi) {
  pi.registerTool({ name: "discovered_tool", description: "d", parameters: { type: "object", properties: {} }, execute: async () => "x" });
}`)
	put("sk/SKILL.md", "---\nname: explicit\ndescription: skill from the command line\n---\nbody")
	put("pt/review.md", "---\ndescription: R\n---\nreview $ARGUMENTS")

	api.Reqs = nil
	code, _, errs := cli("", "-C", dir, "-e", ext, "--skill", filepath.Join(dir, "sk"), "--prompt-template", filepath.Join(dir, "pt"), "hi")
	if code != 0 {
		t.Fatalf("%d %s", code, errs)
	}
	sys := systemText(api.Reqs[0]["system"])
	if !has(api.toolNames(0), "from_flag_ext") {
		t.Errorf("-e extension tool missing: %v", api.toolNames(0))
	}
	if !strings.Contains(sys, "skill from the command line") || !strings.Contains(sys, "CONTEXT FILE TEXT") {
		t.Errorf("system prompt: %s", sys)
	}

	// -ns / -nc / -ne switch discovery off but keep what was named explicitly
	api.Reqs = nil
	code, _, errs = cli("", "-C", dir, "--trust", "-ne", "-ns", "-nc", "-np", "-e", ext, "--skill", filepath.Join(dir, "sk"), "hi")
	if code != 0 {
		t.Fatalf("%d %s", code, errs)
	}
	sys = systemText(api.Reqs[0]["system"])
	if strings.Contains(sys, "discovered skill") || strings.Contains(sys, "CONTEXT FILE TEXT") || !strings.Contains(sys, "skill from the command line") ||
		!has(api.toolNames(0), "from_flag_ext") || has(api.toolNames(0), "discovered_tool") {
		t.Errorf("-ns -nc -ne: tools %v system %s", api.toolNames(0), sys)
	}
	api.Reqs = nil
	if code, _, errs = cli("", "-C", dir, "--trust", "hi"); code != 0 {
		t.Fatalf("%d %s", code, errs)
	}
	if sys = systemText(api.Reqs[0]["system"]); !strings.Contains(sys, "discovered skill") || !strings.Contains(sys, "CONTEXT FILE TEXT") ||
		!has(api.toolNames(0), "discovered_tool") {
		t.Errorf("trusted discovery should find the skill, the context file and the extension: %v %s", api.toolNames(0), sys)
	}
	if has(api.toolNames(0), "from_flag_ext") {
		t.Errorf("an extension nobody named must not load")
	}

	// unusable paths and theme options are warnings, not failures
	if code, _, errs = cli("", "-C", dir, "-e", filepath.Join(dir, "nope.mjs"), "-e", "builtin:mcp", "--skill", filepath.Join(dir, "nope"),
		"--prompt-template", filepath.Join(dir, "nope.md"), "--theme", "t", "hi"); code != 0 {
		t.Fatalf("%d %s", code, errs)
	}
}

func TestExtensionRegisteredFlags(t *testing.T) {
	api := newRecorder(t, "flag_tool", map[string]any{})
	isolate(t, api.URL)
	dir := t.TempDir()
	ext := filepath.Join(dir, "flags.mjs")
	src := `export default function (pi) {
  pi.registerFlag("plan", { description: "Plan first", type: "boolean", default: false });
  pi.registerFlag("label", { description: "A label", type: "string", default: "none" });
  pi.registerTool({ name: "flag_tool", description: "d", parameters: { type: "object", properties: {} },
    execute: async () => ({ content: [{ type: "text", text: JSON.stringify([pi.getFlag("plan"), pi.getFlag("label")]) }] }) });
}`
	if err := os.WriteFile(ext, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	// a string flag takes the next word, a boolean flag does not, and the message is what remains
	code, out, errs := cli("", "-C", dir, "-e", ext, "--plan", "--label", "my label", "go")
	if code != 0 || !strings.Contains(out, `RESULT: [true,"my label"]`) {
		t.Fatalf("%d %q %s", code, out, errs)
	}
	if got := firstUserText(api.Reqs); got != "go" {
		t.Errorf("prompt = %q", got)
	}
	api.Reqs = nil
	if code, out, _ = cli("", "-C", dir, "-e", ext, "--label=x", "go"); code != 0 || !strings.Contains(out, `RESULT: [false,"x"]`) {
		t.Errorf("%d %q", code, out)
	}
	api.Reqs = nil
	if code, out, _ = cli("", "-C", dir, "-e", ext, "go"); code != 0 || !strings.Contains(out, `RESULT: [false,"none"]`) {
		t.Errorf("defaults: %d %q", code, out)
	}
	// an option nobody registered is a usage error, with or without extensions
	if code, _, errs = cli("", "-C", dir, "-e", ext, "--bogus", "go"); code != 2 || !strings.Contains(errs, "unknown option") {
		t.Errorf("unknown long option: %d %q", code, errs)
	}
	if code, _, errs = cli("", "-C", dir, "--bogus", "go"); code != 2 || !strings.Contains(errs, "unknown option") {
		t.Errorf("unknown long option without extensions: %d %q", code, errs)
	}
	if code, _, errs = cli("", "-C", dir, "-e", ext, "go", "--label"); code != 2 || !strings.Contains(errs, "needs a value") {
		t.Errorf("string flag without a value: %d %q", code, errs)
	}
	// --help lists them
	code, out, _ = cli("", "-C", dir, "-e", ext, "--help")
	if code != 0 || !strings.Contains(out, "Extension options:") || !strings.Contains(out, "--label VALUE") || !strings.Contains(out, "(default none)") ||
		!strings.Contains(out, "--plan ") {
		t.Errorf("help: %d %q", code, out)
	}
	if _, out, _ = cli("", "-C", dir, "--help"); strings.Contains(out, "Extension options:") {
		t.Errorf("no extensions, no section: %q", out)
	}
}

func TestModelSelectionOptions(t *testing.T) {
	api := newRecorder(t, "", nil)
	isolate(t, api.URL)
	dir := t.TempDir()
	model := func(args ...string) (int, string, string) {
		api.Reqs = nil
		code, _, errs := cli("", append(append([]string{"-C", dir}, args...), "hi")...)
		got := ""
		if len(api.Reqs) > 0 {
			got, _ = api.Reqs[0]["model"].(string)
		}
		return code, got, errs
	}
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"--model", "sonnet"}, "claude-sonnet-5-5"},
		{[]string{"--model", "Haiku 5.5"}, "claude-haiku-5-5"},
		{[]string{"--model", "anthropic/opus"}, "claude-opus-5-5"},
		{[]string{"--model", "opus", "--provider", "anthropic"}, "claude-opus-5-5"},
		{[]string{"--model", "claude-haiku-5-5"}, "claude-haiku-5-5"},
		{[]string{"--model", "claude-haiku-5-5-20261001"}, "claude-haiku-5-5-20261001"},
		{[]string{"--models", "haiku,opus"}, "claude-haiku-5-5"},
		{[]string{"--models", "*opus*"}, "claude-opus-5-5"},
		{[]string{"--models", "nothing-like-it,claude-opus-5-5"}, "claude-opus-5-5"},
		{[]string{"--models", "haiku", "--model", "opus"}, "claude-opus-5-5"},
		{[]string{"--model", "sonnet:high"}, "claude-sonnet-5-5"},
		{[]string{"--models", "haiku:low,opus"}, "claude-haiku-5-5"},
	} {
		if code, got, errs := model(c.args...); code != 0 || got != c.want {
			t.Errorf("%v: code %d model %q want %q (%s)", c.args, code, got, c.want, errs)
		}
	}
	for _, args := range [][]string{{"--model", "zzzz"}, {"--model", "openai/sonnet", "--provider", "anthropic"}, {"--model", "x", "--provider", "nosuch"}} {
		if code, _, errs := model(args...); code != 1 || errs == "" {
			t.Errorf("%v: want exit 1 with a message, got %d %q", args, code, errs)
		}
	}
	// an id the catalog does not list is sent as written when its provider is known
	if code, got, _ := model("--model", "anthropic/brand-new-model"); code != 0 || got != "brand-new-model" {
		t.Errorf("unlisted id: %d %q", code, got)
	}
}

func TestEnabledModelsSettingPicksTheStartupModel(t *testing.T) {
	api := newRecorder(t, "", nil)
	isolate(t, api.URL)
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".koine"), 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(s string) {
		if err := os.WriteFile(filepath.Join(dir, ".koine", "settings.json"), []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	ran := func(args ...string) string {
		api.Reqs = nil
		if code, _, errs := cli("", append(append([]string{"-C", dir}, args...), "hi")...); code != 0 {
			t.Fatalf("%d %s", code, errs)
		}
		return api.Reqs[0]["model"].(string)
	}
	write(`{"enabledModels": ["haiku", "claude-opus-*"]}`)
	if got := ran(); got != "claude-haiku-5-5" {
		t.Errorf("enabledModels first match: %q", got)
	}
	if got := ran("--models", "opus"); got != "claude-opus-5-5" {
		t.Errorf("--models replaces the setting: %q", got)
	}
	write(`{"enabledModels": ["haiku"], "defaultModel": "claude-opus-5-5"}`)
	if got := ran(); got != "claude-opus-5-5" {
		t.Errorf("a configured default model is kept: %q", got)
	}
	t.Setenv("KOINE_MODEL", "claude-sonnet-5-5")
	if got := ran(); got != "claude-sonnet-5-5" {
		t.Errorf("KOINE_MODEL wins over the scope: %q", got)
	}
}

func TestAPIKeyOverridesTheEnvironmentForOneRun(t *testing.T) {
	api := newRecorder(t, "", nil)
	isolate(t, api.URL)
	t.Setenv("ANTHROPIC_API_KEY", "from-env")
	if code, _, errs := cli("", "-C", t.TempDir(), "--model", "sonnet", "--api-key", "from-flag", "hi"); code != 0 || api.Keys[0] != "from-flag" {
		t.Fatalf("%d %s keys=%v", code, errs, api.Keys)
	}
	if code, _, errs := cli("", "-C", t.TempDir(), "--models", "haiku", "--api-key", "other", "hi"); code != 0 || api.Keys[1] != "other" {
		t.Fatalf("%d %s keys=%v", code, errs, api.Keys)
	}
	t.Setenv("ANTHROPIC_API_KEY", "")
	if code, _, errs := cli("", "-C", t.TempDir(), "--model", "sonnet", "--api-key", "only-flag", "hi"); code != 0 || api.Keys[2] != "only-flag" {
		t.Fatalf("a key from the flag alone should be enough: %d %s keys=%v", code, errs, api.Keys)
	}
	if code, _, errs := cli("", "-C", t.TempDir(), "--model", "sonnet", "hi"); code == 0 {
		t.Errorf("without any key the run must fail: %s", errs)
	}
}

func TestListModels(t *testing.T) {
	isolate(t, "http://127.0.0.1:1")
	code, out, _ := cli("", "--list-models")
	if code != 0 || !strings.HasPrefix(out, "provider") || !strings.Contains(out, "claude-sonnet-5-5") || !strings.Contains(out, "1M") {
		t.Errorf("%d %q", code, out)
	}
	if code, out, _ = cli("", "--list-models", "haiku"); code != 0 || !strings.Contains(out, "claude-haiku-5-5") || strings.Contains(out, "opus") {
		t.Errorf("filtered: %d %q", code, out)
	}
	if code, out, _ = cli("", "--list-models", "no such model"); code != 0 || !strings.Contains(out, "no models match") {
		t.Errorf("none: %d %q", code, out)
	}
}

func TestBashToolSeesSessionVariablesAndChildrenSeeMarkers(t *testing.T) {
	api := newRecorder(t, "bash", map[string]any{"command": "echo id=$KOINE_SESSION_ID model=$KOINE_SESSION_MODEL provider=$KOINE_SESSION_PROVIDER " +
		"file=$KOINE_SESSION_FILE agent=$AI_AGENT marker=$KOINE_CODING_AGENT"})
	isolate(t, api.URL)
	t.Setenv("AI_AGENT", "")
	t.Setenv("KOINE_CODING_AGENT", "")
	app.SetProcessMarkers()
	code, out, errs := cli("", "-C", t.TempDir(), "--model", "haiku", "run it")
	if code != 0 {
		t.Fatalf("%d %s", code, errs)
	}
	for _, want := range []string{"model=claude-haiku-5-5", "provider=anthropic", "agent=koine", "marker=true", "file=" + os.Getenv("HOME")} {
		if !strings.Contains(out, want) {
			t.Errorf("bash output lacks %q: %s", want, out)
		}
	}
	if strings.Contains(out, "id= ") {
		t.Errorf("session id missing: %s", out)
	}
	// with sessions off there is an id but no file
	code, out, _ = cli("", "-C", t.TempDir(), "--no-session", "run it")
	if code != 0 || strings.Contains(out, "id= ") || !strings.Contains(out, "file= agent") {
		t.Errorf("--no-session: %d %s", code, out)
	}
}
