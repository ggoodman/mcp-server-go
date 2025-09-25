package elicitation

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync"
)

// BindStruct constructs (or reuses a cached) flat-object schema derived from the
// concrete struct type pointed to by ptr and returns a SchemaDecoder bound to
// that destination pointer. Rules:
//   - ptr must be a non-nil pointer to a struct
//   - Only exported fields are considered
//   - json:"-" fields skipped
//   - Field name comes from `json` tag (first segment before a comma) or the lower-camel field name
//   - Pointer fields => optional (omitted from required set)
//   - Non-pointer fields => required
//   - jsonschema tag supports a tiny subset: description=...,enum=a|b|c,minLength=...,maxLength=...,minimum=...,maximum=...
//     (values parsed best-effort; unknown tokens ignored). We keep the subset intentionally small to make
//     client implementations cheap. More complex constructs (nested objects, arrays, refs, composition) are rejected.
//
// Decoding strategy:
//   - Incoming raw map validated for unknown keys only if the caller requested strict mode at the Elicit() layer (not here).
//     (This binder does not know the strict flag; enforcement occurs externally by inspecting Schema properties.)
//   - Type coercion is NOT performed: we expect correctly typed JSON values for each primitive.
//   - On validation failure: returns an error and does not mutate destination struct.
//
// NOTE: Strict enforcement of unknown keys is performed outside (the elicitation session logic) since it depends
// on per-call options; the schema exposes its property set for that purpose.
// Limitations (intentional):
//   - No nested objects / arrays (keeps client UI mechanics simple initially)
//   - No oneOf/anyOf/allOf/discriminator constructs
//   - Enums only supported for string fields
//   - Numeric constraints handled only for simple number fields (not enums)
//
// Future considerations (tracked via TODOs inline):
//   - Support nested objects once we have a clear client rendering contract (TODO: nested objects)
//   - Add canonical JSON encoder to guarantee ordering across Go versions (TODO: canonical encoder)
//   - Add default value support via tag (TODO: defaults)
//   - Allow custom per-field validation hooks (TODO: custom validators)
func BindStruct(ptr any) (SchemaDecoder, error) {
	if ptr == nil {
		return nil, errors.New("elicitation: nil pointer passed to BindStruct")
	}
	v := reflect.ValueOf(ptr)
	if v.Kind() != reflect.Ptr || v.IsNil() {
		return nil, errors.New("elicitation: BindStruct expects non-nil pointer to struct")
	}
	if v.Elem().Kind() != reflect.Struct {
		return nil, errors.New("elicitation: BindStruct expects pointer to struct")
	}

	rt := v.Elem().Type()
	rs := getOrBuildReflectedSchema(rt)
	return &boundReflectionDecoder{schema: rs, dst: v}, nil
}

// reflectedSchema is an immutable compiled schema representation.
// properties map preserves insertion order via a sorted slice for canonical JSON output.
type reflectedSchema struct {
	fingerprint string
	jsonBytes   []byte
	// property ordering kept separately for stable marshal
	properties []reflectedProperty
	// quick lookup for validation / unknown key detection
	propIndex map[string]reflectedProperty
	// required field names
	required []string
}

type reflectedProperty struct {
	Name        string
	Type        string // "string" | "number" | "boolean" | "enum" (enum still has an underlying primitive; simplified here)
	Description string
	EnumValues  []string
	Constraints propertyConstraints
	IsPointer   bool
	FieldIndex  int
}

type propertyConstraints struct {
	MinLength *int
	MaxLength *int
	Minimum   *float64
	Maximum   *float64
}

var (
	refSchemaCache sync.Map // reflect.Type -> *reflectedSchema
)

func getOrBuildReflectedSchema(rt reflect.Type) *reflectedSchema {
	if v, ok := refSchemaCache.Load(rt); ok {
		return v.(*reflectedSchema)
	}
	rs := buildReflectedSchema(rt)
	actual, _ := refSchemaCache.LoadOrStore(rt, rs)
	return actual.(*reflectedSchema)
}

func buildReflectedSchema(rt reflect.Type) *reflectedSchema {
	var props []reflectedProperty
	for i := 0; i < rt.NumField(); i++ {
		f := rt.Field(i)
		if f.PkgPath != "" { // unexported
			continue
		}
		jsonTag := f.Tag.Get("json")
		if jsonTag == "-" {
			continue
		}
		name := f.Name
		if jsonTag != "" {
			name = strings.Split(jsonTag, ",")[0]
			if name == "" { // tag like `json:",omitempty"`
				name = f.Name
			}
		}

		pt := f.Type
		isPtr := pt.Kind() == reflect.Ptr
		if isPtr {
			pt = pt.Elem()
		}

		var pType string
		switch pt.Kind() {
		case reflect.String:
			pType = "string"
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
			reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
			reflect.Float32, reflect.Float64:
			pType = "number"
		case reflect.Bool:
			pType = "boolean"
		default:
			// Reject nested/unsupported kinds for now.
			continue
		}

		desc, enumVals, constraints := parseJSONSchemaTag(f.Tag.Get("jsonschema"))

		prop := reflectedProperty{
			Name:        name,
			Type:        pType,
			Description: desc,
			EnumValues:  enumVals,
			Constraints: constraints,
			IsPointer:   isPtr,
			FieldIndex:  i,
		}
		props = append(props, prop)
	}

	// Sort properties by name for deterministic ordering.
	sort.Slice(props, func(i, j int) bool { return props[i].Name < props[j].Name })

	var required []string
	for _, p := range props {
		if !p.IsPointer { // non-pointer => required
			required = append(required, p.Name)
		}
	}

	propIndex := make(map[string]reflectedProperty, len(props))
	for _, p := range props {
		propIndex[p.Name] = p
	}

	jsonBytes := marshalSchemaJSON(props, required)
	fingerprint := computeFingerprint(jsonBytes)
	return &reflectedSchema{
		fingerprint: fingerprint,
		jsonBytes:   jsonBytes,
		properties:  props,
		propIndex:   propIndex,
		required:    required,
	}
}

func marshalSchemaJSON(props []reflectedProperty, required []string) []byte {
	// Produce minimal object schema reflecting allowed primitives.
	// {"type":"object","properties":{...},"required":[...]} (omit required if empty)
	propsObj := make(map[string]any, len(props))
	for _, p := range props {
		m := map[string]any{}
		if len(p.EnumValues) > 0 {
			m["enum"] = p.EnumValues
		} else {
			m["type"] = p.Type
		}
		if p.Description != "" {
			m["description"] = p.Description
		}
		if c := p.Constraints; c.MinLength != nil {
			m["minLength"] = *c.MinLength
		}
		if c := p.Constraints; c.MaxLength != nil {
			m["maxLength"] = *c.MaxLength
		}
		if c := p.Constraints; c.Minimum != nil {
			m["minimum"] = *c.Minimum
		}
		if c := p.Constraints; c.Maximum != nil {
			m["maximum"] = *c.Maximum
		}
		propsObj[p.Name] = m
	}
	root := map[string]any{
		"type":       "object",
		"properties": propsObj,
	}
	if len(required) > 0 {
		root["required"] = required
	}
	b, _ := json.Marshal(root) // NOTE: map iteration order is randomized across Go versions; for absolute stability implement custom canonical encoder (TODO: canonical encoder).
	return b
}

func computeFingerprint(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// reflectedSchema implements Schema.
func (r *reflectedSchema) MarshalJSON() ([]byte, error) {
	return append([]byte(nil), r.jsonBytes...), nil
}
func (r *reflectedSchema) Fingerprint() string { return r.fingerprint }

// boundReflectionDecoder binds a compiled schema instance to a concrete destination pointer.
type boundReflectionDecoder struct {
	schema *reflectedSchema
	dst    reflect.Value // original pointer value
}

func (b *boundReflectionDecoder) Schema() (Schema, error) { return b.schema, nil }

func (b *boundReflectionDecoder) Decode(raw map[string]any, dst any) error {
	// dst may be provided (overriding the originally bound pointer) or nil to mean use the bound one.
	var target reflect.Value
	if dst == nil {
		target = b.dst
	} else {
		v := reflect.ValueOf(dst)
		if v.Kind() != reflect.Ptr || v.IsNil() || v.Elem().Type() != b.dst.Elem().Type() {
			return errors.New("elicitation: Decode destination type mismatch or nil")
		}
		target = v
	}

	// Work in a fresh value to avoid partial mutation on failure.
	fresh := reflect.New(target.Elem().Type()).Elem()

	// Required tracking.
	requiredMissing := make(map[string]struct{}, len(b.schema.required))
	for _, r := range b.schema.required {
		requiredMissing[r] = struct{}{}
	}

	for k, v := range raw {
		prop, ok := b.schema.propIndex[k]
		if !ok {
			// Unknown key: leave it to outer strictness enforcement. Just ignore here.
			continue
		}
		delete(requiredMissing, k)

		field := fresh.Field(prop.FieldIndex)
		// Unwrap pointer field by allocating if necessary.
		if prop.IsPointer {
			if field.IsNil() {
				field.Set(reflect.New(field.Type().Elem()))
			}
			field = field.Elem()
		}

		if err := assignPrimitive(field, v, prop); err != nil {
			return fmt.Errorf("elicitation: field %s: %w", k, err)
		}
	}

	if len(requiredMissing) > 0 {
		keys := make([]string, 0, len(requiredMissing))
		for k := range requiredMissing {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		return fmt.Errorf("elicitation: missing required fields: %s", strings.Join(keys, ","))
	}

	// Success: copy fresh into target.
	target.Elem().Set(fresh)
	return nil
}

func assignPrimitive(dst reflect.Value, val any, prop reflectedProperty) error {
	if val == nil {
		return errors.New("null provided for primitive field")
	}
	// Enum check first (only strings for now).
	if len(prop.EnumValues) > 0 {
		str, ok := val.(string)
		if !ok {
			return errors.New("enum expects string value")
		}
		for _, ev := range prop.EnumValues {
			if ev == str {
				goto ENUMOK
			}
		}
		return fmt.Errorf("value %q not in enum", str)
	}
ENUMOK:
	switch prop.Type {
	case "string":
		vs, ok := val.(string)
		if !ok {
			return typeErr("string", val)
		}
		if c := prop.Constraints; c.MinLength != nil && len(vs) < *c.MinLength {
			return fmt.Errorf("minLength violation (%d)", *c.MinLength)
		}
		if c := prop.Constraints; c.MaxLength != nil && len(vs) > *c.MaxLength {
			return fmt.Errorf("maxLength violation (%d)", *c.MaxLength)
		}
		dst.SetString(vs)
	case "number":
		// JSON numbers decode as float64 by default.
		f, ok := val.(float64)
		if !ok {
			return typeErr("number", val)
		}
		if c := prop.Constraints; c.Minimum != nil && f < *c.Minimum {
			return fmt.Errorf("minimum violation (%g)", *c.Minimum)
		}
		if c := prop.Constraints; c.Maximum != nil && f > *c.Maximum {
			return fmt.Errorf("maximum violation (%g)", *c.Maximum)
		}
		convertAssignNumber(dst, f)
	case "boolean":
		b, ok := val.(bool)
		if !ok {
			return typeErr("boolean", val)
		}
		dst.SetBool(b)
	default:
		return fmt.Errorf("unsupported primitive type %s", prop.Type)
	}
	return nil
}

func typeErr(expected string, got any) error { return fmt.Errorf("expected %s got %T", expected, got) }

func convertAssignNumber(dst reflect.Value, f float64) {
	// Accept lossy down-casts (users can model with float64 if they care). For integers, round toward zero.
	switch dst.Kind() {
	case reflect.Float32, reflect.Float64:
		dst.SetFloat(f)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		dst.SetInt(int64(f))
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		if f < 0 {
			f = 0
		}
		dst.SetUint(uint64(f))
	}
}

func parseJSONSchemaTag(tag string) (desc string, enumVals []string, c propertyConstraints) {
	if tag == "" {
		return
	}
	parts := strings.Split(tag, ",")
	for _, p := range parts {
		if p == "" {
			continue
		}
		kv := strings.SplitN(p, "=", 2)
		key := kv[0]
		val := ""
		if len(kv) == 2 {
			val = kv[1]
		}
		switch key {
		case "description":
			desc = val
		case "enum":
			if val != "" {
				enumVals = strings.Split(val, "|")
			}
		case "minLength":
			if iv, err := parseInt(val); err == nil {
				c.MinLength = &iv
			}
		case "maxLength":
			if iv, err := parseInt(val); err == nil {
				c.MaxLength = &iv
			}
		case "minimum":
			if fv, err := parseFloat(val); err == nil {
				c.Minimum = &fv
			}
		case "maximum":
			if fv, err := parseFloat(val); err == nil {
				c.Maximum = &fv
			}
		default:
			// ignore unknown tokens for forward compat
		}
	}
	return
}

func parseInt(s string) (int, error) {
	var i int
	_, err := fmt.Sscanf(s, "%d", &i)
	return i, err
}

func parseFloat(s string) (float64, error) {
	var f float64
	_, err := fmt.Sscanf(s, "%f", &f)
	return f, err
}

// Ensure interfaces are implemented.
var _ Schema = (*reflectedSchema)(nil)
var _ SchemaDecoder = (*boundReflectionDecoder)(nil)
