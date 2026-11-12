package pkgs

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestEntryForms(t *testing.T) {
	var es []Entry
	doc := `["npm:a", {"source":"npm:b","extensions":["x/*.ts","!x/old.ts"],"skills":[],"autoload":false}, {"source":"./c"}]`
	if err := json.Unmarshal([]byte(doc), &es); err != nil {
		t.Fatal(err)
	}
	if es[0].Source != "npm:a" || es[0].Filtered() {
		t.Errorf("string form: %+v", es[0])
	}
	b := es[1]
	if !reflect.DeepEqual(b.Extensions, []string{"x/*.ts", "!x/old.ts"}) || b.Skills == nil || len(b.Skills) != 0 || b.Prompts != nil || *b.Autoload {
		t.Errorf("object form: %+v", b)
	}
	if es[2].Filtered() {
		t.Errorf("an object with only a source is a plain entry: %+v", es[2])
	}
	out, _ := json.Marshal(es)
	if string(out) != `["npm:a",{"source":"npm:b","extensions":["x/*.ts","!x/old.ts"],"skills":[],"autoload":false},"./c"]` {
		t.Errorf("round trip: %s", out)
	}
	for _, bad := range []string{`[1]`, `[{"extensions":[]}]`, `[{"source":"npm:a","skills":"x"}]`} {
		var x []Entry
		if json.Unmarshal([]byte(bad), &x) == nil {
			t.Errorf("%s should be refused", bad)
		}
	}
}

func TestDirsPerScope(t *testing.T) {
	t.Setenv(agentdirEnv, "")
	u, p := DirsFor(User, "/home/u", "/proj"), DirsFor(Project, "/home/u", "/proj")
	if u.Settings != "/home/u/.koine/settings.json" || u.Root != "/home/u/.koine/packages" || u.Lock != "/home/u/.koine/packages.lock.json" {
		t.Errorf("user: %+v", u)
	}
	if p.Settings != "/proj/.koine/settings.json" || p.Root != "/proj/.koine/packages" || p.BaseDir() != "/proj/.koine" {
		t.Errorf("project: %+v", p)
	}
	t.Setenv(agentdirEnv, "/elsewhere")
	if DirsFor(User, "/home/u", "/proj").Root != "/elsewhere/packages" {
		t.Error("the agent directory override is ignored")
	}
}

func TestUpdateEntriesKeepsTheRestOfTheFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "sub", "settings.json")
	if err := UpdateEntries(p, func(es []Entry) []Entry { return append(es, Entry{Source: "npm:a"}) }); err != nil {
		t.Fatal(err) // creates the file and its directory
	}
	if err := os.WriteFile(p, []byte(`{"zeta": 1, "defaultModel": "m", "packages": ["npm:a"], "compaction": {"enabled": false}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	_ = os.Chmod(p, 0o600)
	if err := UpdateEntries(p, func(es []Entry) []Entry { return append(es, Entry{Source: "git:h.example/x/y", Skills: []string{}}) }); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	got := string(b)
	order := []string{`"zeta": 1`, `"defaultModel": "m"`, `"packages"`, `"compaction"`}
	last := -1
	for _, k := range order {
		i := strings.Index(got, k)
		if i < 0 || i < last {
			t.Fatalf("key %s lost or reordered:\n%s", k, got)
		}
		last = i
	}
	if st, _ := os.Stat(p); st.Mode().Perm() != 0o600 {
		t.Errorf("mode changed to %v", st.Mode())
	}
	es, _, err := ReadEntries(p)
	if err != nil || len(es) != 2 || es[1].Skills == nil {
		t.Errorf("read back: %+v %v", es, err)
	}
	// removing the last entry removes the key but keeps the others
	if err := UpdateEntries(p, func([]Entry) []Entry { return nil }); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(p)
	if strings.Contains(string(b), "packages") || !strings.Contains(string(b), "compaction") {
		t.Errorf("after clearing:\n%s", b)
	}
}

func TestUpdateEntriesRefusesWhatItCouldDamage(t *testing.T) {
	dir := t.TempDir()
	withComment := filepath.Join(dir, "c.json")
	_ = os.WriteFile(withComment, []byte("{\n // keep me\n \"packages\": [\"npm:a\"]\n}"), 0o644)
	if err := UpdateEntries(withComment, func(es []Entry) []Entry { return es }); err == nil || !strings.Contains(err.Error(), "comments") {
		t.Errorf("a file with comments must not be rewritten: %v", err)
	}
	if es, _, err := ReadEntries(withComment); err != nil || len(es) != 1 {
		t.Errorf("but it can be read: %v %v", es, err)
	}
	bad := filepath.Join(dir, "b.json")
	_ = os.WriteFile(bad, []byte(`{"packages": "npm:a"}`), 0o644)
	if err := UpdateEntries(bad, func(es []Entry) []Entry { return es }); err == nil {
		t.Error("packages that is not an array")
	}
	if _, _, err := ReadEntries(bad); err == nil {
		t.Error("read: packages that is not an array")
	}
	notObj := filepath.Join(dir, "n.json")
	_ = os.WriteFile(notObj, []byte(`[1]`), 0o644)
	if _, _, err := ReadEntries(notObj); err == nil {
		t.Error("settings that are not an object")
	}
	// one bad entry is skipped and reported, the rest is used
	mixed := filepath.Join(dir, "m.json")
	_ = os.WriteFile(mixed, []byte(`{"packages": ["npm:a", 5, "npm:b"]}`), 0o644)
	es, problems, err := ReadEntries(mixed)
	if err != nil || len(es) != 2 || len(problems) != 1 {
		t.Errorf("mixed: %v %v %v", es, problems, err)
	}
	if es, _, err := ReadEntries(filepath.Join(dir, "missing.json")); err != nil || es != nil {
		t.Errorf("missing file: %v %v", es, err)
	}
}

func TestPrefsComeFromTheUserFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv(agentdirEnv, "")
	if p := ReadPrefs(home); p.AllowScripts || p.NpmCommand != nil || p.Collapse {
		t.Errorf("defaults: %+v", p)
	}
	_ = os.MkdirAll(filepath.Join(home, ".koine"), 0o755)
	_ = os.WriteFile(filepath.Join(home, ".koine", "settings.json"),
		[]byte(`{"npmCommand": ["pnpm", "--silent"], "allowInstallScripts": true, "collapseChangelog": true}`), 0o644)
	p := ReadPrefs(home)
	if !p.AllowScripts || !p.Collapse || !reflect.DeepEqual(p.NpmCommand, []string{"pnpm", "--silent"}) {
		t.Errorf("prefs: %+v", p)
	}
	_ = os.WriteFile(filepath.Join(home, ".koine", "settings.json"), []byte(`not json`), 0o644)
	if p := ReadPrefs(home); p.AllowScripts {
		t.Error("a broken file gives the defaults")
	}
}
