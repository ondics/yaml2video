package v2

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"strings"

	yaml "github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/parser"
)

// Schema validation operates on JSON values so YAML scalar types are not coerced
// by Go struct decoding. The schemas are read from the repository at runtime.
func document(path, schemaName string) (map[string]any, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	parsed, err := parser.ParseBytes(raw, 0)
	if err != nil {
		return nil, fmt.Errorf("%s: YAML: %w", path, err)
	}
	if len(parsed.Docs) != 1 {
		return nil, fmt.Errorf("%s: expected one YAML document, got %d", path, len(parsed.Docs))
	}

	var node any
	if err = yaml.UnmarshalWithOptions(raw, &node, yaml.Strict()); err != nil {
		return nil, fmt.Errorf("%s: YAML: %w", path, err)
	}

	b, err := yaml.YAMLToJSON(raw)
	if err != nil {
		return nil, err
	}

	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var value any
	if err = dec.Decode(&value); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}

	schemaBytes, err := os.ReadFile(filepath.Join(schemaDir(), schemaName))
	if err != nil {
		return nil, fmt.Errorf("schema %s: %w", schemaName, err)
	}

	var schema map[string]any
	if err = json.Unmarshal(schemaBytes, &schema); err != nil {
		return nil, err
	}

	if err = validate(value, schema, schema, "$"); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}

	result, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s: expected an object", path)
	}

	return result, nil
}

// SchemaDir may be set when schemas are deployed separately from the source tree.
// By default, the schemas shipped in docs/ are used.
var SchemaDir string

func schemaDir() string {
	if SchemaDir != "" {
		return SchemaDir
	}
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "docs")
}

func validate(value any, rule, root map[string]any, path string) error {
	fail := func(msg string) error { return fmt.Errorf("%s: %s", path, msg) }
	if ref, ok := rule["$ref"].(string); ok {
		cur := any(root)
		for part := range strings.SplitSeq(strings.TrimPrefix(ref, "#/"), "/") {
			obj, ok := cur.(map[string]any)
			if !ok {
				return fail("invalid schema reference")
			}
			cur = obj[part]
		}
		target, ok := cur.(map[string]any)
		if !ok {
			return fail("invalid schema reference")
		}
		return validate(value, target, root, path)
	}

	if typ, ok := rule["type"].(string); ok {
		valid := false
		switch typ {
		case "object":
			_, valid = value.(map[string]any)
		case "array":
			_, valid = value.([]any)
		case "string":
			_, valid = value.(string)
		case "boolean":
			_, valid = value.(bool)
		case "number":
			if _, ok := value.(json.Number); ok {
				_, valid = number(value)
			}
		case "integer":
			n, ok := value.(json.Number)
			if ok {
				f, e := n.Float64()
				valid = e == nil && !math.IsInf(f, 0) && math.Trunc(f) == f
			}
		}

		if !valid {
			return fail("expected " + typ)
		}
	}

	if c, ok := rule["const"]; ok && !reflect.DeepEqual(normal(value), normal(c)) {
		return fail("invalid constant")
	}

	if enums, ok := rule["enum"].([]any); ok {
		found := false
		for _, e := range enums {
			if reflect.DeepEqual(normal(value), normal(e)) {
				found = true
			}
		}
		if !found {
			return fail("not in enum")
		}
	}

	if s, ok := value.(string); ok {
		if min, ok := rule["minLength"].(float64); ok && len([]rune(s)) < int(min) {
			return fail("string too short")
		}

		if pat, ok := rule["pattern"].(string); ok {
			re, e := regexp.Compile(pat)
			if e != nil {
				return e
			}
			if !re.MatchString(s) {
				return fail("pattern mismatch")
			}
		}
	}

	if n, ok := number(value); ok {
		for _, bound := range []string{"minimum", "exclusiveMinimum", "maximum", "exclusiveMaximum"} {
			if b, ok := rule[bound].(float64); ok {
				switch bound {
				case "minimum":
					if n < b {
						return fail(bound)
					}
				case "exclusiveMinimum":
					if n <= b {
						return fail(bound)
					}
				case "maximum":
					if n > b {
						return fail(bound)
					}
				case "exclusiveMaximum":
					if n >= b {
						return fail(bound)
					}
				}
			}
		}
	}

	if obj, ok := value.(map[string]any); ok {
		if req, ok := rule["required"].([]any); ok {
			for _, r := range req {
				if _, ok := obj[r.(string)]; !ok {
					return fail("missing " + r.(string))
				}
			}
		}

		props, _ := rule["properties"].(map[string]any)
		for k, v := range obj {
			r, ok := props[k].(map[string]any)
			if !ok {
				if rule["additionalProperties"] == false {
					return fail("unknown property " + k)
				}
				continue
			}
			if err := validate(v, r, root, path+"."+k); err != nil {
				return err
			}
		}
	}

	if arr, ok := value.([]any); ok {
		if m, ok := rule["minItems"].(float64); ok && len(arr) < int(m) {
			return fail("too few items")
		}

		if m, ok := rule["maxItems"].(float64); ok && len(arr) > int(m) {
			return fail("too many items")
		}

		if rule["uniqueItems"] == true {
			for i := range arr {
				for j := range i {
					if reflect.DeepEqual(arr[i], arr[j]) {
						return fail("duplicate item")
					}
				}
			}
		}

		if item, ok := rule["items"].(map[string]any); ok {
			for i, v := range arr {
				if err := validate(v, item, root, fmt.Sprintf("%s[%d]", path, i)); err != nil {
					return err
				}
			}
		}
	}

	for _, kind := range []string{"oneOf", "anyOf", "allOf"} {
		if rules, ok := rule[kind].([]any); ok {
			passes := 0
			for _, r := range rules {
				if validate(value, r.(map[string]any), root, path) == nil {
					passes++
				}
			}
			if kind == "allOf" && passes != len(rules) || kind == "oneOf" && passes != 1 || kind == "anyOf" && passes == 0 {
				return fail(kind + " failed")
			}
		}
	}

	if cond, ok := rule["if"].(map[string]any); ok && validate(value, cond, root, path) == nil {
		if then, ok := rule["then"].(map[string]any); ok {
			return validate(value, then, root, path)
		}
	}

	return nil
}

func number(v any) (float64, bool) {
	switch n := v.(type) {
	case json.Number:
		f, e := n.Float64()
		return f, e == nil && !math.IsInf(f, 0)
	case float64:
		return n, !math.IsInf(n, 0) && !math.IsNaN(n)
	}
	return 0, false
}

func normal(v any) any {
	if n, ok := number(v); ok {
		return n
	}
	return v
}
