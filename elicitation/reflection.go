package elicitation

import (
	"bytes"
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
//   - jsonschema tag supports: title=...,description=...,format=email|uri|date|date-time,enum=a|b|c,enumNames=DisplayA|DisplayB,
//     minLength=...,maxLength=...,minimum=...,maximum=...,default=true|false (boolean fields only).
//     (values parsed best-effort; unknown tokens ignored; enumNames length must match enum). We keep the subset intentionally small to make
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
	Title       string
	EnumValues  []string
	EnumNames   []string
	Constraints propertyConstraints
	IsPointer   bool
	FieldIndex  int
	// Only for strings: format constraint (email|uri|date|date-time)
	Format string
	// Boolean default (only captured if field is boolean and tag supplies default=true/false)
	BoolDefault   *bool
	StringDefault *string  // optional string default
	NumberDefault *float64 // optional number/integer default
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
			reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			pType = "integer"
		case reflect.Float32, reflect.Float64:
			pType = "number"
		case reflect.Bool:
			pType = "boolean"
		default:
			// Reject nested/unsupported kinds for now.
			continue
		}

		title, desc, enumVals, enumNames, format, defaultLiteral, constraints := parseJSONSchemaTagExtended(f.Tag.Get("jsonschema"))

		// If enumNames length mismatch, ignore them (enforce parity)
		if len(enumNames) > 0 && len(enumNames) != len(enumVals) {
			enumNames = nil
		}

		prop := reflectedProperty{
			Name:        name,
			Type:        pType,
			Description: desc,
			Title:       title,
			EnumValues:  enumVals,
			EnumNames:   enumNames,
			Constraints: constraints,
			IsPointer:   isPtr,
			FieldIndex:  i,
			Format:      format,
		}
		// Interpret default literal now that we know type and pointer-ness
		if defaultLiteral != nil {
			// Allow boolean defaults on required (non-pointer) fields for backward compatibility
			// but forbid defaults on other required primitive types.
			if !isPtr && pType != "boolean" {
				panic(fmt.Sprintf("elicitation: default specified on required %s field %s", pType, f.Name))
			}
			switch pType {
			case "boolean":
				if *defaultLiteral == "true" || *defaultLiteral == "false" {
					b := *defaultLiteral == "true"
					prop.BoolDefault = &b
				} else {
					panic(fmt.Sprintf("elicitation: invalid boolean default %q for field %s", *defaultLiteral, f.Name))
				}
			case "string":
				if len(enumVals) > 0 {
					found := false
					for _, ev := range enumVals {
						if ev == *defaultLiteral {
							found = true
							break
						}
					}
					if !found {
						panic(fmt.Sprintf("elicitation: default %q not in enum for field %s", *defaultLiteral, f.Name))
					}
				}
				if constraints.MinLength != nil && len(*defaultLiteral) < *constraints.MinLength {
					panic(fmt.Sprintf("elicitation: default violates minLength for field %s", f.Name))
				}
				if constraints.MaxLength != nil && len(*defaultLiteral) > *constraints.MaxLength {
					panic(fmt.Sprintf("elicitation: default violates maxLength for field %s", f.Name))
				}
				prop.StringDefault = defaultLiteral
			case "number", "integer":
				var nf float64
				if _, err := fmt.Sscanf(*defaultLiteral, "%f", &nf); err != nil {
					panic(fmt.Sprintf("elicitation: invalid number default %q for field %s", *defaultLiteral, f.Name))
				}
				if pType == "integer" && nf != float64(int64(nf)) {
					panic(fmt.Sprintf("elicitation: non-integer default %q for integer field %s", *defaultLiteral, f.Name))
				}
				if constraints.Minimum != nil && nf < *constraints.Minimum {
					panic(fmt.Sprintf("elicitation: default below minimum for field %s", f.Name))
				}
				if constraints.Maximum != nil && nf > *constraints.Maximum {
					panic(fmt.Sprintf("elicitation: default above maximum for field %s", f.Name))
				}
				prop.NumberDefault = &nf
			}
		}
		// format only meaningful for strings; drop otherwise
		if format != "" && pType != "string" {
			prop.Format = ""
		}
		props = append(props, prop)
	}

	// Preserve declaration order (struct field order) rather than sorting.
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

// marshalSchemaJSON produces a canonical JSON representation with deterministic ordering.
// Ordering rules:
//   - Root object keys in order: type, properties, required (required omitted if empty)
//   - Properties retain insertion order from struct definition
//   - Within each property object keys appear in fixed order: type, title, description, format, enum, enumNames, default, minLength, maxLength, minimum, maximum
func marshalSchemaJSON(props []reflectedProperty, required []string) []byte {
	var buf bytes.Buffer
	buf.WriteByte('{')
	// type
	buf.WriteString("\"type\":\"object\",")
	// properties
	buf.WriteString("\"properties\":{")
	for i, p := range props {
		if i > 0 {
			buf.WriteByte(',')
		}
		encJSONString(&buf, p.Name)
		buf.WriteByte(':')
		buf.WriteByte('{')
		// fixed key order
		// type (always)
		buf.WriteString("\"type\":")
		encJSONString(&buf, p.Type)
		// title
		if p.Title != "" {
			buf.WriteString(",\"title\":")
			encJSONString(&buf, p.Title)
		}
		// description
		if p.Description != "" {
			buf.WriteString(",\"description\":")
			encJSONString(&buf, p.Description)
		}
		// format
		if p.Format != "" {
			buf.WriteString(",\"format\":")
			encJSONString(&buf, p.Format)
		}
		// enum
		if len(p.EnumValues) > 0 {
			buf.WriteString(",\"enum\":")
			writeStringArray(&buf, p.EnumValues)
			if len(p.EnumNames) > 0 {
				buf.WriteString(",\"enumNames\":")
				writeStringArray(&buf, p.EnumNames)
			}
		}
		if p.BoolDefault != nil {
			buf.WriteString(",\"default\":")
			if *p.BoolDefault {
				buf.WriteString("true")
			} else {
				buf.WriteString("false")
			}
		}
		if p.StringDefault != nil {
			buf.WriteString(",\"default\":")
			encJSONString(&buf, *p.StringDefault)
		}
		if p.NumberDefault != nil {
			buf.WriteString(",\"default\":")
			buf.WriteString(floatToString(*p.NumberDefault))
		}
		if c := p.Constraints; c.MinLength != nil {
			buf.WriteString(",\"minLength\":")
			buf.WriteString(intToString(*c.MinLength))
		}
		if c := p.Constraints; c.MaxLength != nil {
			buf.WriteString(",\"maxLength\":")
			buf.WriteString(intToString(*c.MaxLength))
		}
		if c := p.Constraints; c.Minimum != nil {
			buf.WriteString(",\"minimum\":")
			buf.WriteString(floatToString(*c.Minimum))
		}
		if c := p.Constraints; c.Maximum != nil {
			buf.WriteString(",\"maximum\":")
			buf.WriteString(floatToString(*c.Maximum))
		}
		buf.WriteByte('}')
	}
	buf.WriteByte('}')
	if len(required) > 0 {
		buf.WriteString(",\"required\":")
		// required array order matches declaration order
		buf.WriteByte('[')
		for i, r := range required {
			if i > 0 {
				buf.WriteByte(',')
			}
			encJSONString(&buf, r)
		}
		buf.WriteByte(']')
	}
	buf.WriteByte('}')
	return buf.Bytes()
}

func encJSONString(buf *bytes.Buffer, s string) {
	b, _ := json.Marshal(s) // uses encoding/json string escaper
	buf.Write(b)
}

func writeStringArray(buf *bytes.Buffer, arr []string) {
	buf.WriteByte('[')
	for i, v := range arr {
		if i > 0 {
			buf.WriteByte(',')
		}
		encJSONString(buf, v)
	}
	buf.WriteByte(']')
}

func intToString(i int) string       { return fmt.Sprintf("%d", i) }
func floatToString(f float64) string { return trimFloat(fmt.Sprintf("%g", f)) }
func trimFloat(s string) string      { return s }

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

	provided := make(map[string]struct{}, len(raw))
	for k, v := range raw {
		prop, ok := b.schema.propIndex[k]
		if !ok {
			// Unknown key: leave it to outer strictness enforcement. Just ignore here.
			continue
		}
		delete(requiredMissing, k)
		provided[k] = struct{}{}

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

	// Apply boolean defaults for optional (pointer) fields not provided.
	for _, p := range b.schema.properties {
		if _, ok := provided[p.Name]; ok {
			continue
		}
		if !p.IsPointer {
			continue
		}
		var hasDefault bool
		var dv any
		if p.BoolDefault != nil {
			hasDefault = true
			dv = *p.BoolDefault
		}
		if p.StringDefault != nil {
			hasDefault = true
			dv = *p.StringDefault
		}
		if p.NumberDefault != nil {
			hasDefault = true
			dv = *p.NumberDefault
		}
		if !hasDefault {
			continue
		}
		field := fresh.Field(p.FieldIndex)
		if field.IsNil() {
			field.Set(reflect.New(field.Type().Elem()))
		}
		fe := field.Elem()
		switch p.Type {
		case "boolean":
			fe.SetBool(dv.(bool))
		case "string":
			fe.SetString(dv.(string))
		case "number", "integer":
			f := dv.(float64)
			convertAssignNumber(fe, f)
		}
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
	case "number", "integer":
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

// parseJSONSchemaTagExtended returns core metadata plus the raw default literal (if any).
func parseJSONSchemaTagExtended(tag string) (title string, desc string, enumVals []string, enumNames []string, format string, defaultLiteral *string, c propertyConstraints) {
	if tag == "" {
		return
	}
	parts := strings.Split(tag, ",")
	seenDefault := false
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
		case "title":
			title = val
		case "description":
			desc = val
		case "enum":
			if val != "" {
				enumVals = strings.Split(val, "|")
			}
		case "enumNames":
			if val != "" {
				enumNames = strings.Split(val, "|")
			}
		case "format":
			switch val {
			case "email", "uri", "date", "date-time":
				format = val
			}
		case "default":
			if seenDefault {
				panic("elicitation: multiple default values specified in tag")
			}
			seenDefault = true
			v := val
			defaultLiteral = &v
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
			// ignore unknown
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
