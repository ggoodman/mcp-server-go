package elicitation

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"sync"

	js "github.com/invopop/jsonschema"

	"github.com/ggoodman/mcp-server-go/internal/validation"
	"github.com/ggoodman/mcp-server-go/mcp"
)

// fieldMeta captures per-field decoding expectations.
type fieldMeta struct {
	Name      string
	Index     []int
	Required  bool
	Kind      reflect.Kind // underlying base kind (not pointer)
	EnumSet   map[string]struct{}
	Min, Max  *float64
	IsPointer bool
}

var projCache sync.Map // map[reflect.Type]*cachedProjection (used by forthcoming decode path)

type cachedProjection struct {
	schema mcp.ElicitationSchema
	fields []fieldMeta
}

// --- Projection ---

// project derives an elicitation schema and field metadata for the given type.
func project(t reflect.Type) (*cachedProjection, error) {
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return nil, fmt.Errorf("elicitation: type must be struct kind, got %s", t.Kind())
	}
	if v, ok := projCache.Load(t); ok {
		return v.(*cachedProjection), nil
	}

	// Build mapping from json property name to struct field for metadata.
	fieldMap := map[string]reflect.StructField{}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.PkgPath != "" {
			continue
		} // unexported
		name, _ := parseJSONName(f)
		if name == "-" {
			continue
		}
		if name == "" {
			name = lowerFirstExported(f.Name)
		}
		fieldMap[name] = f
	}

	r := &js.Reflector{DoNotReference: true, ExpandedStruct: true}
	root := r.Reflect(reflect.New(t).Interface())
	if root == nil || root.Type != "object" {
		return nil, fmt.Errorf("elicitation: projected root not object")
	}

	requiredSet := map[string]struct{}{}
	for _, n := range root.Required {
		requiredSet[n] = struct{}{}
	}

	out := mcp.ElicitationSchema{Type: "object", Properties: make(map[string]mcp.PrimitiveSchemaDefinition)}
	var metas []fieldMeta

	if root.Properties != nil {
		for el := root.Properties.Oldest(); el != nil; el = el.Next() {
			name := el.Key
			v := el.Value
			if v == nil {
				return nil, fmt.Errorf("elicitation: nil property schema for %s", name)
			}
			// Reject nested/complex constructs we don't support for elicitation.
			if v.Type == "object" || v.Type == "array" || v.Ref != "" || len(v.AllOf) > 0 || len(v.AnyOf) > 0 || len(v.OneOf) > 0 || v.Not != nil {
				return nil, fmt.Errorf("elicitation: unsupported schema feature on field %s", name)
			}
			if v.Items != nil || v.AdditionalProperties != nil {
				return nil, fmt.Errorf("elicitation: complex collection feature on field %s", name)
			}
			if v.Pattern != "" || v.Format != "" || len(v.Examples) > 0 || v.Default != nil || v.ContentEncoding != "" || v.ContentMediaType != "" {
				return nil, fmt.Errorf("elicitation: unsupported supplemental keyword on field %s", name)
			}

			sf, ok := fieldMap[name]
			if !ok {
				return nil, fmt.Errorf("elicitation: property %s not matched to struct field", name)
			}
			ft := sf.Type
			isPtr := false
			if ft.Kind() == reflect.Pointer {
				isPtr = true
				ft = ft.Elem()
			}
			baseKind := ft.Kind()
			typeStr, ok := mapSchemaTypeToProtocol(v.Type, baseKind)
			if !ok {
				return nil, fmt.Errorf("elicitation: unsupported type mapping for field %s", name)
			}

			ps := mcp.PrimitiveSchemaDefinition{Type: typeStr}
			if v.Description != "" {
				ps.Description = v.Description
			}
			var enumSet map[string]struct{}
			if len(v.Enum) > 0 {
				if typeStr != "string" {
					return nil, fmt.Errorf("elicitation: enum only on string (%s)", name)
				}
				ps.Enum = make([]any, len(v.Enum))
				enumSet = make(map[string]struct{}, len(v.Enum))
				for i, ev := range v.Enum {
					sv, ok := ev.(string)
					if !ok {
						return nil, fmt.Errorf("elicitation: non-string enum value field %s", name)
					}
					ps.Enum[i] = sv
					enumSet[sv] = struct{}{}
				}
			}
			var minPtr, maxPtr *float64
			if v.Minimum != "" {
				if f, err := strconv.ParseFloat(string(v.Minimum), 64); err == nil {
					ps.Minimum = f
					minPtr = &f
				}
			}
			if v.Maximum != "" {
				if f, err := strconv.ParseFloat(string(v.Maximum), 64); err == nil {
					ps.Maximum = f
					maxPtr = &f
				}
			}

			out.Properties[name] = ps
			_, isReq := requiredSet[name]
			if isReq && !isPtr {
				out.Required = append(out.Required, name)
			}
			metas = append(metas, fieldMeta{Name: name, Index: sf.Index, Required: isReq && !isPtr, Kind: baseKind, EnumSet: enumSet, Min: minPtr, Max: maxPtr, IsPointer: isPtr})
		}
	}

	if len(out.Properties) == 0 {
		return nil, fmt.Errorf("elicitation: struct has no exported fields")
	}
	if err := validation.ElicitationSchema(&out); err != nil {
		return nil, err
	}
	cp := &cachedProjection{schema: out, fields: metas}
	actual, _ := projCache.LoadOrStore(t, cp)
	return actual.(*cachedProjection), nil
}

// ProjectForTesting is a temporary exported wrapper to allow the sessions helper to reference
// the projection logic while the full decode path is being implemented. It is not part of the
// public API surface and may be removed without notice.
// ProjectSchema exposes the elicitation schema (no field metadata) for a type.
func ProjectSchema(t reflect.Type) (*mcp.ElicitationSchema, *cachedProjection, error) {
	cp, err := project(t)
	if err != nil {
		return nil, nil, err
	}
	return &cp.schema, cp, nil
}

// --- Decoding ---

// decodeInto populates ptr (pointer to struct of same shape) from a generic map.
// It enforces required fields, enum membership, numeric bounds, and (optionally) strict key set.
func decodeInto(cp *cachedProjection, ptr any, raw any, strict bool) error {
	if ptr == nil {
		return errors.New("elicitation: decode target nil")
	}
	rv := reflect.ValueOf(ptr)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return errors.New("elicitation: decode target must be non-nil pointer")
	}
	rv = rv.Elem()
	if rv.Kind() != reflect.Struct {
		return errors.New("elicitation: decode target must point to struct")
	}

	// Accept either a map[string]any or something JSON-marshalable into that.
	var m map[string]any
	switch src := raw.(type) {
	case map[string]any:
		m = src
	default:
		b, err := json.Marshal(raw)
		if err != nil {
			return fmt.Errorf("elicitation: cannot coerce response: %w", err)
		}
		if err := json.Unmarshal(b, &m); err != nil {
			return fmt.Errorf("elicitation: cannot parse response: %w", err)
		}
	}
	if m == nil {
		return errors.New("elicitation: response content not an object")
	}

	// Build quick lookups.
	fieldByName := make(map[string]fieldMeta, len(cp.fields))
	for _, fm := range cp.fields {
		fieldByName[fm.Name] = fm
	}

	if strict {
		for k := range m {
			if _, ok := fieldByName[k]; !ok {
				return fmt.Errorf("elicitation: unknown field %s in response", k)
			}
		}
	}

	// Required tracking.
	seen := make(map[string]struct{}, len(m))

	for name, val := range m {
		fm, ok := fieldByName[name]
		if !ok {
			continue
		} // lenient ignore if not strict
		seen[name] = struct{}{}
		fv := rv.FieldByIndex(fm.Index)
		// Handle pointer allocation
		target := fv
		if fm.IsPointer {
			if fv.IsNil() {
				fv.Set(reflect.New(fv.Type().Elem()))
			}
			target = fv.Elem()
		}
		if !target.CanSet() {
			return fmt.Errorf("elicitation: cannot set field %s", name)
		}
		// Coerce based on fm.Kind
		if val == nil {
			if fm.Required {
				return fmt.Errorf("elicitation: required field %s is null", name)
			}
			continue
		}
		switch fm.Kind {
		case reflect.String:
			s, ok := val.(string)
			if !ok {
				return fmt.Errorf("elicitation: field %s expected string", name)
			}
			if fm.EnumSet != nil {
				if _, ok := fm.EnumSet[s]; !ok {
					return fmt.Errorf("elicitation: field %s enum mismatch", name)
				}
			}
			target.SetString(s)
		case reflect.Bool:
			b, ok := val.(bool)
			if !ok {
				return fmt.Errorf("elicitation: field %s expected boolean", name)
			}
			target.SetBool(b)
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			f, ok := toFloat(val)
			if !ok {
				return fmt.Errorf("elicitation: field %s expected number", name)
			}
			if err := checkBounds(name, f, fm.Min, fm.Max); err != nil {
				return err
			}
			target.SetInt(int64(f))
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			f, ok := toFloat(val)
			if !ok || f < 0 {
				return fmt.Errorf("elicitation: field %s expected non-negative number", name)
			}
			if err := checkBounds(name, f, fm.Min, fm.Max); err != nil {
				return err
			}
			target.SetUint(uint64(f))
		case reflect.Float32, reflect.Float64:
			f, ok := toFloat(val)
			if !ok {
				return fmt.Errorf("elicitation: field %s expected number", name)
			}
			if err := checkBounds(name, f, fm.Min, fm.Max); err != nil {
				return err
			}
			target.SetFloat(f)
		default:
			return fmt.Errorf("elicitation: unsupported field kind %s", fm.Kind)
		}
	}

	for _, fm := range cp.fields {
		if fm.Required {
			if _, ok := seen[fm.Name]; !ok {
				return fmt.Errorf("elicitation: missing required field %s", fm.Name)
			}
		}
	}
	return nil
}

func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int, int8, int16, int32, int64:
		return float64(reflect.ValueOf(n).Int()), true
	case uint, uint8, uint16, uint32, uint64:
		return float64(reflect.ValueOf(n).Uint()), true
	case json.Number:
		f, err := n.Float64()
		if err == nil {
			return f, true
		}
		return 0, false
	default:
		return 0, false
	}
}

func checkBounds(name string, v float64, minPtr, maxPtr *float64) error {
	if minPtr != nil && v < *minPtr {
		return fmt.Errorf("elicitation: field %s below minimum", name)
	}
	if maxPtr != nil && v > *maxPtr {
		return fmt.Errorf("elicitation: field %s above maximum", name)
	}
	return nil
}

// DecodeForTyped is exported for the sessions typed helper; not part of public surface.
func DecodeForTyped(cp *cachedProjection, target any, raw any, strict bool) error {
	return decodeInto(cp, target, raw, strict)
}

func parseJSONName(f reflect.StructField) (name string, omitEmpty bool) {
	tag := f.Tag.Get("json")
	if tag == "" {
		return "", false
	}
	parts := strings.Split(tag, ",")
	name = parts[0]
	for _, p := range parts[1:] {
		if p == "omitempty" {
			omitEmpty = true
		}
	}
	return
}

func lowerFirstExported(s string) string {
	if s == "" {
		return s
	}
	return strings.ToLower(s[:1]) + s[1:]
}

func mapSchemaTypeToProtocol(vType string, k reflect.Kind) (string, bool) {
	switch vType {
	case "string":
		if k == reflect.String {
			return "string", true
		}
	case "integer", "number":
		if (k >= reflect.Int && k <= reflect.Int64) || (k >= reflect.Uint && k <= reflect.Uint64) || k == reflect.Float32 || k == reflect.Float64 {
			return "number", true
		}
	case "boolean":
		if k == reflect.Bool {
			return "boolean", true
		}
	}
	return "", false
}

// split is a tiny allocation-minded splitter (string.Split analogue for 1-byte sep).
func split(s string, sep byte) []string { // retained for any legacy callers
	if s == "" {
		return nil
	}
	var out []string
	last := 0
	for i := 0; i < len(s); i++ {
		if s[i] == sep {
			out = append(out, s[last:i])
			last = i + 1
		}
	}
	out = append(out, s[last:])
	return out
}
