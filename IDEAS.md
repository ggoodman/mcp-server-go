# Ideas

## [x] Split session interface into internal and external components

Our library code doesn't really need the developer-facing interface of the `sessions.Session` interface. Conversely, developers who are building MCP servers with this library don't need the internal-facing parts of the interface.

We should find a way to tease these two concepts apart so that developers building MCP servers can't use the internal plumbing bits. This might be do-able by having the internal object produce (or own) an object that implements the public-facing interface.

## Tools definition via reflection

We could use the `github.com/invopop/jsonschema` package to both define and help unmarshal arguments to tool calls. This would dramatically simplify the redundancy of defining static tools. A user of the library would only need to supply a properly-annotated struct and the appropriate json schemas would be constructed from that. In this way, no manual json-schema construction is required.

## Session lifetime management

Currently, the `sessions.Session` instances we create don't have a clear lifecycle. When we load a session from the manager, what is returned is a sort of reference to the longer-lived platonic session. The session reference and all its resources should be cancelled when the http request for which they were created finishes (or errors). We need to review how we propagate `context.Context` in light of this. For example, if the server makes a request to the client via a client capability, its resources need not last longer than the controlling session. There is some use of `context.Background` and `context.WithoutCancel` that we need to be careful about.
