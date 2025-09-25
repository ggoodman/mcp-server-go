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

// Builder constructs a flat object schema programmatically.
// Usage:
//
//	b := NewBuilder().
//	    String("name", Required(), Description("User name"), MinLength(1)).
//	    EnumString("role", Optional(), Values("admin","user"), Description("Role"))
//	dec := b.MustBind(&dst) // or Build() then Bind(&dst)
//
// NOTE: This intentionally mirrors the reflection subset: flat primitives only.
// Future: nested objects (TODO: nested objects), arrays (TODO: arrays), defaults (TODO: defaults).
// Keeping initial surface minimal aids client implementation cost.
type Builder struct {
	props map[string]*bProperty
	order []string
	built bool
	mu    sync.Mutex
}

type bProperty struct {
	name        string
	ptype       string // string|number|boolean or enum(string)
	required    bool
	description string
	enumVals    []string
	constraints propertyConstraints
}

// NewBuilder returns a new Builder instance.
func NewBuilder() *Builder { return &Builder{props: make(map[string]*bProperty)} }

// String adds a string property.
func (b *Builder) String(name string, opts ...PropOption) *Builder {
	return b.add(name, "string", opts...)
}

// Number adds a number property.
func (b *Builder) Number(name string, opts ...PropOption) *Builder {
	return b.add(name, "number", opts...)
}

// Boolean adds a boolean property.
func (b *Builder) Boolean(name string, opts ...PropOption) *Builder {
	return b.add(name, "boolean", opts...)
}

// EnumString adds an enum-typed string property (enum constraint rather than type=string).
func (b *Builder) EnumString(name string, values []string, opts ...PropOption) *Builder {
	bp := b.ensure(name)
	bp.ptype = "string" // still string type; enum slice present signals enum
	bp.enumVals = append([]string(nil), values...)
	for _, o := range opts {
		if o != nil {
			o(bp)
		}
	}
	return b
}

func (b *Builder) add(name, ptype string, opts ...PropOption) *Builder {
	bp := b.ensure(name)
	bp.ptype = ptype
	for _, o := range opts {
		if o != nil {
			o(bp)
		}
	}
	return b
}

func (b *Builder) ensure(name string) *bProperty {
	if strings.TrimSpace(name) == "" {
		panic("elicitation: empty property name")
	}
	if b.props[name] == nil {
		b.props[name] = &bProperty{name: name}
		b.order = append(b.order, name)
	}
	return b.props[name]
}

// PropOption mutates a property configuration.
type PropOption func(*bProperty)

// Required marks property required (default if not optional and not pointer in reflection path; here explicit).
func Required() PropOption { return func(p *bProperty) { p.required = true } }

// Optional marks property optional.
func Optional() PropOption { return func(p *bProperty) { p.required = false } }

// Description adds human-readable description.
func Description(desc string) PropOption { return func(p *bProperty) { p.description = desc } }

// MinLength sets string minimum length.
func MinLength(n int) PropOption { return func(p *bProperty) { p.constraints.MinLength = &n } }

// MaxLength sets string maximum length.
func MaxLength(n int) PropOption { return func(p *bProperty) { p.constraints.MaxLength = &n } }

// Minimum sets numeric minimum.
func Minimum(f float64) PropOption { return func(p *bProperty) { p.constraints.Minimum = &f } }

// Maximum sets numeric maximum.
func Maximum(f float64) PropOption { return func(p *bProperty) { p.constraints.Maximum = &f } }

// Build finalizes and returns a dynamicSchema without binding to a destination.
func (b *Builder) Build() (Schema, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.built {
		return nil, errors.New("elicitation: builder reused after Build")
	}
	// Empty object schema is legal; nothing special required.
	// Validate, normalize ordering
	names := append([]string(nil), b.order...)
	sort.Strings(names)
	var required []string
	propsObj := make(map[string]any, len(names))
	for _, name := range names {
		p := b.props[name]
		if p.ptype == "" {
			return nil, fmt.Errorf("elicitation: property %s missing type", name)
		}
		m := map[string]any{}
		if len(p.enumVals) > 0 {
			vals := make([]string, len(p.enumVals))
			copy(vals, p.enumVals)
			m["enum"] = vals
		} else {
			m["type"] = p.ptype
		}
		if p.description != "" {
			m["description"] = p.description
		}
		if c := p.constraints; c.MinLength != nil {
			m["minLength"] = *c.MinLength
		}
		if c := p.constraints; c.MaxLength != nil {
			m["maxLength"] = *c.MaxLength
		}
		if c := p.constraints; c.Minimum != nil {
			m["minimum"] = *c.Minimum
		}
		if c := p.constraints; c.Maximum != nil {
			m["maximum"] = *c.Maximum
		}
		propsObj[name] = m
		if p.required {
			required = append(required, name)
		}
	}
	root := map[string]any{"type": "object", "properties": propsObj}
	if len(required) > 0 {
		root["required"] = required
	}
	jsonBytes, _ := json.Marshal(root) // TODO: canonical encoder
	ds := &dynamicSchema{jsonBytes: jsonBytes}
	ds.fingerprint = fingerprintBytes(jsonBytes)
	// Retain metadata for decoding path
	ds.props = b.props
	ds.requiredSet = make(map[string]struct{}, len(required))
	for _, r := range required {
		ds.requiredSet[r] = struct{}{}
	}
	b.built = true
	return ds, nil
}

// MustBuild panics on error.
func (b *Builder) MustBuild() Schema {
	s, err := b.Build()
	if err != nil {
		panic(err)
	}
	return s
}

// Bind returns a SchemaDecoder that decodes into *map[string]any or a struct-like destination.
// Currently only *map[string]any is implemented for dynamic builder binding; extending to typed
// destinations would require either a mapping function or generic adapter (TODO: typed builder decode).
func (b *Builder) Bind(dst any) (SchemaDecoder, error) {
	sch, err := b.Build()
	if err != nil {
		return nil, err
	}
	return bindDynamicSchema(sch.(*dynamicSchema), dst)
}

// MustBind panics on error.
func (b *Builder) MustBind(dst any) SchemaDecoder {
	d, err := b.Bind(dst)
	if err != nil {
		panic(err)
	}
	return d
}

// BindStruct returns a SchemaDecoder that decodes directly into the provided struct pointer.
// Mapping rules:
//   - Property name matches field's json tag (first segment) if present and not '-'; otherwise exact field name match.
//   - Required schema properties must have a compatible field (string/number/boolean) else error.
//   - Optional schema properties without a field are ignored during decode.
//   - Pointer fields are allocated on demand. Required+pointer is allowed and enforced.
//   - Unsupported struct field kinds for mapped properties cause an error at bind time.
//
// Destination must be a non-nil pointer to struct.
func (b *Builder) BindStruct(ptr any) (SchemaDecoder, error) {
	sch, err := b.Build()
	if err != nil {
		return nil, err
	}
	ds := sch.(*dynamicSchema)

	if ptr == nil {
		return nil, errors.New("elicitation: nil destination in BindStruct")
	}
	rv := reflect.ValueOf(ptr)
	if rv.Kind() != reflect.Ptr || rv.IsNil() {
		return nil, errors.New("elicitation: BindStruct expects non-nil pointer to struct")
	}
	rvElem := rv.Elem()
	if rvElem.Kind() != reflect.Struct {
		return nil, errors.New("elicitation: BindStruct expects pointer to struct")
	}

	bindings := make(map[string]fieldBinding)
	// Build index of field name -> field info
	rt := rvElem.Type()
	for i := 0; i < rt.NumField(); i++ {
		f := rt.Field(i)
		if f.PkgPath != "" { // unexported
			continue
		}
		jsonTag := f.Tag.Get("json")
		name := f.Name
		if jsonTag != "" {
			seg := strings.Split(jsonTag, ",")[0]
			if seg == "-" {
				continue
			}
			if seg != "" {
				name = seg
			}
		}
		bindings[name] = fieldBinding{index: i, typ: f.Type, isPtr: f.Type.Kind() == reflect.Ptr}
	}

	propBindings := make(map[string]fieldBinding)
	for pname, prop := range ds.props {
		fb, ok := bindings[pname]
		if !ok {
			if prop.required {
				return nil, fmt.Errorf("elicitation: required property %s has no matching struct field", pname)
			}
			continue // optional unmapped ok
		}
		// Validate compatibility
		ft := fb.typ
		if fb.isPtr {
			ft = ft.Elem()
		}
		kind := ft.Kind()
		if !compatibleKind(prop.ptype, kind) {
			return nil, fmt.Errorf("elicitation: field %s incompatible with schema type %s", pname, prop.ptype)
		}
		propBindings[pname] = fb
	}

	return &dynamicStructDecoder{schema: ds, dst: rv, bindings: propBindings}, nil
}

// MustBindStruct panics on error.
func (b *Builder) MustBindStruct(ptr any) SchemaDecoder {
	d, err := b.BindStruct(ptr)
	if err != nil {
		panic(err)
	}
	return d
}

type fieldBinding struct {
	index int
	typ   reflect.Type
	isPtr bool
}

func compatibleKind(ptype string, kind reflect.Kind) bool {
	switch ptype {
	case "string":
		return kind == reflect.String
	case "number":
		return kind == reflect.Int || kind == reflect.Int8 || kind == reflect.Int16 || kind == reflect.Int32 || kind == reflect.Int64 ||
			kind == reflect.Uint || kind == reflect.Uint8 || kind == reflect.Uint16 || kind == reflect.Uint32 || kind == reflect.Uint64 ||
			kind == reflect.Float32 || kind == reflect.Float64
	case "boolean":
		return kind == reflect.Bool
	default: // enum represented as string internally
		return kind == reflect.String
	}
}

// dynamicStructDecoder decodes into a struct value with precomputed field bindings.
type dynamicStructDecoder struct {
	schema   *dynamicSchema
	dst      reflect.Value // pointer to struct
	bindings map[string]fieldBinding
}

var _ SchemaDecoder = (*dynamicStructDecoder)(nil)

func (d *dynamicStructDecoder) Schema() (Schema, error) { return d.schema, nil }

func (d *dynamicStructDecoder) Decode(raw map[string]any, dst any) error {
	var target reflect.Value
	if dst == nil {
		target = d.dst
	} else {
		v := reflect.ValueOf(dst)
		if v.Kind() != reflect.Ptr || v.IsNil() || v.Elem().Type() != d.dst.Elem().Type() {
			return errors.New("elicitation: typed decode destination mismatch")
		}
		target = v
	}

	// Validate required presence
	missing := make([]string, 0)
	for r := range d.schema.requiredSet {
		if _, ok := raw[r]; !ok {
			missing = append(missing, r)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required fields: %s", strings.Join(missing, ","))
	}

	fresh := reflect.New(target.Elem().Type()).Elem()

	for name, val := range raw {
		prop := d.schema.props[name]
		if prop == nil {
			continue
		}
		fb, mapped := d.bindings[name]
		if !mapped {
			continue
		} // schema prop not mapped to field (optional)
		if err := validateDynamicValue(prop, val); err != nil {
			return fmt.Errorf("field %s: %w", name, err)
		}
		field := fresh.Field(fb.index)
		if fb.isPtr {
			if field.IsNil() {
				field.Set(reflect.New(field.Type().Elem()))
			}
			field = field.Elem()
		}
		assignStructPrimitive(field, val, prop.ptype)
	}

	// Success
	target.Elem().Set(fresh)
	return nil
}

func assignStructPrimitive(dst reflect.Value, val any, ptype string) {
	switch ptype {
	case "string":
		dst.SetString(val.(string))
	case "number":
		f := val.(float64)
		// reuse numeric assignment logic from reflection path (simplified)
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
	case "boolean":
		dst.SetBool(val.(bool))
	default: // treat enums as string
		dst.SetString(val.(string))
	}
}

// dynamicSchema implements Schema for builder-produced schemas.
type dynamicSchema struct {
	jsonBytes   []byte
	fingerprint string
	props       map[string]*bProperty
	// required set for validation
	requiredSet map[string]struct{}
}

func (d *dynamicSchema) MarshalJSON() ([]byte, error) {
	return append([]byte(nil), d.jsonBytes...), nil
}
func (d *dynamicSchema) Fingerprint() string { return d.fingerprint }

// dynamicDecoder implements SchemaDecoder for builder-based schemas.
type dynamicDecoder struct {
	schema *dynamicSchema
	dst    any // *map[string]any currently
}

func bindDynamicSchema(ds *dynamicSchema, dst any) (SchemaDecoder, error) {
	// Accept only *map[string]any for now
	if _, ok := dst.(*map[string]any); !ok {
		return nil, errors.New("elicitation: builder Bind currently only supports *map[string]any")
	}
	return &dynamicDecoder{schema: ds, dst: dst}, nil
}

func (d *dynamicDecoder) Schema() (Schema, error) { return d.schema, nil }

func (d *dynamicDecoder) Decode(raw map[string]any, dst any) error {
	if dst == nil {
		dst = d.dst
	}
	mp, ok := dst.(*map[string]any)
	if !ok || mp == nil {
		return errors.New("elicitation: dynamic decode expects *map[string]any destination")
	}
	// Validate required
	missing := make([]string, 0)
	for r := range d.schema.requiredSet {
		if _, ok := raw[r]; !ok {
			missing = append(missing, r)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required fields: %s", strings.Join(missing, ","))
	}
	// Type + constraint checks
	for name, val := range raw {
		bp := d.schema.props[name]
		if bp == nil {
			continue
		} // unknown keys left to outer strictness
		if err := validateDynamicValue(bp, val); err != nil {
			return fmt.Errorf("field %s: %w", name, err)
		}
	}
	// Shallow copy into destination
	copyMap := make(map[string]any, len(raw))
	for k, v := range raw {
		copyMap[k] = v
	}
	*mp = copyMap
	return nil
}

func validateDynamicValue(bp *bProperty, val any) error {
	if val == nil {
		return errors.New("null value")
	}
	if len(bp.enumVals) > 0 {
		vs, ok := val.(string)
		if !ok {
			return errors.New("enum expects string value")
		}
		for _, ev := range bp.enumVals {
			if ev == vs {
				goto ENUMOK
			}
		}
		return fmt.Errorf("value %q not in enum", vs)
	}
ENUMOK:
	switch bp.ptype {
	case "string":
		vs, ok := val.(string)
		if !ok {
			return typeErr("string", val)
		}
		if c := bp.constraints; c.MinLength != nil && len(vs) < *c.MinLength {
			return fmt.Errorf("minLength violation (%d)", *c.MinLength)
		}
		if c := bp.constraints; c.MaxLength != nil && len(vs) > *c.MaxLength {
			return fmt.Errorf("maxLength violation (%d)", *c.MaxLength)
		}
	case "number":
		_, ok := val.(float64)
		if !ok {
			return typeErr("number", val)
		}
		f := val.(float64)
		if c := bp.constraints; c.Minimum != nil && f < *c.Minimum {
			return fmt.Errorf("minimum violation (%g)", *c.Minimum)
		}
		if c := bp.constraints; c.Maximum != nil && f > *c.Maximum {
			return fmt.Errorf("maximum violation (%g)", *c.Maximum)
		}
	case "boolean":
		if _, ok := val.(bool); !ok {
			return typeErr("boolean", val)
		}
	default:
		return fmt.Errorf("unsupported type %s", bp.ptype)
	}
	return nil
}

func fingerprintBytes(b []byte) string { sum := sha256.Sum256(b); return hex.EncodeToString(sum[:]) }
