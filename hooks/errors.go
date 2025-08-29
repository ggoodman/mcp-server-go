package hooks

import "fmt"

// NotFoundError indicates a requested item (tool, resource, prompt) doesn't exist.
// This should be returned when the implementation cannot find the requested entity
// and should result in a JSON-RPC "Method not found" error.
type NotFoundError struct {
	Type string // "tool", "resource", "prompt"
	Name string // identifier that wasn't found
}

func (e *NotFoundError) Error() string {
	return fmt.Sprintf("%s not found: %s", e.Type, e.Name)
}

// AlreadySubscribedError indicates attempting to subscribe to an already-subscribed resource.
// This should result in a JSON-RPC "Invalid params" error.
type AlreadySubscribedError struct {
	URI string
}

func (e *AlreadySubscribedError) Error() string {
	return fmt.Sprintf("already subscribed to resource: %s", e.URI)
}

// NotSubscribedError indicates attempting to unsubscribe from a non-subscribed resource.
// This should result in a JSON-RPC "Invalid params" error.
type NotSubscribedError struct {
	URI string
}

func (e *NotSubscribedError) Error() string {
	return fmt.Sprintf("not subscribed to resource: %s", e.URI)
}

// InvalidParamsError indicates that the provided parameters are invalid.
// This should result in a JSON-RPC "Invalid params" error.
type InvalidParamsError struct {
	Field  string // which field is invalid
	Reason string // why it's invalid
}

func (e *InvalidParamsError) Error() string {
	if e.Field != "" {
		return fmt.Sprintf("invalid parameter %s: %s", e.Field, e.Reason)
	}
	return fmt.Sprintf("invalid parameters: %s", e.Reason)
}

// UnsupportedOperationError indicates that the requested operation is not supported.
// This should result in a JSON-RPC "Method not found" error.
type UnsupportedOperationError struct {
	Operation string
}

func (e *UnsupportedOperationError) Error() string {
	return fmt.Sprintf("operation not supported: %s", e.Operation)
}
