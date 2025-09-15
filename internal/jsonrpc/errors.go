package jsonrpc

// ErrorCode is a JSON-RPC 2.0 error code.
type ErrorCode int

const (
	// ErrorCodeParseError indicates invalid JSON was received by the server.
	ErrorCodeParseError ErrorCode = -32700
	// ErrorCodeInvalidRequest indicates the JSON sent is not a valid Request object.
	ErrorCodeInvalidRequest ErrorCode = -32600
	// ErrorCodeMethodNotFound indicates the method does not exist / is not available.
	ErrorCodeMethodNotFound ErrorCode = -32601
	// ErrorCodeInvalidParams indicates invalid method parameters.
	ErrorCodeInvalidParams ErrorCode = -32602
	// ErrorCodeInternalError indicates an internal JSON-RPC error.
	ErrorCodeInternalError ErrorCode = -32603
)
