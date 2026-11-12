package tools

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestValidateCoercesLooseValuesTheWayPiDoes(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{
		"n":{"type":"number"},"flag":{"type":"boolean"},"tags":{"type":"array","items":{"type":"number"}},"opt":{"type":"string"},"s":{"type":"string"},
		"nested":{"type":"object","properties":{"k":{"type":"integer"}}},"nums":{"type":"array","items":{"type":"number"}}}}`)
	args := json.RawMessage(`{"n":" 4.5 ","flag":"true","tags":"7","opt":null,"s":"12","nested":{"k":"3"},"nums":["1","2"]}`)
	out, err := Validate(schema, args)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	_ = json.Unmarshal(out, &got)
	if got["n"] != 4.5 || got["flag"] != true || got["s"] != "12" || got["nested"].(map[string]any)["k"] != float64(3) {
		t.Errorf("%s", out)
	}
	if tg := got["tags"].([]any); len(tg) != 1 || tg[0] != float64(7) {
		t.Errorf("a single value becomes a one-item array: %s", out)
	}
	if _, kept := got["opt"]; kept {
		t.Errorf("null in an optional property is dropped: %s", out)
	}
	if nums := got["nums"].([]any); nums[0] != float64(1) {
		t.Errorf("array items are coerced: %s", out)
	}
	// values that cannot be coerced stay as they are, and unchanged arguments come back byte for byte
	same := json.RawMessage(`{"n":1}`)
	if out, err := Validate(schema, same); err != nil || string(out) != string(same) {
		t.Errorf("%s %v", out, err)
	}
	if _, err := Validate(schema, json.RawMessage(`{"n":"abc"}`)); err == nil || !strings.Contains(err.Error(), "- n: expected number, got string") {
		t.Errorf("%v", err)
	}
	if _, err := Validate(schema, json.RawMessage(`{"flag":"maybe"}`)); err == nil {
		t.Error("only true and false coerce")
	}
}

func TestValidateReportsEachProblemOnItsOwnLine(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","required":["path","mode"],"additionalProperties":false,"properties":{
		"path":{"type":"string","minLength":2,"maxLength":4},
		"mode":{"enum":["r","w"]},
		"limit":{"type":"integer","minimum":1,"maximum":5},
		"items":{"type":"array","minItems":1,"maxItems":2,"items":{"type":"string"}},
		"kind":{"const":"fixed"},
		"either":{"anyOf":[{"type":"string"},{"type":"number"}]},
		"one":{"oneOf":[{"type":"boolean"}]},
		"more":{"type":"object","additionalProperties":{"type":"number"}}}}`)
	_, err := Validate(schema, json.RawMessage(`{"path":"x","limit":9,"items":[1,"a","b"],"kind":"other","either":true,"one":"x","stray":1,"more":{"a":"z"}}`))
	if err == nil {
		t.Fatal("expected errors")
	}
	for _, want := range []string{
		"- mode: missing", "- path: must be at least 2 characters", "- limit: must be at most 5", "- items: allows at most 2 items",
		"- items.0: expected string, got integer", "- kind: must be fixed", "- either: does not match any of the allowed shapes",
		"- one: does not match any of the allowed shapes", "- stray: is not a known argument", "- more.a: expected number, got string",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("missing %q in\n%v", want, err)
		}
	}
	for _, c := range []struct{ args, want string }{
		{`{"path":"toolong","mode":"x"}`, "- path: must be at most 4 characters"},
		{`{"path":"ok","mode":"x"}`, "- mode: must be one of r, w"},
		{`{"path":"ok","mode":"r","limit":0}`, "- limit: must be at least 1"},
		{`{"path":"ok","mode":"r","items":[]}`, "- items: needs at least 1 items"},
	} {
		if _, err := Validate(schema, json.RawMessage(c.args)); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v", c.args, err)
		}
	}
	if _, err := Validate(schema, json.RawMessage(`{"path":"ok","mode":"r","either":3,"items":["a"],"kind":"fixed","one":true}`)); err != nil {
		t.Errorf("valid arguments: %v", err)
	}
}

func TestValidateNeverRejectsWhatItDoesNotUnderstand(t *testing.T) {
	for _, schema := range []string{``, `not json`, `[1]`, `{"$ref":"#/x"}`, `{"type":"object","format":"weird"}`} {
		if _, err := Validate(json.RawMessage(schema), json.RawMessage(`{"a":1}`)); err != nil {
			t.Errorf("schema %q: %v", schema, err)
		}
	}
	if out, err := Validate(json.RawMessage(`{"type":"object"}`), nil); err != nil || out != nil {
		t.Errorf("no arguments: %s %v", out, err)
	}
	if _, err := Validate(json.RawMessage(`{"type":"object"}`), json.RawMessage(`{`)); err == nil || !strings.Contains(err.Error(), "not valid JSON") {
		t.Errorf("%v", err)
	}
	if _, err := Validate(json.RawMessage(`{"type":["string","null"]}`), json.RawMessage(`3`)); err == nil {
		t.Error("a list of types")
	}
	if _, err := Validate(json.RawMessage(`{"type":"object"}`), json.RawMessage(`[1]`)); err == nil || !strings.Contains(err.Error(), "(arguments)") {
		t.Errorf("%v", err)
	}
	kinds := map[string]string{`null`: "null", `true`: "boolean", `1.5`: "number", `2`: "integer", `"s"`: "string", `[1]`: "array", `{}`: "object"}
	for v, want := range kinds {
		var x any
		_ = json.Unmarshal([]byte(v), &x)
		if typeOf(x) != want {
			t.Errorf("typeOf(%s) = %s", v, typeOf(x))
		}
	}
	if typeOf(struct{}{}) != "unknown" {
		t.Error("typeOf unknown")
	}
}
