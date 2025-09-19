package elicitation

import (
	"errors"

	"github.com/ggoodman/mcp-server-go/mcp"
)

// ObjectPart applies a transformation to an ElicitationSchema under construction.
type ObjectPart interface{ apply(*mcp.ElicitationSchema) }

type partFn func(*mcp.ElicitationSchema)

func (f partFn) apply(s *mcp.ElicitationSchema) { f(s) }

// ObjectSchema builds an object-shaped ElicitationSchema from parts.
func ObjectSchema(parts ...ObjectPart) mcp.ElicitationSchema {
	s := mcp.ElicitationSchema{
		Type:       "object",
		Properties: make(map[string]mcp.PrimitiveSchemaDefinition),
	}
	for _, p := range parts {
		p.apply(&s)
	}
	return s
}

// PropString adds a string property.
func PropString(name, description string, opts ...StringOpt) ObjectPart {
	ps := mcp.PrimitiveSchemaDefinition{Type: "string", Description: description}
	cfg := &stringConfig{}
	for _, o := range opts {
		o(cfg)
	}
	if cfg.enum != nil {
		ps.Enum = make([]any, len(cfg.enum))
		for i, v := range cfg.enum {
			ps.Enum[i] = v
		}
	}
	return partFn(func(s *mcp.ElicitationSchema) { s.Properties[name] = ps })
}

// PropNumber adds a number property.
func PropNumber(name, description string, opts ...NumberOpt) ObjectPart {
	ps := mcp.PrimitiveSchemaDefinition{Type: "number", Description: description}
	cfg := &numberConfig{}
	for _, o := range opts {
		o(cfg)
	}
	if cfg.minimumSet {
		ps.Minimum = cfg.minimum
	}
	if cfg.maximumSet {
		ps.Maximum = cfg.maximum
	}
	return partFn(func(s *mcp.ElicitationSchema) { s.Properties[name] = ps })
}

// PropEnum convenience for a string enum.
func PropEnum(name, description string, values ...string) ObjectPart {
	ps := mcp.PrimitiveSchemaDefinition{Type: "string", Description: description}
	ps.Enum = make([]any, len(values))
	for i, v := range values {
		ps.Enum[i] = v
	}
	return partFn(func(s *mcp.ElicitationSchema) { s.Properties[name] = ps })
}

// Required marks properties required.
func Required(names ...string) ObjectPart {
	return partFn(func(s *mcp.ElicitationSchema) { s.Required = append(s.Required, names...) })
}

// String options

type stringConfig struct{ enum []string }

type StringOpt func(*stringConfig)

func WithEnum(values ...string) StringOpt {
	return func(c *stringConfig) { c.enum = append([]string(nil), values...) }
}

// Number options

type numberConfig struct {
	minimum    float64
	minimumSet bool
	maximum    float64
	maximumSet bool
}

type NumberOpt func(*numberConfig)

func WithMinimum(v float64) NumberOpt {
	return func(c *numberConfig) { c.minimum = v; c.minimumSet = true }
}
func WithMaximum(v float64) NumberOpt {
	return func(c *numberConfig) { c.maximum = v; c.maximumSet = true }
}

// validateObjectSchema validates an object ElicitationSchema produced by ObjectSchema.
//
// NOTE: This mutates the provided schema in-place by de-duplicating the Required
// slice while preserving the order of first occurrence.
func validateObjectSchema(s *mcp.ElicitationSchema) error {
	if s == nil {
		return errors.New("nil schema")
	}
	if s.Type != "object" {
		return errors.New("schema type must be object")
	}
	if len(s.Properties) == 0 {
		return errors.New("object schema requires at least one property")
	}
	// Check required names exist and dedupe required slice while preserving order of first occurrence.
	seenReq := map[string]bool{}
	var dedupReq []string
	for _, name := range s.Required {
		if _, ok := s.Properties[name]; !ok {
			return errors.New("required property missing: " + name)
		}
		if !seenReq[name] {
			seenReq[name] = true
			dedupReq = append(dedupReq, name)
		}
	}
	s.Required = dedupReq
	// Basic per-property checks
	for name, prop := range s.Properties {
		if prop.Type == "" {
			return errors.New("property " + name + " missing type")
		}
		if prop.Minimum != 0 || prop.Maximum != 0 { // bounds sanity when both set
			if prop.Minimum != 0 && prop.Maximum != 0 && prop.Minimum > prop.Maximum {
				return errors.New("property " + name + " minimum greater than maximum")
			}
		}
		// enum uniqueness
		if len(prop.Enum) > 1 {
			uniq := map[any]struct{}{}
			for _, v := range prop.Enum {
				uniq[v] = struct{}{}
			}
			if len(uniq) != len(prop.Enum) {
				return errors.New("duplicate enum values for property " + name)
			}
		}
	}
	return nil
}
