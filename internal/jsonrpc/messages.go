package jsonrpc

import (
	"encoding/json"
	"fmt"
)

// ProtocolVersion is the supported JSON-RPC protocol version.
const ProtocolVersion = "2.0"

// Message is the raw JSON representation of a JSON-RPC message.
type Message []byte

// AnyMessage is a generic JSON-RPC message (request, notification, or response).
type AnyMessage struct {
	JSONRPCVersion string          `json:"jsonrpc"`
	Method         string          `json:"method,omitempty"`
	Params         json.RawMessage `json:"params,omitempty"`
	Result         json.RawMessage `json:"result,omitempty"`
	Error          *Error          `json:"error,omitempty"`
	ID             *RequestID      `json:"id,omitempty"`
}

// Request represents a JSON-RPC request (with an ID) or notification (without ID).
type Request struct {
	JSONRPCVersion string          `json:"jsonrpc"`
	Method         string          `json:"method"`
	Params         json.RawMessage `json:"params,omitempty"`
	ID             *RequestID      `json:"id,omitempty"`
}

// Response represents a JSON-RPC response.
type Response struct {
	JSONRPCVersion string          `json:"jsonrpc"`
	Result         json.RawMessage `json:"result,omitempty"`
	Error          *Error          `json:"error,omitempty"`
	ID             *RequestID      `json:"id,omitempty"`
}

// NewResultResponse builds a successful JSON-RPC response object.
func NewResultResponse(id *RequestID, result any) (*Response, error) {
	resultBytes, err := json.Marshal(result)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal result: %w", err)
	}

	return &Response{
		JSONRPCVersion: ProtocolVersion,
		Result:         resultBytes,
		ID:             id,
	}, nil
}

// NewErrorResponse builds an error JSON-RPC response with the given code.
func NewErrorResponse(id *RequestID, code ErrorCode, message string, data any) *Response {
	return &Response{
		JSONRPCVersion: ProtocolVersion,
		Error: &Error{
			Code:    code,
			Message: message,
			Data:    data,
		},
		ID: id,
	}
}

// Error is a JSON-RPC error object.
type Error struct {
	Code    ErrorCode `json:"code"`
	Message string    `json:"message"`
	Data    any       `json:"data,omitempty"`
}

// UnmarshalJSON implements custom JSON unmarshaling for AnyMessage
// It enforces JSON-RPC 2.0 semantics and validates message structure
func (m *AnyMessage) UnmarshalJSON(data []byte) error {
	// Define a temporary struct to capture raw JSON
	type rawMessage struct {
		JSONRPCVersion string          `json:"jsonrpc"`
		Method         string          `json:"method,omitempty"`
		Params         json.RawMessage `json:"params,omitempty"`
		Result         json.RawMessage `json:"result,omitempty"`
		Error          *Error          `json:"error,omitempty"`
		ID             *RequestID      `json:"id,omitempty"`
	}

	var raw rawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("invalid JSON: %w", err)
	}

	// Validate JSON-RPC version
	if raw.JSONRPCVersion != ProtocolVersion {
		return fmt.Errorf("invalid JSON-RPC version: expected %q, got %q", ProtocolVersion, raw.JSONRPCVersion)
	}

	// Determine message type and validate structure
	hasMethod := raw.Method != ""
	hasResult := len(raw.Result) > 0
	hasError := raw.Error != nil

	if hasMethod {
		// This should be a request
		if hasResult || hasError {
			return fmt.Errorf("request message cannot have result or error fields")
		}
	} else {
		// This should be a response
		if hasResult && hasError {
			return fmt.Errorf("response message cannot have both result and error fields")
		}
		if !hasResult && !hasError {
			return fmt.Errorf("response message must have either result or error field")
		}
	}

	// Copy validated fields to the AnyMessage
	m.JSONRPCVersion = raw.JSONRPCVersion
	m.Method = raw.Method
	m.Params = raw.Params
	m.Result = raw.Result
	m.Error = raw.Error
	m.ID = raw.ID

	return nil
}

// Type returns "request" if the message is a request, "response" if it's a response, or "notification" if it's a notification
func (m *AnyMessage) Type() string {
	if m.Method != "" {
		if m.ID == nil {
			return "notification"
		}
		return "request"
	}
	return "response"
}

// AsRequest returns the message as a Request if it is a request message, otherwise nil
func (m *AnyMessage) AsRequest() *Request {
	if m.Method == "" {
		return nil
	}

	return &Request{
		JSONRPCVersion: m.JSONRPCVersion,
		Method:         m.Method,
		Params:         m.Params,
		ID:             m.ID,
	}
}

// AsResponse returns the message as a Response if it is a response message, otherwise nil
func (m *AnyMessage) AsResponse() *Response {
	if m.Method != "" {
		return nil
	}

	return &Response{
		JSONRPCVersion: m.JSONRPCVersion,
		Result:         m.Result,
		Error:          m.Error,
		ID:             m.ID,
	}
}
