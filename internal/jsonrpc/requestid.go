package jsonrpc

import (
	"encoding/json"
	"fmt"
)

// RequestID represents a JSON-RPC ID that can be either a string or a number
type RequestID struct {
	value interface{}
}

// NewRequestID creates a new JSONRPCId from a string or number
func NewRequestID(value interface{}) *RequestID {
	switch v := value.(type) {
	case string, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64:
		return &RequestID{value: v}
	default:
		return &RequestID{value: nil}
	}
}

// String returns the string representation of the ID
func (id *RequestID) String() string {
	if id == nil {
		return ""
	}
	if id.value == nil {
		return ""
	}

	switch v := id.value.(type) {
	case string:
		return v
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64:
		return fmt.Sprintf("%v", v)
	default:
		panic("unreachable: JSONRPCId contains unsupported type")
	}
}

// Value returns the underlying value
func (id *RequestID) Value() interface{} {
	return id.value
}

// IsNil returns true if the ID is nil/empty
func (id *RequestID) IsNil() bool {
	if id == nil {
		return true
	}

	return id.value == nil
}

// MarshalJSON implements json.Marshaler
func (id *RequestID) MarshalJSON() ([]byte, error) {
	if id == nil || id.value == nil {
		return []byte{}, nil
	}
	return json.Marshal(id.value)
}

// UnmarshalJSON implements json.Unmarshaler
func (id *RequestID) UnmarshalJSON(data []byte) error {
	// Try to unmarshal as a number first
	var num float64
	if err := json.Unmarshal(data, &num); err == nil {
		// Check if it's an integer
		if num == float64(int64(num)) {
			id.value = int64(num)
		} else {
			id.value = num
		}
		return nil
	}

	// Try to unmarshal as a string
	var str string
	if err := json.Unmarshal(data, &str); err == nil {
		id.value = str
		return nil
	}

	return fmt.Errorf("JSON-RPC ID must be a string or number, got: %s", string(data))
}
