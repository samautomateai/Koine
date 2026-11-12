package tools

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

// Validate checks the arguments of a tool call against the tool's JSON Schema, after coercing loose values the way Pi does
// (extensions.md, "Tool parameter schemas"): a numeric string becomes a number, "true" and "false" become booleans, a single value
// where an array is expected becomes a one-item array, and a null in an optional property is dropped. It returns the (possibly
// coerced) arguments, or an error that lists each problem on a line of its own, such as "- path: missing" or
// "- limit: expected number, got string".
//
// Only the common keywords are checked: type, required, properties, items, enum, const, minimum, maximum, minLength, maxLength,
// minItems, maxItems, anyOf, oneOf and additionalProperties. Anything else (a $ref, a format) is not checked, so a schema this code
// does not understand never rejects a call.
func Validate(schema, args json.RawMessage) (json.RawMessage, error) {
	var s any
	if len(schema) == 0 || json.Unmarshal(schema, &s) != nil {
		return args, nil
	}
	sm, ok := s.(map[string]any)
	if !ok {
		return args, nil
	}
	var v any = map[string]any{}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &v); err != nil {
			return args, fmt.Errorf("the arguments are not valid JSON: %w", err)
		}
	}
	changed := false
	v = coerce(sm, v, &changed)
	if errs := check(sm, v, ""); len(errs) > 0 {
		return args, fmt.Errorf("%s", strings.Join(errs, "\n"))
	}
	if !changed {
		return args, nil
	}
	out, err := json.Marshal(v)
	if err != nil {
		return args, nil
	}
	return out, nil
}

func typeOf(v any) string {
	switch x := v.(type) {
	case nil:
		return "null"
	case bool:
		return "boolean"
	case float64:
		if x == math.Trunc(x) {
			return "integer"
		}
		return "number"
	case string:
		return "string"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	}
	return "unknown"
}

// schemaTypes lists the types a schema allows ("type" may be one name or a list).
func schemaTypes(s map[string]any) []string {
	switch t := s["type"].(type) {
	case string:
		return []string{t}
	case []any:
		var out []string
		for _, x := range t {
			if n, ok := x.(string); ok {
				out = append(out, n)
			}
		}
		return out
	}
	return nil
}

func allows(types []string, t string) bool {
	for _, x := range types {
		if x == t || (x == "number" && t == "integer") {
			return true
		}
	}
	return false
}

func coerce(s map[string]any, v any, changed *bool) any {
	types := schemaTypes(s)
	switch x := v.(type) {
	case string:
		switch {
		case (allows(types, "number") || allows(types, "integer")) && !allows(types, "string"):
			if f, err := strconv.ParseFloat(strings.TrimSpace(x), 64); err == nil && strings.TrimSpace(x) != "" && !math.IsInf(f, 0) && !math.IsNaN(f) {
				*changed = true
				return f
			}
		case allows(types, "boolean") && !allows(types, "string"):
			if x == "true" || x == "false" {
				*changed = true
				return x == "true"
			}
		}
	case map[string]any:
		props, _ := s["properties"].(map[string]any)
		req := requiredSet(s)
		for k, pv := range x {
			if pv == nil && !req[k] && !allows(schemaTypes(propSchema(props, k)), "null") {
				delete(x, k) // null in an optional property means "not given"
				*changed = true
				continue
			}
			if ps := propSchema(props, k); ps != nil {
				x[k] = coerce(ps, pv, changed)
			}
		}
	case []any:
		if items, ok := s["items"].(map[string]any); ok {
			for i := range x {
				x[i] = coerce(items, x[i], changed)
			}
		}
	}
	if v != nil && allows(types, "array") && !allows(types, typeOf(v)) && typeOf(v) != "array" && len(types) == 1 {
		*changed = true
		one := []any{v}
		if items, ok := s["items"].(map[string]any); ok {
			one[0] = coerce(items, v, changed)
		}
		return one
	}
	return v
}

func propSchema(props map[string]any, k string) map[string]any {
	m, _ := props[k].(map[string]any)
	return m
}

func requiredSet(s map[string]any) map[string]bool {
	out := map[string]bool{}
	if r, ok := s["required"].([]any); ok {
		for _, x := range r {
			if n, ok := x.(string); ok {
				out[n] = true
			}
		}
	}
	return out
}

func join(path, k string) string {
	if path == "" {
		return k
	}
	return path + "." + k
}

func label(path string) string {
	if path == "" {
		return "(arguments)"
	}
	return path
}

func check(s map[string]any, v any, path string) []string {
	if alts, ok := s["anyOf"].([]any); ok && !anyMatches(alts, v, path) {
		return []string{"- " + label(path) + ": does not match any of the allowed shapes"}
	}
	if alts, ok := s["oneOf"].([]any); ok && !anyMatches(alts, v, path) {
		return []string{"- " + label(path) + ": does not match any of the allowed shapes"}
	}
	var errs []string
	if types := schemaTypes(s); len(types) > 0 && !allows(types, typeOf(v)) {
		return []string{fmt.Sprintf("- %s: expected %s, got %s", label(path), strings.Join(types, " or "), typeOf(v))}
	}
	if enum, ok := s["enum"].([]any); ok && !contains(enum, v) {
		errs = append(errs, fmt.Sprintf("- %s: must be one of %s", label(path), listOf(enum)))
	}
	if c, ok := s["const"]; ok && fmt.Sprint(c) != fmt.Sprint(v) {
		errs = append(errs, fmt.Sprintf("- %s: must be %v", label(path), c))
	}
	switch x := v.(type) {
	case float64:
		if m, ok := s["minimum"].(float64); ok && x < m {
			errs = append(errs, fmt.Sprintf("- %s: must be at least %v", label(path), m))
		}
		if m, ok := s["maximum"].(float64); ok && x > m {
			errs = append(errs, fmt.Sprintf("- %s: must be at most %v", label(path), m))
		}
	case string:
		if m, ok := s["minLength"].(float64); ok && float64(len([]rune(x))) < m {
			errs = append(errs, fmt.Sprintf("- %s: must be at least %v characters", label(path), m))
		}
		if m, ok := s["maxLength"].(float64); ok && float64(len([]rune(x))) > m {
			errs = append(errs, fmt.Sprintf("- %s: must be at most %v characters", label(path), m))
		}
	case []any:
		if m, ok := s["minItems"].(float64); ok && float64(len(x)) < m {
			errs = append(errs, fmt.Sprintf("- %s: needs at least %v items", label(path), m))
		}
		if m, ok := s["maxItems"].(float64); ok && float64(len(x)) > m {
			errs = append(errs, fmt.Sprintf("- %s: allows at most %v items", label(path), m))
		}
		if items, ok := s["items"].(map[string]any); ok {
			for i, e := range x {
				errs = append(errs, check(items, e, join(path, strconv.Itoa(i)))...)
			}
		}
	case map[string]any:
		errs = append(errs, checkObject(s, x, path)...)
	}
	return errs
}

func checkObject(s map[string]any, x map[string]any, path string) []string {
	var errs []string
	props, _ := s["properties"].(map[string]any)
	if r, ok := s["required"].([]any); ok {
		for _, n := range r {
			if name, ok := n.(string); ok {
				if _, there := x[name]; !there {
					errs = append(errs, "- "+join(path, name)+": missing")
				}
			}
		}
	}
	keys := make([]string, 0, len(x))
	for k := range x {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if ps := propSchema(props, k); ps != nil {
			errs = append(errs, check(ps, x[k], join(path, k))...)
		} else if ap, ok := s["additionalProperties"]; ok {
			switch a := ap.(type) {
			case bool:
				if !a {
					errs = append(errs, "- "+join(path, k)+": is not a known argument")
				}
			case map[string]any:
				errs = append(errs, check(a, x[k], join(path, k))...)
			}
		}
	}
	return errs
}

func anyMatches(alts []any, v any, path string) bool {
	for _, a := range alts {
		if am, ok := a.(map[string]any); ok && len(check(am, v, path)) == 0 {
			return true
		}
	}
	return len(alts) == 0
}

func contains(list []any, v any) bool {
	for _, x := range list {
		if fmt.Sprint(x) == fmt.Sprint(v) {
			return true
		}
	}
	return false
}

func listOf(list []any) string {
	parts := make([]string, len(list))
	for i, x := range list {
		parts[i] = fmt.Sprint(x)
	}
	return strings.Join(parts, ", ")
}
