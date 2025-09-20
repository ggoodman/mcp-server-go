package validation

import (
	"fmt"

	"github.com/ggoodman/mcp-server-go/mcp"
)

// ElicitationSchema validates and normalizes an elicitation schema in-place.
// It de-duplicates Required preserving first-occurrence order.
func ElicitationSchema(s *mcp.ElicitationSchema) error {
	if s == nil {
		return fmt.Errorf("nil schema")
	}
	if s.Type != "object" {
		return fmt.Errorf("elicitation schema type must be object")
	}
	if len(s.Properties) == 0 {
		return fmt.Errorf("elicitation schema requires at least one property")
	}
	seen := map[string]struct{}{}
	var req []string
	for _, name := range s.Required {
		if _, ok := s.Properties[name]; !ok {
			return fmt.Errorf("required property missing: %s", name)
		}
		if _, dup := seen[name]; !dup {
			seen[name] = struct{}{}
			req = append(req, name)
		}
	}
	s.Required = req
	for name, p := range s.Properties {
		if p.Type == "" {
			return fmt.Errorf("property %s missing type", name)
		}
		if p.Minimum != 0 || p.Maximum != 0 {
			if p.Minimum != 0 && p.Maximum != 0 && p.Minimum > p.Maximum {
				return fmt.Errorf("property %s minimum greater than maximum", name)
			}
		}
		if len(p.Enum) > 1 {
			uniq := map[any]struct{}{}
			for _, v := range p.Enum {
				uniq[v] = struct{}{}
			}
			if len(uniq) != len(p.Enum) {
				return fmt.Errorf("duplicate enum values for property %s", name)
			}
		}
	}
	return nil
}
