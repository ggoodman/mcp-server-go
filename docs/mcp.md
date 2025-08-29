mcp-streaming-http-go/docs/mcp/2025-06-18/architecture.md:

````
# Architecture

<div id="enable-section-numbers" />

The Model Context Protocol (MCP) follows a client-host-server architecture where each
host can run multiple client instances. This architecture enables users to integrate AI
capabilities across applications while maintaining clear security boundaries and
isolating concerns. Built on JSON-RPC, MCP provides a stateful session protocol focused
on context exchange and sampling coordination between clients and servers.

## Core Components

```mermaid
graph LR
    subgraph "Application Host Process"
        H[Host]
        C1[Client 1]
        C2[Client 2]
        C3[Client 3]
        H --> C1
        H --> C2
        H --> C3
    end

    subgraph "Local machine"
        S1[Server 1<br>Files & Git]
        S2[Server 2<br>Database]
        R1[("Local<br>Resource A")]
        R2[("Local<br>Resource B")]

        C1 --> S1
        C2 --> S2
        S1 <--> R1
        S2 <--> R2
    end

    subgraph "Internet"
        S3[Server 3<br>External APIs]
        R3[("Remote<br>Resource C")]

        C3 --> S3
        S3 <--> R3
    end
````

### Host

The host process acts as the container and coordinator:

- Creates and manages multiple client instances
- Controls client connection permissions and lifecycle
- Enforces security policies and consent requirements
- Handles user authorization decisions
- Coordinates AI/LLM integration and sampling
- Manages context aggregation across clients

### Clients

Each client is created by the host and maintains an isolated server connection:

- Establishes one stateful session per server
- Handles protocol negotiation and capability exchange
- Routes protocol messages bidirectionally
- Manages subscriptions and notifications
- Maintains security boundaries between servers

A host application creates and manages multiple clients, with each client having a 1:1
relationship with a particular server.

### Servers

Servers provide specialized context and capabilities:

- Expose resources, tools and prompts via MCP primitives
- Operate independently with focused responsibilities
- Request sampling through client interfaces
- Must respect security constraints
- Can be local processes or remote services

## Design Principles

MCP is built on several key design principles that inform its architecture and
implementation:

1. **Servers should be extremely easy to build**

   - Host applications handle complex orchestration responsibilities
   - Servers focus on specific, well-defined capabilities
   - Simple interfaces minimize implementation overhead
   - Clear separation enables maintainable code

2. **Servers should be highly composable**

   - Each server provides focused functionality in isolation
   - Multiple servers can be combined seamlessly
   - Shared protocol enables interoperability
   - Modular design supports extensibility

3. **Servers should not be able to read the whole conversation, nor "see into" other
   servers**

   - Servers receive only necessary contextual information
   - Full conversation history stays with the host
   - Each server connection maintains isolation
   - Cross-server interactions are controlled by the host
   - Host process enforces security boundaries

4. **Features can be added to servers and clients progressively**
   - Core protocol provides minimal required functionality
   - Additional capabilities can be negotiated as needed
   - Servers and clients evolve independently
   - Protocol designed for future extensibility
   - Backwards compatibility is maintained

## Capability Negotiation

The Model Context Protocol uses a capability-based negotiation system where clients and
servers explicitly declare their supported features during initialization. Capabilities
determine which protocol features and primitives are available during a session.

- Servers declare capabilities like resource subscriptions, tool support, and prompt
  templates
- Clients declare capabilities like sampling support and notification handling
- Both parties must respect declared capabilities throughout the session
- Additional capabilities can be negotiated through extensions to the protocol

```mermaid
sequenceDiagram
    participant Host
    participant Client
    participant Server

    Host->>+Client: Initialize client
    Client->>+Server: Initialize session with capabilities
    Server-->>Client: Respond with supported capabilities

    Note over Host,Server: Active Session with Negotiated Features

    loop Client Requests
        Host->>Client: User- or model-initiated action
        Client->>Server: Request (tools/resources)
        Server-->>Client: Response
        Client-->>Host: Update UI or respond to model
    end

    loop Server Requests
        Server->>Client: Request (sampling)
        Client->>Host: Forward to AI
        Host-->>Client: AI response
        Client-->>Server: Response
    end

    loop Notifications
        Server--)Client: Resource updates
        Client--)Server: Status changes
    end

    Host->>Client: Terminate
    Client->>-Server: End session
    deactivate Server
```

Each capability unlocks specific protocol features for use during the session. For
example:

- Implemented [server features](/specification/2025-06-18/server) must be advertised in the
  server's capabilities
- Emitting resource subscription notifications requires the server to declare
  subscription support
- Tool invocation requires the server to declare tool capabilities
- [Sampling](/specification/2025-06-18/client) requires the client to declare support in its
  capabilities

This capability negotiation ensures clients and servers have a clear understanding of
supported functionality while maintaining protocol extensibility.

```

mcp-streaming-http-go/docs/mcp/2025-06-18/basic/authorization.md:
```

# Authorization

<div id="enable-section-numbers" />

<Info>**Protocol Revision**: 2025-06-18</Info>

## Introduction

### Purpose and Scope

The Model Context Protocol provides authorization capabilities at the transport level,
enabling MCP clients to make requests to restricted MCP servers on behalf of resource
owners. This specification defines the authorization flow for HTTP-based transports.

### Protocol Requirements

Authorization is **OPTIONAL** for MCP implementations. When supported:

- Implementations using an HTTP-based transport **SHOULD** conform to this specification.
- Implementations using an STDIO transport **SHOULD NOT** follow this specification, and
  instead retrieve credentials from the environment.
- Implementations using alternative transports **MUST** follow established security best
  practices for their protocol.

### Standards Compliance

This authorization mechanism is based on established specifications listed below, but
implements a selected subset of their features to ensure security and interoperability
while maintaining simplicity:

- OAuth 2.1 IETF DRAFT ([draft-ietf-oauth-v2-1-13](https://datatracker.ietf.org/doc/html/draft-ietf-oauth-v2-1-13))
- OAuth 2.0 Authorization Server Metadata
  ([RFC8414](https://datatracker.ietf.org/doc/html/rfc8414))
- OAuth 2.0 Dynamic Client Registration Protocol
  ([RFC7591](https://datatracker.ietf.org/doc/html/rfc7591))
- OAuth 2.0 Protected Resource Metadata ([RFC9728](https://datatracker.ietf.org/doc/html/rfc9728))

## Authorization Flow

### Roles

A protected _MCP server_ acts as an [OAuth 2.1 resource server](https://www.ietf.org/archive/id/draft-ietf-oauth-v2-1-13.html#name-roles),
capable of accepting and responding to protected resource requests using access tokens.

An _MCP client_ acts as an [OAuth 2.1 client](https://www.ietf.org/archive/id/draft-ietf-oauth-v2-1-13.html#name-roles),
making protected resource requests on behalf of a resource owner.

The _authorization server_ is responsible for interacting with the user (if necessary) and issuing access tokens for use at the MCP server.
The implementation details of the authorization server are beyond the scope of this specification. It may be hosted with the
resource server or a separate entity. The [Authorization Server Discovery section](#authorization-server-discovery)
specifies how an MCP server indicates the location of its corresponding authorization server to a client.

### Overview

1. Authorization servers **MUST** implement OAuth 2.1 with appropriate security
   measures for both confidential and public clients.

2. Authorization servers and MCP clients **SHOULD** support the OAuth 2.0 Dynamic Client Registration
   Protocol ([RFC7591](https://datatracker.ietf.org/doc/html/rfc7591)).

3. MCP servers **MUST** implement OAuth 2.0 Protected Resource Metadata ([RFC9728](https://datatracker.ietf.org/doc/html/rfc9728)).
   MCP clients **MUST** use OAuth 2.0 Protected Resource Metadata for authorization server discovery.

4. Authorization servers **MUST** provide OAuth 2.0 Authorization
   Server Metadata ([RFC8414](https://datatracker.ietf.org/doc/html/rfc8414)).
   MCP clients **MUST** use the OAuth 2.0 Authorization Server Metadata.

### Authorization Server Discovery

This section describes the mechanisms by which MCP servers advertise their associated
authorization servers to MCP clients, as well as the discovery process through which MCP
clients can determine authorization server endpoints and supported capabilities.

#### Authorization Server Location

MCP servers **MUST** implement the OAuth 2.0 Protected Resource Metadata ([RFC9728](https://datatracker.ietf.org/doc/html/rfc9728))
specification to indicate the locations of authorization servers. The Protected Resource Metadata document returned by the MCP server **MUST** include
the `authorization_servers` field containing at least one authorization server.

The specific use of `authorization_servers` is beyond the scope of this specification; implementers should consult
OAuth 2.0 Protected Resource Metadata ([RFC9728](https://datatracker.ietf.org/doc/html/rfc9728)) for
guidance on implementation details.

Implementors should note that Protected Resource Metadata documents can define multiple authorization servers. The responsibility for selecting which authorization server to use lies with the MCP client, following the guidelines specified in
[RFC9728 Section 7.6 "Authorization Servers"](https://datatracker.ietf.org/doc/html/rfc9728#name-authorization-servers).

MCP servers **MUST** use the HTTP header `WWW-Authenticate` when returning a _401 Unauthorized_ to indicate the location of the resource server metadata URL
as described in [RFC9728 Section 5.1 "WWW-Authenticate Response"](https://datatracker.ietf.org/doc/html/rfc9728#name-www-authenticate-response).

MCP clients **MUST** be able to parse `WWW-Authenticate` headers and respond appropriately to `HTTP 401 Unauthorized` responses from the MCP server.

#### Server Metadata Discovery

MCP clients **MUST** follow the OAuth 2.0 Authorization Server Metadata [RFC8414](https://datatracker.ietf.org/doc/html/rfc8414)
specification to obtain the information required to interact with the authorization server.

#### Sequence Diagram

The following diagram outlines an example flow:

```mermaid
sequenceDiagram
    participant C as Client
    participant M as MCP Server (Resource Server)
    participant A as Authorization Server

    C->>M: MCP request without token
    M-->>C: HTTP 401 Unauthorized with WWW-Authenticate header
    Note over C: Extract resource_metadata<br />from WWW-Authenticate

    C->>M: GET /.well-known/oauth-protected-resource
    M-->>C: Resource metadata with authorization server URL
    Note over C: Validate RS metadata,<br />build AS metadata URL

    C->>A: GET /.well-known/oauth-authorization-server
    A-->>C: Authorization server metadata

    Note over C,A: OAuth 2.1 authorization flow happens here

    C->>A: Token request
    A-->>C: Access token

    C->>M: MCP request with access token
    M-->>C: MCP response
    Note over C,M: MCP communication continues with valid token
```

### Dynamic Client Registration

MCP clients and authorization servers **SHOULD** support the
OAuth 2.0 Dynamic Client Registration Protocol [RFC7591](https://datatracker.ietf.org/doc/html/rfc7591)
to allow MCP clients to obtain OAuth client IDs without user interaction. This provides a
standardized way for clients to automatically register with new authorization servers, which is crucial
for MCP because:

- Clients may not know all possible MCP servers and their authorization servers in advance.
- Manual registration would create friction for users.
- It enables seamless connection to new MCP servers and their authorization servers.
- Authorization servers can implement their own registration policies.

Any authorization servers that _do not_ support Dynamic Client Registration need to provide
alternative ways to obtain a client ID (and, if applicable, client credentials). For one of
these authorization servers, MCP clients will have to either:

1. Hardcode a client ID (and, if applicable, client credentials) specifically for the MCP client to use when
   interacting with that authorization server, or
2. Present a UI to users that allows them to enter these details, after registering an
   OAuth client themselves (e.g., through a configuration interface hosted by the
   server).

### Authorization Flow Steps

The complete Authorization flow proceeds as follows:

```mermaid
sequenceDiagram
    participant B as User-Agent (Browser)
    participant C as Client
    participant M as MCP Server (Resource Server)
    participant A as Authorization Server

    C->>M: MCP request without token
    M->>C: HTTP 401 Unauthorized with WWW-Authenticate header
    Note over C: Extract resource_metadata URL from WWW-Authenticate

    C->>M: Request Protected Resource Metadata
    M->>C: Return metadata

    Note over C: Parse metadata and extract authorization server(s)<br/>Client determines AS to use

    C->>A: GET /.well-known/oauth-authorization-server
    A->>C: Authorization server metadata response

    alt Dynamic client registration
        C->>A: POST /register
        A->>C: Client Credentials
    end

    Note over C: Generate PKCE parameters<br/>Include resource parameter
    C->>B: Open browser with authorization URL + code_challenge + resource
    B->>A: Authorization request with resource parameter
    Note over A: User authorizes
    A->>B: Redirect to callback with authorization code
    B->>C: Authorization code callback
    C->>A: Token request + code_verifier + resource
    A->>C: Access token (+ refresh token)
    C->>M: MCP request with access token
    M-->>C: MCP response
    Note over C,M: MCP communication continues with valid token
```

#### Resource Parameter Implementation

MCP clients **MUST** implement Resource Indicators for OAuth 2.0 as defined in [RFC 8707](https://www.rfc-editor.org/rfc/rfc8707.html)
to explicitly specify the target resource for which the token is being requested. The `resource` parameter:

1. **MUST** be included in both authorization requests and token requests.
2. **MUST** identify the MCP server that the client intends to use the token with.
3. **MUST** use the canonical URI of the MCP server as defined in [RFC 8707 Section 2](https://www.rfc-editor.org/rfc/rfc8707.html#name-access-token-request).

##### Canonical Server URI

For the purposes of this specification, the canonical URI of an MCP server is defined as the resource identifier as specified in
[RFC 8707 Section 2](https://www.rfc-editor.org/rfc/rfc8707.html#section-2) and aligns with the `resource` parameter in
[RFC 9728](https://datatracker.ietf.org/doc/html/rfc9728).

MCP clients **SHOULD** provide the most specific URI that they can for the MCP server they intend to access, following the guidance in [RFC 8707](https://www.rfc-editor.org/rfc/rfc8707). While the canonical form uses lowercase scheme and host components, implementations **SHOULD** accept uppercase scheme and host components for robustness and interoperability.

Examples of valid canonical URIs:

- `https://mcp.example.com/mcp`
- `https://mcp.example.com`
- `https://mcp.example.com:8443`
- `https://mcp.example.com/server/mcp` (when path component is necessary to identify individual MCP server)

Examples of invalid canonical URIs:

- `mcp.example.com` (missing scheme)
- `https://mcp.example.com#fragment` (contains fragment)

> **Note:** While both `https://mcp.example.com/` (with trailing slash) and `https://mcp.example.com` (without trailing slash) are technically valid absolute URIs according to [RFC 3986](https://www.rfc-editor.org/rfc/rfc3986), implementations **SHOULD** consistently use the form without the trailing slash for better interoperability unless the trailing slash is semantically significant for the specific resource.

For example, if accessing an MCP server at `https://mcp.example.com`, the authorization request would include:

```
&resource=https%3A%2F%2Fmcp.example.com
```

MCP clients **MUST** send this parameter regardless of whether authorization servers support it.

### Access Token Usage

#### Token Requirements

Access token handling when making requests to MCP servers **MUST** conform to the requirements defined in
[OAuth 2.1 Section 5 "Resource Requests"](https://datatracker.ietf.org/doc/html/draft-ietf-oauth-v2-1-13#section-5).
Specifically:

1. MCP client **MUST** use the Authorization request header field defined in
   [OAuth 2.1 Section 5.1.1](https://datatracker.ietf.org/doc/html/draft-ietf-oauth-v2-1-13#section-5.1.1):

```
Authorization: Bearer <access-token>
```

Note that authorization **MUST** be included in every HTTP request from client to server,
even if they are part of the same logical session.

2. Access tokens **MUST NOT** be included in the URI query string

Example request:

```http
GET /mcp HTTP/1.1
Host: mcp.example.com
Authorization: Bearer eyJhbGciOiJIUzI1NiIs...
```

#### Token Handling

MCP servers, acting in their role as an OAuth 2.1 resource server, **MUST** validate access tokens as described in
[OAuth 2.1 Section 5.2](https://datatracker.ietf.org/doc/html/draft-ietf-oauth-v2-1-13#section-5.2).
MCP servers **MUST** validate that access tokens were issued specifically for them as the intended audience,
according to [RFC 8707 Section 2](https://www.rfc-editor.org/rfc/rfc8707.html#section-2).
If validation fails, servers **MUST** respond according to
[OAuth 2.1 Section 5.3](https://datatracker.ietf.org/doc/html/draft-ietf-oauth-v2-1-13#section-5.3)
error handling requirements. Invalid or expired tokens **MUST** receive a HTTP 401
response.

MCP clients **MUST NOT** send tokens to the MCP server other than ones issued by the MCP server's authorization server.

Authorization servers **MUST** only accept tokens that are valid for use with their
own resources.

MCP servers **MUST NOT** accept or transit any other tokens.

### Error Handling

Servers **MUST** return appropriate HTTP status codes for authorization errors:

| Status Code | Description  | Usage                                      |
| ----------- | ------------ | ------------------------------------------ |
| 401         | Unauthorized | Authorization required or token invalid    |
| 403         | Forbidden    | Invalid scopes or insufficient permissions |
| 400         | Bad Request  | Malformed authorization request            |

## Security Considerations

Implementations **MUST** follow OAuth 2.1 security best practices as laid out in [OAuth 2.1 Section 7. "Security Considerations"](https://datatracker.ietf.org/doc/html/draft-ietf-oauth-v2-1-13#name-security-considerations).

### Token Audience Binding and Validation

[RFC 8707](https://www.rfc-editor.org/rfc/rfc8707.html) Resource Indicators provide critical security benefits by binding tokens to their intended
audiences **when the Authorization Server supports the capability**. To enable current and future adoption:

- MCP clients **MUST** include the `resource` parameter in authorization and token requests as specified in the [Resource Parameter Implementation](#resource-parameter-implementation) section
- MCP servers **MUST** validate that tokens presented to them were specifically issued for their use

The [Security Best Practices document](/specification/2025-06-18/basic/security_best_practices#token-passthrough)
outlines why token audience validation is crucial and why token passthrough is explicitly forbidden.

### Token Theft

Attackers who obtain tokens stored by the client, or tokens cached or logged on the server can access protected resources with
requests that appear legitimate to resource servers.

Clients and servers **MUST** implement secure token storage and follow OAuth best practices,
as outlined in [OAuth 2.1, Section 7.1](https://datatracker.ietf.org/doc/html/draft-ietf-oauth-v2-1-13#section-7.1).

Authorization servers **SHOULD** issue short-lived access tokens to reduce the impact of leaked tokens.
For public clients, authorization servers **MUST** rotate refresh tokens as described in [OAuth 2.1 Section 4.3.1 "Token Endpoint Extension"](https://datatracker.ietf.org/doc/html/draft-ietf-oauth-v2-1-13#section-4.3.1).

### Communication Security

Implementations **MUST** follow [OAuth 2.1 Section 1.5 "Communication Security"](https://datatracker.ietf.org/doc/html/draft-ietf-oauth-v2-1-13#section-1.5).

Specifically:

1. All authorization server endpoints **MUST** be served over HTTPS.
2. All redirect URIs **MUST** be either `localhost` or use HTTPS.

### Authorization Code Protection

An attacker who has gained access to an authorization code contained in an authorization response can try to redeem the authorization code for an access token or otherwise make use of the authorization code.
(Further described in [OAuth 2.1 Section 7.5](https://datatracker.ietf.org/doc/html/draft-ietf-oauth-v2-1-13#section-7.5))

To mitigate this, MCP clients **MUST** implement PKCE according to [OAuth 2.1 Section 7.5.2](https://datatracker.ietf.org/doc/html/draft-ietf-oauth-v2-1-13#section-7.5.2).
PKCE helps prevent authorization code interception and injection attacks by requiring clients to create a secret verifier-challenge pair, ensuring that only the original requestor can exchange an authorization code for tokens.

### Open Redirection

An attacker may craft malicious redirect URIs to direct users to phishing sites.

MCP clients **MUST** have redirect URIs registered with the authorization server.

Authorization servers **MUST** validate exact redirect URIs against pre-registered values to prevent redirection attacks.

MCP clients **SHOULD** use and verify state parameters in the authorization code flow
and discard any results that do not include or have a mismatch with the original state.

Authorization servers **MUST** take precautions to prevent redirecting user agents to untrusted URI's, following suggestions laid out in [OAuth 2.1 Section 7.12.2](https://datatracker.ietf.org/doc/html/draft-ietf-oauth-v2-1-13#section-7.12.2)

Authorization servers **SHOULD** only automatically redirect the user agent if it trusts the redirection URI. If the URI is not trusted, the authorization server MAY inform the user and rely on the user to make the correct decision.

### Confused Deputy Problem

Attackers can exploit MCP servers acting as intermediaries to third-party APIs, leading to [confused deputy vulnerabilities](/specification/2025-06-18/basic/security_best_practices#confused-deputy-problem).
By using stolen authorization codes, they can obtain access tokens without user consent.

MCP proxy servers using static client IDs **MUST** obtain user consent for each dynamically
registered client before forwarding to third-party authorization servers (which may require additional consent).

### Access Token Privilege Restriction

An attacker can gain unauthorized access or otherwise compromise an MCP server if the server accepts tokens issued for other resources.

This vulnerability has two critical dimensions:

1. **Audience validation failures.** When an MCP server doesn't verify that tokens were specifically intended for it (for example, via the audience claim, as mentioned in [RFC9068](https://www.rfc-editor.org/rfc/rfc9068.html)), it may accept tokens originally issued for other services. This breaks a fundamental OAuth security boundary, allowing attackers to reuse legitimate tokens across different services than intended.
2. **Token passthrough.** If the MCP server not only accepts tokens with incorrect audiences but also forwards these unmodified tokens to downstream services, it can potentially cause the ["confused deputy" problem](#confused-deputy-problem), where the downstream API may incorrectly trust the token as if it came from the MCP server or assume the token was validated by the upstream API. See the [Token Passthrough section](/specification/2025-06-18/basic/security_best_practices#token-passthrough) of the Security Best Practices guide for additional details.

MCP servers **MUST** validate access tokens before processing the request, ensuring the access token is issued specifically for the MCP server, and take all necessary steps to ensure no data is returned to unauthorized parties.

A MCP server **MUST** follow the guidelines in [OAuth 2.1 - Section 5.2](https://www.ietf.org/archive/id/draft-ietf-oauth-v2-1-13.html#section-5.2) to validate inbound tokens.

MCP servers **MUST** only accept tokens specifically intended for themselves and **MUST** reject tokens that do not include them in the audience claim or otherwise verify that they are the intended recipient of the token. See the [Security Best Practices Token Passthrough section](/specification/2025-06-18/basic/security_best_practices#token-passthrough) for details.

If the MCP server makes requests to upstream APIs, it may act as an OAuth client to them. The access token used at the upstream API is a separate token, issued by the upstream authorization server. The MCP server **MUST NOT** pass through the token it received from the MCP client.

MCP clients **MUST** implement and use the `resource` parameter as defined in [RFC 8707 - Resource Indicators for OAuth 2.0](https://www.rfc-editor.org/rfc/rfc8707.html)
to explicitly specify the target resource for which the token is being requested. This requirement aligns with the recommendation in
[RFC 9728 Section 7.4](https://datatracker.ietf.org/doc/html/rfc9728#section-7.4). This ensures that access tokens are bound to their intended resources and
cannot be misused across different services.

```

mcp-streaming-http-go/docs/mcp/2025-06-18/basic/lifecycle.md:
```

# Lifecycle

<div id="enable-section-numbers" />

<Info>**Protocol Revision**: 2025-06-18</Info>

The Model Context Protocol (MCP) defines a rigorous lifecycle for client-server
connections that ensures proper capability negotiation and state management.

1. **Initialization**: Capability negotiation and protocol version agreement
2. **Operation**: Normal protocol communication
3. **Shutdown**: Graceful termination of the connection

```mermaid
sequenceDiagram
    participant Client
    participant Server

    Note over Client,Server: Initialization Phase
    activate Client
    Client->>+Server: initialize request
    Server-->>Client: initialize response
    Client--)Server: initialized notification

    Note over Client,Server: Operation Phase
    rect rgb(200, 220, 250)
        note over Client,Server: Normal protocol operations
    end

    Note over Client,Server: Shutdown
    Client--)-Server: Disconnect
    deactivate Server
    Note over Client,Server: Connection closed
```

## Lifecycle Phases

### Initialization

The initialization phase **MUST** be the first interaction between client and server.
During this phase, the client and server:

- Establish protocol version compatibility
- Exchange and negotiate capabilities
- Share implementation details

The client **MUST** initiate this phase by sending an `initialize` request containing:

- Protocol version supported
- Client capabilities
- Client implementation information

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "initialize",
  "params": {
    "protocolVersion": "2024-11-05",
    "capabilities": {
      "roots": {
        "listChanged": true
      },
      "sampling": {},
      "elicitation": {}
    },
    "clientInfo": {
      "name": "ExampleClient",
      "title": "Example Client Display Name",
      "version": "1.0.0"
    }
  }
}
```

The server **MUST** respond with its own capabilities and information:

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "result": {
    "protocolVersion": "2024-11-05",
    "capabilities": {
      "logging": {},
      "prompts": {
        "listChanged": true
      },
      "resources": {
        "subscribe": true,
        "listChanged": true
      },
      "tools": {
        "listChanged": true
      }
    },
    "serverInfo": {
      "name": "ExampleServer",
      "title": "Example Server Display Name",
      "version": "1.0.0"
    },
    "instructions": "Optional instructions for the client"
  }
}
```

After successful initialization, the client **MUST** send an `initialized` notification
to indicate it is ready to begin normal operations:

```json
{
  "jsonrpc": "2.0",
  "method": "notifications/initialized"
}
```

- The client **SHOULD NOT** send requests other than
  [pings](/specification/2025-06-18/basic/utilities/ping) before the server has responded to the
  `initialize` request.
- The server **SHOULD NOT** send requests other than
  [pings](/specification/2025-06-18/basic/utilities/ping) and
  [logging](/specification/2025-06-18/server/utilities/logging) before receiving the `initialized`
  notification.

#### Version Negotiation

In the `initialize` request, the client **MUST** send a protocol version it supports.
This **SHOULD** be the _latest_ version supported by the client.

If the server supports the requested protocol version, it **MUST** respond with the same
version. Otherwise, the server **MUST** respond with another protocol version it
supports. This **SHOULD** be the _latest_ version supported by the server.

If the client does not support the version in the server's response, it **SHOULD**
disconnect.

<Note>
  If using HTTP, the client **MUST** include the `MCP-Protocol-Version: <protocol-version>` HTTP header on all subsequent requests to the MCP
  server.
  For details, see [the Protocol Version Header section in Transports](/specification/2025-06-18/basic/transports#protocol-version-header).
</Note>

#### Capability Negotiation

Client and server capabilities establish which optional protocol features will be
available during the session.

Key capabilities include:

| Category | Capability     | Description                                                                               |
| -------- | -------------- | ----------------------------------------------------------------------------------------- |
| Client   | `roots`        | Ability to provide filesystem [roots](/specification/2025-06-18/client/roots)             |
| Client   | `sampling`     | Support for LLM [sampling](/specification/2025-06-18/client/sampling) requests            |
| Client   | `elicitation`  | Support for server [elicitation](/specification/2025-06-18/client/elicitation) requests   |
| Client   | `experimental` | Describes support for non-standard experimental features                                  |
| Server   | `prompts`      | Offers [prompt templates](/specification/2025-06-18/server/prompts)                       |
| Server   | `resources`    | Provides readable [resources](/specification/2025-06-18/server/resources)                 |
| Server   | `tools`        | Exposes callable [tools](/specification/2025-06-18/server/tools)                          |
| Server   | `logging`      | Emits structured [log messages](/specification/2025-06-18/server/utilities/logging)       |
| Server   | `completions`  | Supports argument [autocompletion](/specification/2025-06-18/server/utilities/completion) |
| Server   | `experimental` | Describes support for non-standard experimental features                                  |

Capability objects can describe sub-capabilities like:

- `listChanged`: Support for list change notifications (for prompts, resources, and
  tools)
- `subscribe`: Support for subscribing to individual items' changes (resources only)

### Operation

During the operation phase, the client and server exchange messages according to the
negotiated capabilities.

Both parties **MUST**:

- Respect the negotiated protocol version
- Only use capabilities that were successfully negotiated

### Shutdown

During the shutdown phase, one side (usually the client) cleanly terminates the protocol
connection. No specific shutdown messages are defined—instead, the underlying transport
mechanism should be used to signal connection termination:

#### stdio

For the stdio [transport](/specification/2025-06-18/basic/transports), the client **SHOULD** initiate
shutdown by:

1. First, closing the input stream to the child process (the server)
2. Waiting for the server to exit, or sending `SIGTERM` if the server does not exit
   within a reasonable time
3. Sending `SIGKILL` if the server does not exit within a reasonable time after `SIGTERM`

The server **MAY** initiate shutdown by closing its output stream to the client and
exiting.

#### HTTP

For HTTP [transports](/specification/2025-06-18/basic/transports), shutdown is indicated by closing the
associated HTTP connection(s).

## Timeouts

Implementations **SHOULD** establish timeouts for all sent requests, to prevent hung
connections and resource exhaustion. When the request has not received a success or error
response within the timeout period, the sender **SHOULD** issue a [cancellation
notification](/specification/2025-06-18/basic/utilities/cancellation) for that request and stop waiting for
a response.

SDKs and other middleware **SHOULD** allow these timeouts to be configured on a
per-request basis.

Implementations **MAY** choose to reset the timeout clock when receiving a [progress
notification](/specification/2025-06-18/basic/utilities/progress) corresponding to the request, as this
implies that work is actually happening. However, implementations **SHOULD** always
enforce a maximum timeout, regardless of progress notifications, to limit the impact of a
misbehaving client or server.

## Error Handling

Implementations **SHOULD** be prepared to handle these error cases:

- Protocol version mismatch
- Failure to negotiate required capabilities
- Request [timeouts](#timeouts)

Example initialization error:

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "error": {
    "code": -32602,
    "message": "Unsupported protocol version",
    "data": {
      "supported": ["2024-11-05"],
      "requested": "1.0.0"
    }
  }
}
```

```

mcp-streaming-http-go/docs/mcp/2025-06-18/basic/security_best_practices.md:
```

# Security Best Practices

<div id="enable-section-numbers" />

## Introduction

### Purpose and Scope

This document provides security considerations for the Model Context Protocol (MCP), complementing the MCP Authorization specification. This document identifies security risks, attack vectors, and best practices specific to MCP implementations.

The primary audience for this document includes developers implementing MCP authorization flows, MCP server operators, and security professionals evaluating MCP-based systems. This document should be read alongside the MCP Authorization specification and [OAuth 2.0 security best practices](https://datatracker.ietf.org/doc/html/rfc9700).

## Attacks and Mitigations

This section gives a detailed description of attacks on MCP implementations, along with potential countermeasures.

### Confused Deputy Problem

Attackers can exploit MCP servers proxying other resource servers, creating "[confused deputy](https://en.wikipedia.org/wiki/Confused_deputy_problem)" vulnerabilities.

#### Terminology

**MCP Proxy Server**
: An MCP server that connects MCP clients to third-party APIs, offering MCP features while delegating operations and acting as a single OAuth client to the third-party API server.

**Third-Party Authorization Server**
: Authorization server that protects the third-party API. It may lack dynamic client registration support, requiring MCP proxy to use a static client ID for all requests.

**Third-Party API**
: The protected resource server that provides the actual API functionality. Access to this
API requires tokens issued by the third-party authorization server.

**Static Client ID**
: A fixed OAuth 2.0 client identifier used by the MCP proxy server when communicating with
the third-party authorization server. This Client ID refers to the MCP server acting as a client
to the Third-Party API. It is the same value for all MCP server to Third-Party API interactions regardless of
which MCP client initiated the request.

#### Architecture and Attack Flows

##### Normal OAuth proxy usage (preserves user consent)

```mermaid
sequenceDiagram
    participant UA as User-Agent (Browser)
    participant MC as MCP Client
    participant M as MCP Proxy Server
    participant TAS as Third-Party Authorization Server

    Note over UA,M: Initial Auth flow completed

    Note over UA,TAS: Step 1: Legitimate user consent for Third Party Server

    M->>UA: Redirect to third party authorization server
    UA->>TAS: Authorization request (client_id: mcp-proxy)
    TAS->>UA: Authorization consent screen
    Note over UA: Review consent screen
    UA->>TAS: Approve
    TAS->>UA: Set consent cookie for client ID: mcp-proxy
    TAS->>UA: 3P Authorization code + redirect to mcp-proxy-server.com
    UA->>M: 3P Authorization code
    Note over M,TAS: Exchange 3P code for 3P token
    Note over M: Generate MCP authorization code
    M->>UA: Redirect to MCP Client with MCP authorization code

    Note over M,UA: Exchange code for token, etc.
```

##### Malicious OAuth proxy usage (skips user consent)

```mermaid
sequenceDiagram
    participant UA as User-Agent (Browser)
    participant M as MCP Proxy Server
    participant TAS as Third-Party Authorization Server
    participant A as Attacker


    Note over UA,A: Step 2: Attack (leveraging existing cookie, skipping consent)
    A->>M: Dynamically register malicious client, redirect_uri: attacker.com
    A->>UA: Sends malicious link
    UA->>TAS: Authorization request (client_id: mcp-proxy) + consent cookie
    rect rgba(255, 17, 0, 0.67)
    TAS->>TAS: Cookie present, consent skipped
    end

   TAS->>UA: 3P Authorization code + redirect to mcp-proxy-server.com
   UA->>M: 3P Authorization code
   Note over M,TAS: Exchange 3P code for 3P token
   Note over M: Generate MCP authorization code
   M->>UA: Redirect to attacker.com with MCP Authorization code
   UA->>A: MCP Authorization code delivered to attacker.com
   Note over M,A: Attacker exchanges MCP code for MCP token
   A->>M: Attacker impersonates user to MCP server
```

#### Attack Description

When an MCP proxy server uses a static client ID to authenticate with a third-party
authorization server that does not support dynamic client registration, the following
attack becomes possible:

1. A user authenticates normally through the MCP proxy server to access the third-party API
2. During this flow, the third-party authorization server sets a cookie on the user agent
   indicating consent for the static client ID
3. An attacker later sends the user a malicious link containing a crafted authorization request which contains a malicious redirect URI along with a new dynamically registered client ID
4. When the user clicks the link, their browser still has the consent cookie from the previous legitimate request
5. The third-party authorization server detects the cookie and skips the consent screen
6. The MCP authorization code is redirected to the attacker's server (specified in the crafted redirect_uri during dynamic client registration)
7. The attacker exchanges the stolen authorization code for access tokens for the MCP server without the user's explicit approval
8. Attacker now has access to the third-party API as the compromised user

#### Mitigation

MCP proxy servers using static client IDs **MUST** obtain user consent for each dynamically
registered client before forwarding to third-party authorization servers (which may require additional consent).

### Token Passthrough

"Token passthrough" is an anti-pattern where an MCP server accepts tokens from an MCP client without validating that the tokens were properly issued _to the MCP server_ and "passing them through" to the downstream API.

#### Risks

Token passthrough is explicitly forbidden in the [authorization specification](/specification/2025-06-18/basic/authorization) as it introduces a number of security risks, that include:

- **Security Control Circumvention**
  - The MCP Server or downstream APIs might implement important security controls like rate limiting, request validation, or traffic monitoring, that depend on the token audience or other credential constraints. If clients can obtain and use tokens directly with the downstream APIs without the MCP server validating them properly or ensuring that the tokens are issued for the right service, they bypass these controls.
- **Accountability and Audit Trail Issues**
  - The MCP Server will be unable to identify or distinguish between MCP Clients when clients are calling with an upstream-issued access token which may be opaque to the MCP Server.
  - The downstream Resource Server’s logs may show requests that appear to come from a different source with a different identity, rather than the MCP server that is actually forwarding the tokens.
  - Both factors make incident investigation, controls, and auditing more difficult.
  - If the MCP Server passes tokens without validating their claims (e.g., roles, privileges, or audience) or other metadata, a malicious actor in possession of a stolen token can use the server as a proxy for data exfiltration.
- **Trust Boundary Issues**
  - The downstream Resource Server grants trust to specific entities. This trust might include assumptions about origin or client behavior patterns. Breaking this trust boundary could lead to unexpected issues.
  - If the token is accepted by multiple services without proper validation, an attacker compromising one service can use the token to access other connected services.
- **Future Compatibility Risk**
  - Even if an MCP Server starts as a "pure proxy" today, it might need to add security controls later. Starting with proper token audience separation makes it easier to evolve the security model.

#### Mitigation

MCP servers **MUST NOT** accept any tokens that were not explicitly issued for the MCP server.

### Session Hijacking

Session hijacking is an attack vector where a client is provided a session ID by the server, and an unauthorized party is able to obtain and use that same session ID to impersonate the original client and perform unauthorized actions on their behalf.

#### Session Hijack Prompt Injection

```mermaid
sequenceDiagram
    participant Client
    participant ServerA
    participant Queue
    participant ServerB
    participant Attacker

    Client->>ServerA: Initialize (connect to streamable HTTP server)
    ServerA-->>Client: Respond with session ID

    Attacker->>ServerB: Access/guess session ID
    Note right of Attacker: Attacker knows/guesses session ID

    Attacker->>ServerB: Trigger event (malicious payload, using session ID)
    ServerB->>Queue: Enqueue event (keyed by session ID)

    ServerA->>Queue: Poll for events (using session ID)
    Queue-->>ServerA: Event data (malicious payload)

    ServerA-->>Client: Async response (malicious payload)
    Client->>Client: Acts based on malicious payload
```

#### Session Hijack Impersonation

```mermaid
sequenceDiagram
    participant Client
    participant Server
    participant Attacker

    Client->>Server: Initialize (login/authenticate)
    Server-->>Client: Respond with session ID (persistent session created)

    Attacker->>Server: Access/guess session ID
    Note right of Attacker: Attacker knows/guesses session ID

    Attacker->>Server: Make API call (using session ID, no re-auth)
    Server-->>Attacker: Respond as if Attacker is Client (session hijack)
```

#### Attack Description

When you have multiple stateful HTTP servers that handle MCP requests, the following attack vectors are possible:

**Session Hijack Prompt Injection**

1. The client connects to **Server A** and receives a session ID.

2. The attacker obtains an existing session ID and sends a malicious event to **Server B** with said session ID.

   - When a server supports [redelivery/resumable streams](/specification/2025-06-18/basic/transports#resumability-and-redelivery), deliberately terminating the request before receiving the response could lead to it being resumed by the original client via the GET request for server sent events.
   - If a particular server initiates server sent events as a consequence of a tool call such as a `notifications/tools/list_changed`, where it is possible to affect the tools that are offered by the server, a client could end up with tools that they were not aware were enabled.

3. **Server B** enqueues the event (associated with session ID) into a shared queue.

4. **Server A** polls the queue for events using the session ID and retrieves the malicious payload.

5. **Server A** sends the malicious payload to the client as an asynchronous or resumed response.

6. The client receives and acts on the malicious payload, leading to potential compromise.

**Session Hijack Impersonation**

1. The MCP client authenticates with the MCP server, creating a persistent session ID.
2. The attacker obtains the session ID.
3. The attacker makes calls to the MCP server using the session ID.
4. MCP server does not check for additional authorization and treats the attacker as a legitimate user, allowing unauthorized access or actions.

#### Mitigation

To prevent session hijacking and event injection attacks, the following mitigations should be implemented:

MCP servers that implement authorization **MUST** verify all inbound requests.
MCP Servers **MUST NOT** use sessions for authentication.

MCP servers **MUST** use secure, non-deterministic session IDs.
Generated session IDs (e.g., UUIDs) **SHOULD** use secure random number generators. Avoid predictable or sequential session identifiers that could be guessed by an attacker. Rotating or expiring session IDs can also reduce the risk.

MCP servers **SHOULD** bind session IDs to user-specific information.
When storing or transmitting session-related data (e.g., in a queue), combine the session ID with information unique to the authorized user, such as their internal user ID. Use a key format like `<user_id>:<session_id>`. This ensures that even if an attacker guesses a session ID, they cannot impersonate another user as the user ID is derived from the user token and not provided by the client.

MCP servers can optionally leverage additional unique identifiers.

```

mcp-streaming-http-go/docs/mcp/2025-06-18/basic/transports.md:
```

# Transports

<div id="enable-section-numbers" />

<Info>**Protocol Revision**: 2025-06-18</Info>

MCP uses JSON-RPC to encode messages. JSON-RPC messages **MUST** be UTF-8 encoded.

The protocol currently defines two standard transport mechanisms for client-server
communication:

1. [stdio](#stdio), communication over standard in and standard out
2. [Streamable HTTP](#streamable-http)

Clients **SHOULD** support stdio whenever possible.

It is also possible for clients and servers to implement
[custom transports](#custom-transports) in a pluggable fashion.

## stdio

In the **stdio** transport:

- The client launches the MCP server as a subprocess.
- The server reads JSON-RPC messages from its standard input (`stdin`) and sends messages
  to its standard output (`stdout`).
- Messages are individual JSON-RPC requests, notifications, or responses.
- Messages are delimited by newlines, and **MUST NOT** contain embedded newlines.
- The server **MAY** write UTF-8 strings to its standard error (`stderr`) for logging
  purposes. Clients **MAY** capture, forward, or ignore this logging.
- The server **MUST NOT** write anything to its `stdout` that is not a valid MCP message.
- The client **MUST NOT** write anything to the server's `stdin` that is not a valid MCP
  message.

```mermaid
sequenceDiagram
    participant Client
    participant Server Process

    Client->>+Server Process: Launch subprocess
    loop Message Exchange
        Client->>Server Process: Write to stdin
        Server Process->>Client: Write to stdout
        Server Process--)Client: Optional logs on stderr
    end
    Client->>Server Process: Close stdin, terminate subprocess
    deactivate Server Process
```

## Streamable HTTP

<Info>
  This replaces the [HTTP+SSE
  transport](/specification/2024-11-05/basic/transports#http-with-sse) from
  protocol version 2024-11-05. See the [backwards compatibility](#backwards-compatibility)
  guide below.
</Info>

In the **Streamable HTTP** transport, the server operates as an independent process that
can handle multiple client connections. This transport uses HTTP POST and GET requests.
Server can optionally make use of
[Server-Sent Events](https://en.wikipedia.org/wiki/Server-sent_events) (SSE) to stream
multiple server messages. This permits basic MCP servers, as well as more feature-rich
servers supporting streaming and server-to-client notifications and requests.

The server **MUST** provide a single HTTP endpoint path (hereafter referred to as the
**MCP endpoint**) that supports both POST and GET methods. For example, this could be a
URL like `https://example.com/mcp`.

#### Security Warning

When implementing Streamable HTTP transport:

1. Servers **MUST** validate the `Origin` header on all incoming connections to prevent DNS rebinding attacks
2. When running locally, servers **SHOULD** bind only to localhost (127.0.0.1) rather than all network interfaces (0.0.0.0)
3. Servers **SHOULD** implement proper authentication for all connections

Without these protections, attackers could use DNS rebinding to interact with local MCP servers from remote websites.

### Sending Messages to the Server

Every JSON-RPC message sent from the client **MUST** be a new HTTP POST request to the
MCP endpoint.

1. The client **MUST** use HTTP POST to send JSON-RPC messages to the MCP endpoint.
2. The client **MUST** include an `Accept` header, listing both `application/json` and
   `text/event-stream` as supported content types.
3. The body of the POST request **MUST** be a single JSON-RPC _request_, _notification_, or _response_.
4. If the input is a JSON-RPC _response_ or _notification_:
   - If the server accepts the input, the server **MUST** return HTTP status code 202
     Accepted with no body.
   - If the server cannot accept the input, it **MUST** return an HTTP error status code
     (e.g., 400 Bad Request). The HTTP response body **MAY** comprise a JSON-RPC _error
     response_ that has no `id`.
5. If the input is a JSON-RPC _request_, the server **MUST** either
   return `Content-Type: text/event-stream`, to initiate an SSE stream, or
   `Content-Type: application/json`, to return one JSON object. The client **MUST**
   support both these cases.
6. If the server initiates an SSE stream:
   - The SSE stream **SHOULD** eventually include JSON-RPC _response_ for the
     JSON-RPC _request_ sent in the POST body.
   - The server **MAY** send JSON-RPC _requests_ and _notifications_ before sending the
     JSON-RPC _response_. These messages **SHOULD** relate to the originating client
     _request_.
   - The server **SHOULD NOT** close the SSE stream before sending the JSON-RPC _response_
     for the received JSON-RPC _request_, unless the [session](#session-management)
     expires.
   - After the JSON-RPC _response_ has been sent, the server **SHOULD** close the SSE
     stream.
   - Disconnection **MAY** occur at any time (e.g., due to network conditions).
     Therefore:
     - Disconnection **SHOULD NOT** be interpreted as the client cancelling its request.
     - To cancel, the client **SHOULD** explicitly send an MCP `CancelledNotification`.
     - To avoid message loss due to disconnection, the server **MAY** make the stream
       [resumable](#resumability-and-redelivery).

### Listening for Messages from the Server

1. The client **MAY** issue an HTTP GET to the MCP endpoint. This can be used to open an
   SSE stream, allowing the server to communicate to the client, without the client first
   sending data via HTTP POST.
2. The client **MUST** include an `Accept` header, listing `text/event-stream` as a
   supported content type.
3. The server **MUST** either return `Content-Type: text/event-stream` in response to
   this HTTP GET, or else return HTTP 405 Method Not Allowed, indicating that the server
   does not offer an SSE stream at this endpoint.
4. If the server initiates an SSE stream:
   - The server **MAY** send JSON-RPC _requests_ and _notifications_ on the stream.
   - These messages **SHOULD** be unrelated to any concurrently-running JSON-RPC
     _request_ from the client.
   - The server **MUST NOT** send a JSON-RPC _response_ on the stream **unless**
     [resuming](#resumability-and-redelivery) a stream associated with a previous client
     request.
   - The server **MAY** close the SSE stream at any time.
   - The client **MAY** close the SSE stream at any time.

### Multiple Connections

1. The client **MAY** remain connected to multiple SSE streams simultaneously.
2. The server **MUST** send each of its JSON-RPC messages on only one of the connected
   streams; that is, it **MUST NOT** broadcast the same message across multiple streams.
   - The risk of message loss **MAY** be mitigated by making the stream
     [resumable](#resumability-and-redelivery).

### Resumability and Redelivery

To support resuming broken connections, and redelivering messages that might otherwise be
lost:

1. Servers **MAY** attach an `id` field to their SSE events, as described in the
   [SSE standard](https://html.spec.whatwg.org/multipage/server-sent-events.html#event-stream-interpretation).
   - If present, the ID **MUST** be globally unique across all streams within that
     [session](#session-management)—or all streams with that specific client, if session
     management is not in use.
2. If the client wishes to resume after a broken connection, it **SHOULD** issue an HTTP
   GET to the MCP endpoint, and include the
   [`Last-Event-ID`](https://html.spec.whatwg.org/multipage/server-sent-events.html#the-last-event-id-header)
   header to indicate the last event ID it received.
   - The server **MAY** use this header to replay messages that would have been sent
     after the last event ID, _on the stream that was disconnected_, and to resume the
     stream from that point.
   - The server **MUST NOT** replay messages that would have been delivered on a
     different stream.

In other words, these event IDs should be assigned by servers on a _per-stream_ basis, to
act as a cursor within that particular stream.

### Session Management

An MCP "session" consists of logically related interactions between a client and a
server, beginning with the [initialization phase](/specification/2025-06-18/basic/lifecycle). To support
servers which want to establish stateful sessions:

1. A server using the Streamable HTTP transport **MAY** assign a session ID at
   initialization time, by including it in an `Mcp-Session-Id` header on the HTTP
   response containing the `InitializeResult`.
   - The session ID **SHOULD** be globally unique and cryptographically secure (e.g., a
     securely generated UUID, a JWT, or a cryptographic hash).
   - The session ID **MUST** only contain visible ASCII characters (ranging from 0x21 to
     0x7E).
2. If an `Mcp-Session-Id` is returned by the server during initialization, clients using
   the Streamable HTTP transport **MUST** include it in the `Mcp-Session-Id` header on
   all of their subsequent HTTP requests.
   - Servers that require a session ID **SHOULD** respond to requests without an
     `Mcp-Session-Id` header (other than initialization) with HTTP 400 Bad Request.
3. The server **MAY** terminate the session at any time, after which it **MUST** respond
   to requests containing that session ID with HTTP 404 Not Found.
4. When a client receives HTTP 404 in response to a request containing an
   `Mcp-Session-Id`, it **MUST** start a new session by sending a new `InitializeRequest`
   without a session ID attached.
5. Clients that no longer need a particular session (e.g., because the user is leaving
   the client application) **SHOULD** send an HTTP DELETE to the MCP endpoint with the
   `Mcp-Session-Id` header, to explicitly terminate the session.
   - The server **MAY** respond to this request with HTTP 405 Method Not Allowed,
     indicating that the server does not allow clients to terminate sessions.

### Sequence Diagram

```mermaid
sequenceDiagram
    participant Client
    participant Server

    note over Client, Server: initialization

    Client->>+Server: POST InitializeRequest
    Server->>-Client: InitializeResponse<br>Mcp-Session-Id: 1868a90c...

    Client->>+Server: POST InitializedNotification<br>Mcp-Session-Id: 1868a90c...
    Server->>-Client: 202 Accepted

    note over Client, Server: client requests
    Client->>+Server: POST ... request ...<br>Mcp-Session-Id: 1868a90c...

    alt single HTTP response
      Server->>Client: ... response ...
    else server opens SSE stream
      loop while connection remains open
          Server-)Client: ... SSE messages from server ...
      end
      Server-)Client: SSE event: ... response ...
    end
    deactivate Server

    note over Client, Server: client notifications/responses
    Client->>+Server: POST ... notification/response ...<br>Mcp-Session-Id: 1868a90c...
    Server->>-Client: 202 Accepted

    note over Client, Server: server requests
    Client->>+Server: GET<br>Mcp-Session-Id: 1868a90c...
    loop while connection remains open
        Server-)Client: ... SSE messages from server ...
    end
    deactivate Server

```

### Protocol Version Header

If using HTTP, the client **MUST** include the `MCP-Protocol-Version: <protocol-version>` HTTP header on all subsequent requests to the MCP
server, allowing the MCP server to respond based on the MCP protocol version.

For example: `MCP-Protocol-Version: 2025-06-18`

The protocol version sent by the client **SHOULD** be the one [negotiated during
initialization](/specification/2025-06-18/basic/lifecycle#version-negotiation).

For backwards compatibility, if the server does _not_ receive an `MCP-Protocol-Version`
header, and has no other way to identify the version - for example, by relying on the
protocol version negotiated during initialization - the server **SHOULD** assume protocol
version `2025-03-26`.

If the server receives a request with an invalid or unsupported
`MCP-Protocol-Version`, it **MUST** respond with `400 Bad Request`.

### Backwards Compatibility

Clients and servers can maintain backwards compatibility with the deprecated [HTTP+SSE
transport](/specification/2024-11-05/basic/transports#http-with-sse) (from
protocol version 2024-11-05) as follows:

**Servers** wanting to support older clients should:

- Continue to host both the SSE and POST endpoints of the old transport, alongside the
  new "MCP endpoint" defined for the Streamable HTTP transport.
  - It is also possible to combine the old POST endpoint and the new MCP endpoint, but
    this may introduce unneeded complexity.

**Clients** wanting to support older servers should:

1. Accept an MCP server URL from the user, which may point to either a server using the
   old transport or the new transport.
2. Attempt to POST an `InitializeRequest` to the server URL, with an `Accept` header as
   defined above:
   - If it succeeds, the client can assume this is a server supporting the new Streamable
     HTTP transport.
   - If it fails with an HTTP 4xx status code (e.g., 405 Method Not Allowed or 404 Not
     Found):
     - Issue a GET request to the server URL, expecting that this will open an SSE stream
       and return an `endpoint` event as the first event.
     - When the `endpoint` event arrives, the client can assume this is a server running
       the old HTTP+SSE transport, and should use that transport for all subsequent
       communication.

## Custom Transports

Clients and servers **MAY** implement additional custom transport mechanisms to suit
their specific needs. The protocol is transport-agnostic and can be implemented over any
communication channel that supports bidirectional message exchange.

Implementers who choose to support custom transports **MUST** ensure they preserve the
JSON-RPC message format and lifecycle requirements defined by MCP. Custom transports
**SHOULD** document their specific connection establishment and message exchange patterns
to aid interoperability.

```

mcp-streaming-http-go/docs/mcp/2025-06-18/basic/utilities/cancellation.md:
```

# Cancellation

<div id="enable-section-numbers" />

<Info>**Protocol Revision**: 2025-06-18</Info>

The Model Context Protocol (MCP) supports optional cancellation of in-progress requests
through notification messages. Either side can send a cancellation notification to
indicate that a previously-issued request should be terminated.

## Cancellation Flow

When a party wants to cancel an in-progress request, it sends a `notifications/cancelled`
notification containing:

- The ID of the request to cancel
- An optional reason string that can be logged or displayed

```json
{
  "jsonrpc": "2.0",
  "method": "notifications/cancelled",
  "params": {
    "requestId": "123",
    "reason": "User requested cancellation"
  }
}
```

## Behavior Requirements

1. Cancellation notifications **MUST** only reference requests that:
   - Were previously issued in the same direction
   - Are believed to still be in-progress
2. The `initialize` request **MUST NOT** be cancelled by clients
3. Receivers of cancellation notifications **SHOULD**:
   - Stop processing the cancelled request
   - Free associated resources
   - Not send a response for the cancelled request
4. Receivers **MAY** ignore cancellation notifications if:
   - The referenced request is unknown
   - Processing has already completed
   - The request cannot be cancelled
5. The sender of the cancellation notification **SHOULD** ignore any response to the
   request that arrives afterward

## Timing Considerations

Due to network latency, cancellation notifications may arrive after request processing
has completed, and potentially after a response has already been sent.

Both parties **MUST** handle these race conditions gracefully:

```mermaid
sequenceDiagram
   participant Client
   participant Server

   Client->>Server: Request (ID: 123)
   Note over Server: Processing starts
   Client--)Server: notifications/cancelled (ID: 123)
   alt
      Note over Server: Processing may have<br/>completed before<br/>cancellation arrives
   else If not completed
      Note over Server: Stop processing
   end
```

## Implementation Notes

- Both parties **SHOULD** log cancellation reasons for debugging
- Application UIs **SHOULD** indicate when cancellation is requested

## Error Handling

Invalid cancellation notifications **SHOULD** be ignored:

- Unknown request IDs
- Already completed requests
- Malformed notifications

This maintains the "fire and forget" nature of notifications while allowing for race
conditions in asynchronous communication.

```

mcp-streaming-http-go/docs/mcp/2025-06-18/basic/utilities/ping.md:
```

# Ping

<div id="enable-section-numbers" />

<Info>**Protocol Revision**: 2025-06-18</Info>

The Model Context Protocol includes an optional ping mechanism that allows either party
to verify that their counterpart is still responsive and the connection is alive.

## Overview

The ping functionality is implemented through a simple request/response pattern. Either
the client or server can initiate a ping by sending a `ping` request.

## Message Format

A ping request is a standard JSON-RPC request with no parameters:

```json
{
  "jsonrpc": "2.0",
  "id": "123",
  "method": "ping"
}
```

## Behavior Requirements

1. The receiver **MUST** respond promptly with an empty response:

```json
{
  "jsonrpc": "2.0",
  "id": "123",
  "result": {}
}
```

2. If no response is received within a reasonable timeout period, the sender **MAY**:
   - Consider the connection stale
   - Terminate the connection
   - Attempt reconnection procedures

## Usage Patterns

```mermaid
sequenceDiagram
    participant Sender
    participant Receiver

    Sender->>Receiver: ping request
    Receiver->>Sender: empty response
```

## Implementation Considerations

- Implementations **SHOULD** periodically issue pings to detect connection health
- The frequency of pings **SHOULD** be configurable
- Timeouts **SHOULD** be appropriate for the network environment
- Excessive pinging **SHOULD** be avoided to reduce network overhead

## Error Handling

- Timeouts **SHOULD** be treated as connection failures
- Multiple failed pings **MAY** trigger connection reset
- Implementations **SHOULD** log ping failures for diagnostics

```

mcp-streaming-http-go/docs/mcp/2025-06-18/basic/utilities/progress.md:
```

# Progress

<div id="enable-section-numbers" />

<Info>**Protocol Revision**: 2025-06-18</Info>

The Model Context Protocol (MCP) supports optional progress tracking for long-running
operations through notification messages. Either side can send progress notifications to
provide updates about operation status.

## Progress Flow

When a party wants to _receive_ progress updates for a request, it includes a
`progressToken` in the request metadata.

- Progress tokens **MUST** be a string or integer value
- Progress tokens can be chosen by the sender using any means, but **MUST** be unique
  across all active requests.

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "some_method",
  "params": {
    "_meta": {
      "progressToken": "abc123"
    }
  }
}
```

The receiver **MAY** then send progress notifications containing:

- The original progress token
- The current progress value so far
- An optional "total" value
- An optional "message" value

```json
{
  "jsonrpc": "2.0",
  "method": "notifications/progress",
  "params": {
    "progressToken": "abc123",
    "progress": 50,
    "total": 100,
    "message": "Reticulating splines..."
  }
}
```

- The `progress` value **MUST** increase with each notification, even if the total is
  unknown.
- The `progress` and the `total` values **MAY** be floating point.
- The `message` field **SHOULD** provide relevant human readable progress information.

## Behavior Requirements

1. Progress notifications **MUST** only reference tokens that:

   - Were provided in an active request
   - Are associated with an in-progress operation

2. Receivers of progress requests **MAY**:
   - Choose not to send any progress notifications
   - Send notifications at whatever frequency they deem appropriate
   - Omit the total value if unknown

```mermaid
sequenceDiagram
    participant Sender
    participant Receiver

    Note over Sender,Receiver: Request with progress token
    Sender->>Receiver: Method request with progressToken

    Note over Sender,Receiver: Progress updates
    Receiver-->>Sender: Progress notification (0.2/1.0)
    Receiver-->>Sender: Progress notification (0.6/1.0)
    Receiver-->>Sender: Progress notification (1.0/1.0)

    Note over Sender,Receiver: Operation complete
    Receiver->>Sender: Method response
```

## Implementation Notes

- Senders and receivers **SHOULD** track active progress tokens
- Both parties **SHOULD** implement rate limiting to prevent flooding
- Progress notifications **MUST** stop after completion

```

mcp-streaming-http-go/docs/mcp/2025-06-18/basic.md:
```

# Overview

<div id="enable-section-numbers" />

<Info>**Protocol Revision**: 2025-06-18</Info>

The Model Context Protocol consists of several key components that work together:

- **Base Protocol**: Core JSON-RPC message types
- **Lifecycle Management**: Connection initialization, capability negotiation, and
  session control
- **Authorization**: Authentication and authorization framework for HTTP-based transports
- **Server Features**: Resources, prompts, and tools exposed by servers
- **Client Features**: Sampling and root directory lists provided by clients
- **Utilities**: Cross-cutting concerns like logging and argument completion

All implementations **MUST** support the base protocol and lifecycle management
components. Other components **MAY** be implemented based on the specific needs of the
application.

These protocol layers establish clear separation of concerns while enabling rich
interactions between clients and servers. The modular design allows implementations to
support exactly the features they need.

## Messages

All messages between MCP clients and servers **MUST** follow the
[JSON-RPC 2.0](https://www.jsonrpc.org/specification) specification. The protocol defines
these types of messages:

### Requests

Requests are sent from the client to the server or vice versa, to initiate an operation.

```typescript
{
  jsonrpc: "2.0";
  id: string | number;
  method: string;
  params?: {
    [key: string]: unknown;
  };
}
```

- Requests **MUST** include a string or integer ID.
- Unlike base JSON-RPC, the ID **MUST NOT** be `null`.
- The request ID **MUST NOT** have been previously used by the requestor within the same
  session.

### Responses

Responses are sent in reply to requests, containing the result or error of the operation.

```typescript
{
  jsonrpc: "2.0";
  id: string | number;
  result?: {
    [key: string]: unknown;
  }
  error?: {
    code: number;
    message: string;
    data?: unknown;
  }
}
```

- Responses **MUST** include the same ID as the request they correspond to.
- **Responses** are further sub-categorized as either **successful results** or
  **errors**. Either a `result` or an `error` **MUST** be set. A response **MUST NOT**
  set both.
- Results **MAY** follow any JSON object structure, while errors **MUST** include an
  error code and message at minimum.
- Error codes **MUST** be integers.

### Notifications

Notifications are sent from the client to the server or vice versa, as a one-way message.
The receiver **MUST NOT** send a response.

```typescript
{
  jsonrpc: "2.0";
  method: string;
  params?: {
    [key: string]: unknown;
  };
}
```

- Notifications **MUST NOT** include an ID.

## Auth

MCP provides an [Authorization](/specification/2025-06-18/basic/authorization) framework for use with HTTP.
Implementations using an HTTP-based transport **SHOULD** conform to this specification,
whereas implementations using STDIO transport **SHOULD NOT** follow this specification,
and instead retrieve credentials from the environment.

Additionally, clients and servers **MAY** negotiate their own custom authentication and
authorization strategies.

For further discussions and contributions to the evolution of MCP’s auth mechanisms, join
us in
[GitHub Discussions](https://github.com/modelcontextprotocol/specification/discussions)
to help shape the future of the protocol!

## Schema

The full specification of the protocol is defined as a
[TypeScript schema](https://github.com/modelcontextprotocol/specification/blob/main/schema/2025-06-18/schema.ts).
This is the source of truth for all protocol messages and structures.

There is also a
[JSON Schema](https://github.com/modelcontextprotocol/specification/blob/main/schema/2025-06-18/schema.json),
which is automatically generated from the TypeScript source of truth, for use with
various automated tooling.

### General fields

#### `_meta`

The `_meta` property/parameter is reserved by MCP to allow clients and servers
to attach additional metadata to their interactions.

Certain key names are reserved by MCP for protocol-level metadata, as specified below;
implementations MUST NOT make assumptions about values at these keys.

Additionally, definitions in the [schema](https://github.com/modelcontextprotocol/specification/blob/main/schema/2025-06-18/schema.ts)
may reserve particular names for purpose-specific metadata, as declared in those definitions.

**Key name format:** valid `_meta` key names have two segments: an optional **prefix**, and a **name**.

**Prefix:**

- If specified, MUST be a series of labels separated by dots (`.`), followed by a slash (`/`).
  - Labels MUST start with a letter and end with a letter or digit; interior characters can be letters, digits, or hyphens (`-`).
- Any prefix beginning with zero or more valid labels, followed by `modelcontextprotocol` or `mcp`, followed by any valid label,
  is **reserved** for MCP use.
  - For example: `modelcontextprotocol.io/`, `mcp.dev/`, `api.modelcontextprotocol.org/`, and `tools.mcp.com/` are all reserved.

**Name:**

- Unless empty, MUST begin and end with an alphanumeric character (`[a-z0-9A-Z]`).
- MAY contain hyphens (`-`), underscores (`_`), dots (`.`), and alphanumerics in between.

```

mcp-streaming-http-go/docs/mcp/2025-06-18/changelog.md:
```

# Key Changes

<div id="enable-section-numbers" />

This document lists changes made to the Model Context Protocol (MCP) specification since
the previous revision, [2025-03-26](/specification/2025-03-26).

## Major changes

1. Remove support for JSON-RPC **[batching](https://www.jsonrpc.org/specification#batch)**
   (PR [#416](https://github.com/modelcontextprotocol/specification/pull/416))
2. Add support for [structured tool output](/specification/2025-06-18/server/tools#structured-content)
   (PR [#371](https://github.com/modelcontextprotocol/modelcontextprotocol/pull/371))
3. Classify MCP servers as [OAuth Resource Servers](/specification/2025-06-18/basic/authorization#authorization-server-discovery),
   adding protected resource metadata to discover the corresponding Authorization server.
   (PR [#338](https://github.com/modelcontextprotocol/modelcontextprotocol/pull/338))
4. Require MCP clients to implement Resource Indicators as described in [RFC 8707](https://www.rfc-editor.org/rfc/rfc8707.html) to prevent
   malicious servers from obtaining access tokens.
   (PR [#734](https://github.com/modelcontextprotocol/modelcontextprotocol/pull/734))
5. Clarify [security considerations](/specification/2025-06-18/basic/authorization#security-considerations) and best practices
   in the authorization spec and in a new [security best practices page](/specification/2025-06-18/basic/security_best_practices).
6. Add support for **[elicitation](/specification/2025-06-18/client/elicitation)**, enabling servers to request additional
   information from users during interactions.
   (PR [#382](https://github.com/modelcontextprotocol/modelcontextprotocol/pull/382))
7. Add support for **[resource links](/specification/2025-06-18/server/tools#resource-links)** in
   tool call results. (PR [#603](https://github.com/modelcontextprotocol/modelcontextprotocol/pull/603))
8. Require [negotiated protocol version to be specified](/specification/2025-06-18/basic/transports#protocol-version-header)
   via `MCP-Protocol-Version` header in subsequent requests when using HTTP (PR [#548](https://github.com/modelcontextprotocol/modelcontextprotocol/pull/548)).
9. Change **SHOULD** to **MUST** in [Lifecycle Operation](/specification/2025-06-18/basic/lifecycle#operation)

## Other schema changes

1. Add `_meta` field to additional interface types (PR [#710](https://github.com/modelcontextprotocol/modelcontextprotocol/pull/710)),
   and specify [proper usage](/specification/2025-06-18/basic#meta).
2. Add `context` field to `CompletionRequest`, providing for completion requests to include
   previously-resolved variables (PR [#598](https://github.com/modelcontextprotocol/modelcontextprotocol/pull/598)).
3. Add `title` field for human-friendly display names, so that `name` can be used as a programmatic
   identifier (PR [#663](https://github.com/modelcontextprotocol/modelcontextprotocol/pull/663))

## Full changelog

For a complete list of all changes that have been made since the last protocol revision,
[see GitHub](https://github.com/modelcontextprotocol/specification/compare/2025-03-26...2025-06-18).

```

mcp-streaming-http-go/docs/mcp/2025-06-18/client/elicitation.md:
```

# Elicitation

<div id="enable-section-numbers" />

<Info>**Protocol Revision**: 2025-06-18</Info>

<Note>
  Elicitation is newly introduced in this version of the MCP specification and its design may evolve in future protocol versions.
</Note>

The Model Context Protocol (MCP) provides a standardized way for servers to request additional
information from users through the client during interactions. This flow allows clients to
maintain control over user interactions and data sharing while enabling servers to gather
necessary information dynamically.
Servers request structured data from users with JSON schemas to validate responses.

## User Interaction Model

Elicitation in MCP allows servers to implement interactive workflows by enabling user input
requests to occur _nested_ inside other MCP server features.

Implementations are free to expose elicitation through any interface pattern that suits
their needs—the protocol itself does not mandate any specific user interaction
model.

<Warning>
  For trust & safety and security:

- Servers **MUST NOT** use elicitation to request sensitive information.

Applications **SHOULD**:

- Provide UI that makes it clear which server is requesting information
- Allow users to review and modify their responses before sending
- Respect user privacy and provide clear decline and cancel options
  </Warning>

## Capabilities

Clients that support elicitation **MUST** declare the `elicitation` capability during
[initialization](/specification/2025-06-18/basic/lifecycle#initialization):

```json
{
  "capabilities": {
    "elicitation": {}
  }
}
```

## Protocol Messages

### Creating Elicitation Requests

To request information from a user, servers send an `elicitation/create` request:

#### Simple Text Request

**Request:**

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "elicitation/create",
  "params": {
    "message": "Please provide your GitHub username",
    "requestedSchema": {
      "type": "object",
      "properties": {
        "name": {
          "type": "string"
        }
      },
      "required": ["name"]
    }
  }
}
```

**Response:**

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "result": {
    "action": "accept",
    "content": {
      "name": "octocat"
    }
  }
}
```

#### Structured Data Request

**Request:**

```json
{
  "jsonrpc": "2.0",
  "id": 2,
  "method": "elicitation/create",
  "params": {
    "message": "Please provide your contact information",
    "requestedSchema": {
      "type": "object",
      "properties": {
        "name": {
          "type": "string",
          "description": "Your full name"
        },
        "email": {
          "type": "string",
          "format": "email",
          "description": "Your email address"
        },
        "age": {
          "type": "number",
          "minimum": 18,
          "description": "Your age"
        }
      },
      "required": ["name", "email"]
    }
  }
}
```

**Response:**

```json
{
  "jsonrpc": "2.0",
  "id": 2,
  "result": {
    "action": "accept",
    "content": {
      "name": "Monalisa Octocat",
      "email": "octocat@github.com",
      "age": 30
    }
  }
}
```

**Reject Response Example:**

```json
{
  "jsonrpc": "2.0",
  "id": 2,
  "result": {
    "action": "decline"
  }
}
```

**Cancel Response Example:**

```json
{
  "jsonrpc": "2.0",
  "id": 2,
  "result": {
    "action": "cancel"
  }
}
```

## Message Flow

```mermaid
sequenceDiagram
    participant User
    participant Client
    participant Server

    Note over Server,Client: Server initiates elicitation
    Server->>Client: elicitation/create

    Note over Client,User: Human interaction
    Client->>User: Present elicitation UI
    User-->>Client: Provide requested information

    Note over Server,Client: Complete request
    Client-->>Server: Return user response

    Note over Server: Continue processing with new information
```

## Request Schema

The `requestedSchema` field allows servers to define the structure of the expected response using a restricted subset of JSON Schema. To simplify implementation for clients, elicitation schemas are limited to flat objects with primitive properties only:

```json
"requestedSchema": {
  "type": "object",
  "properties": {
    "propertyName": {
      "type": "string",
      "title": "Display Name",
      "description": "Description of the property"
    },
    "anotherProperty": {
      "type": "number",
      "minimum": 0,
      "maximum": 100
    }
  },
  "required": ["propertyName"]
}
```

### Supported Schema Types

The schema is restricted to these primitive types:

1. **String Schema**

   ```json
   {
     "type": "string",
     "title": "Display Name",
     "description": "Description text",
     "minLength": 3,
     "maxLength": 50,
     "format": "email" // Supported: "email", "uri", "date", "date-time"
   }
   ```

   Supported formats: `email`, `uri`, `date`, `date-time`

2. **Number Schema**

   ```json
   {
     "type": "number", // or "integer"
     "title": "Display Name",
     "description": "Description text",
     "minimum": 0,
     "maximum": 100
   }
   ```

3. **Boolean Schema**

   ```json
   {
     "type": "boolean",
     "title": "Display Name",
     "description": "Description text",
     "default": false
   }
   ```

4. **Enum Schema**
   ```json
   {
     "type": "string",
     "title": "Display Name",
     "description": "Description text",
     "enum": ["option1", "option2", "option3"],
     "enumNames": ["Option 1", "Option 2", "Option 3"]
   }
   ```

Clients can use this schema to:

1. Generate appropriate input forms
2. Validate user input before sending
3. Provide better guidance to users

Note that complex nested structures, arrays of objects, and other advanced JSON Schema features are intentionally not supported to simplify client implementation.

## Response Actions

Elicitation responses use a three-action model to clearly distinguish between different user actions:

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "result": {
    "action": "accept", // or "decline" or "cancel"
    "content": {
      "propertyName": "value",
      "anotherProperty": 42
    }
  }
}
```

The three response actions are:

1. **Accept** (`action: "accept"`): User explicitly approved and submitted with data

   - The `content` field contains the submitted data matching the requested schema
   - Example: User clicked "Submit", "OK", "Confirm", etc.

2. **Decline** (`action: "decline"`): User explicitly declined the request

   - The `content` field is typically omitted
   - Example: User clicked "Reject", "Decline", "No", etc.

3. **Cancel** (`action: "cancel"`): User dismissed without making an explicit choice
   - The `content` field is typically omitted
   - Example: User closed the dialog, clicked outside, pressed Escape, etc.

Servers should handle each state appropriately:

- **Accept**: Process the submitted data
- **Decline**: Handle explicit decline (e.g., offer alternatives)
- **Cancel**: Handle dismissal (e.g., prompt again later)

## Security Considerations

1. Servers **MUST NOT** request sensitive information through elicitation
2. Clients **SHOULD** implement user approval controls
3. Both parties **SHOULD** validate elicitation content against the provided schema
4. Clients **SHOULD** provide clear indication of which server is requesting information
5. Clients **SHOULD** allow users to decline elicitation requests at any time
6. Clients **SHOULD** implement rate limiting
7. Clients **SHOULD** present elicitation requests in a way that makes it clear what information is being requested and why

```

mcp-streaming-http-go/docs/mcp/2025-06-18/client/roots.md:
```

# Roots

<div id="enable-section-numbers" />

<Info>**Protocol Revision**: 2025-06-18</Info>

The Model Context Protocol (MCP) provides a standardized way for clients to expose
filesystem "roots" to servers. Roots define the boundaries of where servers can operate
within the filesystem, allowing them to understand which directories and files they have
access to. Servers can request the list of roots from supporting clients and receive
notifications when that list changes.

## User Interaction Model

Roots in MCP are typically exposed through workspace or project configuration interfaces.

For example, implementations could offer a workspace/project picker that allows users to
select directories and files the server should have access to. This can be combined with
automatic workspace detection from version control systems or project files.

However, implementations are free to expose roots through any interface pattern that
suits their needs—the protocol itself does not mandate any specific user
interaction model.

## Capabilities

Clients that support roots **MUST** declare the `roots` capability during
[initialization](/specification/2025-06-18/basic/lifecycle#initialization):

```json
{
  "capabilities": {
    "roots": {
      "listChanged": true
    }
  }
}
```

`listChanged` indicates whether the client will emit notifications when the list of roots
changes.

## Protocol Messages

### Listing Roots

To retrieve roots, servers send a `roots/list` request:

**Request:**

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "roots/list"
}
```

**Response:**

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "result": {
    "roots": [
      {
        "uri": "file:///home/user/projects/myproject",
        "name": "My Project"
      }
    ]
  }
}
```

### Root List Changes

When roots change, clients that support `listChanged` **MUST** send a notification:

```json
{
  "jsonrpc": "2.0",
  "method": "notifications/roots/list_changed"
}
```

## Message Flow

```mermaid
sequenceDiagram
    participant Server
    participant Client

    Note over Server,Client: Discovery
    Server->>Client: roots/list
    Client-->>Server: Available roots

    Note over Server,Client: Changes
    Client--)Server: notifications/roots/list_changed
    Server->>Client: roots/list
    Client-->>Server: Updated roots
```

## Data Types

### Root

A root definition includes:

- `uri`: Unique identifier for the root. This **MUST** be a `file://` URI in the current
  specification.
- `name`: Optional human-readable name for display purposes.

Example roots for different use cases:

#### Project Directory

```json
{
  "uri": "file:///home/user/projects/myproject",
  "name": "My Project"
}
```

#### Multiple Repositories

```json
[
  {
    "uri": "file:///home/user/repos/frontend",
    "name": "Frontend Repository"
  },
  {
    "uri": "file:///home/user/repos/backend",
    "name": "Backend Repository"
  }
]
```

## Error Handling

Clients **SHOULD** return standard JSON-RPC errors for common failure cases:

- Client does not support roots: `-32601` (Method not found)
- Internal errors: `-32603`

Example error:

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "error": {
    "code": -32601,
    "message": "Roots not supported",
    "data": {
      "reason": "Client does not have roots capability"
    }
  }
}
```

## Security Considerations

1. Clients **MUST**:

   - Only expose roots with appropriate permissions
   - Validate all root URIs to prevent path traversal
   - Implement proper access controls
   - Monitor root accessibility

2. Servers **SHOULD**:
   - Handle cases where roots become unavailable
   - Respect root boundaries during operations
   - Validate all paths against provided roots

## Implementation Guidelines

1. Clients **SHOULD**:

   - Prompt users for consent before exposing roots to servers
   - Provide clear user interfaces for root management
   - Validate root accessibility before exposing
   - Monitor for root changes

2. Servers **SHOULD**:
   - Check for roots capability before usage
   - Handle root list changes gracefully
   - Respect root boundaries in operations
   - Cache root information appropriately

```

mcp-streaming-http-go/docs/mcp/2025-06-18/client/sampling.md:
```

# Sampling

<div id="enable-section-numbers" />

<Info>**Protocol Revision**: 2025-06-18</Info>

The Model Context Protocol (MCP) provides a standardized way for servers to request LLM
sampling ("completions" or "generations") from language models via clients. This flow
allows clients to maintain control over model access, selection, and permissions while
enabling servers to leverage AI capabilities—with no server API keys necessary.
Servers can request text, audio, or image-based interactions and optionally include
context from MCP servers in their prompts.

## User Interaction Model

Sampling in MCP allows servers to implement agentic behaviors, by enabling LLM calls to
occur _nested_ inside other MCP server features.

Implementations are free to expose sampling through any interface pattern that suits
their needs—the protocol itself does not mandate any specific user interaction
model.

<Warning>
  For trust & safety and security, there **SHOULD** always
  be a human in the loop with the ability to deny sampling requests.

Applications **SHOULD**:

- Provide UI that makes it easy and intuitive to review sampling requests
- Allow users to view and edit prompts before sending
- Present generated responses for review before delivery
  </Warning>

## Capabilities

Clients that support sampling **MUST** declare the `sampling` capability during
[initialization](/specification/2025-06-18/basic/lifecycle#initialization):

```json
{
  "capabilities": {
    "sampling": {}
  }
}
```

## Protocol Messages

### Creating Messages

To request a language model generation, servers send a `sampling/createMessage` request:

**Request:**

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "sampling/createMessage",
  "params": {
    "messages": [
      {
        "role": "user",
        "content": {
          "type": "text",
          "text": "What is the capital of France?"
        }
      }
    ],
    "modelPreferences": {
      "hints": [
        {
          "name": "claude-3-sonnet"
        }
      ],
      "intelligencePriority": 0.8,
      "speedPriority": 0.5
    },
    "systemPrompt": "You are a helpful assistant.",
    "maxTokens": 100
  }
}
```

**Response:**

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "result": {
    "role": "assistant",
    "content": {
      "type": "text",
      "text": "The capital of France is Paris."
    },
    "model": "claude-3-sonnet-20240307",
    "stopReason": "endTurn"
  }
}
```

## Message Flow

```mermaid
sequenceDiagram
    participant Server
    participant Client
    participant User
    participant LLM

    Note over Server,Client: Server initiates sampling
    Server->>Client: sampling/createMessage

    Note over Client,User: Human-in-the-loop review
    Client->>User: Present request for approval
    User-->>Client: Review and approve/modify

    Note over Client,LLM: Model interaction
    Client->>LLM: Forward approved request
    LLM-->>Client: Return generation

    Note over Client,User: Response review
    Client->>User: Present response for approval
    User-->>Client: Review and approve/modify

    Note over Server,Client: Complete request
    Client-->>Server: Return approved response
```

## Data Types

### Messages

Sampling messages can contain:

#### Text Content

```json
{
  "type": "text",
  "text": "The message content"
}
```

#### Image Content

```json
{
  "type": "image",
  "data": "base64-encoded-image-data",
  "mimeType": "image/jpeg"
}
```

#### Audio Content

```json
{
  "type": "audio",
  "data": "base64-encoded-audio-data",
  "mimeType": "audio/wav"
}
```

### Model Preferences

Model selection in MCP requires careful abstraction since servers and clients may use
different AI providers with distinct model offerings. A server cannot simply request a
specific model by name since the client may not have access to that exact model or may
prefer to use a different provider's equivalent model.

To solve this, MCP implements a preference system that combines abstract capability
priorities with optional model hints:

#### Capability Priorities

Servers express their needs through three normalized priority values (0-1):

- `costPriority`: How important is minimizing costs? Higher values prefer cheaper models.
- `speedPriority`: How important is low latency? Higher values prefer faster models.
- `intelligencePriority`: How important are advanced capabilities? Higher values prefer
  more capable models.

#### Model Hints

While priorities help select models based on characteristics, `hints` allow servers to
suggest specific models or model families:

- Hints are treated as substrings that can match model names flexibly
- Multiple hints are evaluated in order of preference
- Clients **MAY** map hints to equivalent models from different providers
- Hints are advisory—clients make final model selection

For example:

```json
{
  "hints": [
    { "name": "claude-3-sonnet" }, // Prefer Sonnet-class models
    { "name": "claude" } // Fall back to any Claude model
  ],
  "costPriority": 0.3, // Cost is less important
  "speedPriority": 0.8, // Speed is very important
  "intelligencePriority": 0.5 // Moderate capability needs
}
```

The client processes these preferences to select an appropriate model from its available
options. For instance, if the client doesn't have access to Claude models but has Gemini,
it might map the sonnet hint to `gemini-1.5-pro` based on similar capabilities.

## Error Handling

Clients **SHOULD** return errors for common failure cases:

Example error:

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "error": {
    "code": -1,
    "message": "User rejected sampling request"
  }
}
```

## Security Considerations

1. Clients **SHOULD** implement user approval controls
2. Both parties **SHOULD** validate message content
3. Clients **SHOULD** respect model preference hints
4. Clients **SHOULD** implement rate limiting
5. Both parties **MUST** handle sensitive data appropriately

```

mcp-streaming-http-go/docs/mcp/2025-06-18/schema.md:
```

# Schema Reference

<div id="schema-reference" />

## Common Types

### `Annotations`

<div class="tsd-signature"><span class="tsd-signature-keyword">interface</span> <span class="tsd-kind-interface">Annotations</span> <span class="tsd-signature-symbol">\{</span><br />  <a class="tsd-kind-property" href="#annotations-audience">audience</a><span class="tsd-signature-symbol">?:</span> <a href="#role" class="tsd-signature-type tsd-kind-type-alias">Role</a><span class="tsd-signature-symbol">\[]</span><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#annotations-lastmodified">lastModified</a><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#annotations-priority">priority</a><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">number</span><span class="tsd-signature-symbol">;</span><br /><span class="tsd-signature-symbol">}</span></div><div class="tsd-comment tsd-typography"><p>Optional annotations for the client. The client can use annotations to inform how objects are used or displayed</p> </div><section class="tsd-panel tsd-member"><div data-typedoc-h="3" class="tsd-anchor-link" id="annotations-audience"><code class="tsd-tag">Optional</code><span>audience</span><a href="#annotations-audience" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">audience</span><span class="tsd-signature-symbol">?:</span> <a href="#role" class="tsd-signature-type tsd-kind-type-alias">Role</a><span class="tsd-signature-symbol">\[]</span></div><div class="tsd-comment tsd-typography"><p>Describes who the intended customer of this object or data is.</p> <p>It can include multiple entries to indicate content useful for multiple audiences (e.g., <code>\["user", "assistant"]</code>).</p> </div><div class="tsd-comment tsd-typography" /></section><section class="tsd-panel tsd-member"><div data-typedoc-h="3" class="tsd-anchor-link" id="annotations-lastmodified"><code class="tsd-tag">Optional</code><span>last<wbr />Modified</span><a href="#annotations-lastmodified" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">lastModified</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">string</span></div><div class="tsd-comment tsd-typography"><p>The moment the resource was last modified, as an ISO 8601 formatted string.</p> <p>Should be an ISO 8601 formatted string (e.g., "2025-01-12T15:00:58Z").</p> <p>Examples: last activity timestamp in an open file, timestamp when the resource
was attached, etc.</p> </div><div class="tsd-comment tsd-typography" /></section><section class="tsd-panel tsd-member"><div data-typedoc-h="3" class="tsd-anchor-link" id="annotations-priority"><code class="tsd-tag">Optional</code><span>priority</span><a href="#annotations-priority" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">priority</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">number</span></div><div class="tsd-comment tsd-typography"><p>Describes how important this data is for operating the server.</p> <p>A value of 1 means "most important," and indicates that the data is
effectively required, while 0 means "least important," and indicates that
the data is entirely optional.</p> </div><div class="tsd-comment tsd-typography"><div class="tsd-tag-TJS-type"><div data-typedoc-h="4" class="tsd-anchor-link" id="tjs-type">TJS-type<a href="#tjs-type" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><p>number</p> </div></div></section>

### `AudioContent`

<div class="tsd-signature"><span class="tsd-signature-keyword">interface</span> <span class="tsd-kind-interface">AudioContent</span> <span class="tsd-signature-symbol">\{</span><br />  <a class="tsd-kind-property" href="#audiocontent-_meta">\_meta</a><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-symbol">\{</span> <span class="tsd-signature-symbol">\[</span><span class="tsd-kind-index-signature">key</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">]:</span> <span class="tsd-signature-type">unknown</span> <span class="tsd-signature-symbol">}</span><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#audiocontent-annotations">annotations</a><span class="tsd-signature-symbol">?:</span> <a href="#annotations" class="tsd-signature-type tsd-kind-interface">Annotations</a><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#audiocontent-data">data</a><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#audiocontent-mimetype">mimeType</a><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#">type</a><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">"audio"</span><span class="tsd-signature-symbol">;</span><br /><span class="tsd-signature-symbol">}</span></div><div class="tsd-comment tsd-typography"><p>Audio provided to or from an LLM.</p> </div><section class="tsd-panel tsd-member"><div data-typedoc-h="3" class="tsd-anchor-link" id="audiocontent-_meta"><code class="tsd-tag">Optional</code><span>\_<wbr />meta</span><a href="#audiocontent-_meta" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">\_meta</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-symbol">\{</span> <span class="tsd-signature-symbol">\[</span><span class="tsd-kind-index-signature">key</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">]:</span> <span class="tsd-signature-type">unknown</span> <span class="tsd-signature-symbol">}</span></div><div class="tsd-comment tsd-typography"><p>See <a href="/specification/2025-06-18/basic/index#meta">General fields: <code>\_meta</code></a> for notes on <code>\_meta</code> usage.</p> </div><div class="tsd-comment tsd-typography" /></section><section class="tsd-panel tsd-member"><div data-typedoc-h="3" class="tsd-anchor-link" id="audiocontent-annotations"><code class="tsd-tag">Optional</code><span>annotations</span><a href="#audiocontent-annotations" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">annotations</span><span class="tsd-signature-symbol">?:</span> <a href="#annotations" class="tsd-signature-type tsd-kind-interface">Annotations</a></div><div class="tsd-comment tsd-typography"><p>Optional annotations for the client.</p> </div><div class="tsd-comment tsd-typography" /></section><section class="tsd-panel tsd-member"><div data-typedoc-h="3" class="tsd-anchor-link" id="audiocontent-data"><span>data</span><a href="#audiocontent-data" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">data</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span></div><div class="tsd-comment tsd-typography"><p>The base64-encoded audio data.</p> </div><div class="tsd-comment tsd-typography" /></section><section class="tsd-panel tsd-member"><div data-typedoc-h="3" class="tsd-anchor-link" id="audiocontent-mimetype"><span>mime<wbr />Type</span><a href="#audiocontent-mimetype" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">mimeType</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span></div><div class="tsd-comment tsd-typography"><p>The MIME type of the audio. Different providers may support different audio types.</p> </div><div class="tsd-comment tsd-typography" /></section>

### `BlobResourceContents`

<div class="tsd-signature"><span class="tsd-signature-keyword">interface</span> <span class="tsd-kind-interface">BlobResourceContents</span> <span class="tsd-signature-symbol">\{</span><br />  <a class="tsd-kind-property" href="#blobresourcecontents-_meta">\_meta</a><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-symbol">\{</span> <span class="tsd-signature-symbol">\[</span><span class="tsd-kind-index-signature">key</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">]:</span> <span class="tsd-signature-type">unknown</span> <span class="tsd-signature-symbol">}</span><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#blobresourcecontents-blob">blob</a><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#blobresourcecontents-mimetype">mimeType</a><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#blobresourcecontents-uri">uri</a><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">;</span><br /><span class="tsd-signature-symbol">}</span></div><div class="tsd-comment tsd-typography"><p>The contents of a specific resource or sub-resource.</p> </div><section class="tsd-panel tsd-member tsd-is-inherited"><div data-typedoc-h="3" class="tsd-anchor-link" id="blobresourcecontents-_meta"><code class="tsd-tag">Optional</code><span>\_<wbr />meta</span><a href="#blobresourcecontents-_meta" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">\_meta</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-symbol">\{</span> <span class="tsd-signature-symbol">\[</span><span class="tsd-kind-index-signature">key</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">]:</span> <span class="tsd-signature-type">unknown</span> <span class="tsd-signature-symbol">}</span></div><div class="tsd-comment tsd-typography"><p>See <a href="/specification/2025-06-18/basic/index#meta">General fields: <code>\_meta</code></a> for notes on <code>\_meta</code> usage.</p> </div><div class="tsd-comment tsd-typography" /><aside class="tsd-sources"><p>Inherited from <a href="#resourcecontents">ResourceContents</a>.<a href="#resourcecontents-_meta">\_meta</a></p></aside></section><section class="tsd-panel tsd-member"><div data-typedoc-h="3" class="tsd-anchor-link" id="blobresourcecontents-blob"><span>blob</span><a href="#blobresourcecontents-blob" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">blob</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span></div><div class="tsd-comment tsd-typography"><p>A base64-encoded string representing the binary data of the item.</p> </div><div class="tsd-comment tsd-typography" /></section><section class="tsd-panel tsd-member tsd-is-inherited"><div data-typedoc-h="3" class="tsd-anchor-link" id="blobresourcecontents-mimetype"><code class="tsd-tag">Optional</code><span>mime<wbr />Type</span><a href="#blobresourcecontents-mimetype" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">mimeType</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">string</span></div><div class="tsd-comment tsd-typography"><p>The MIME type of this resource, if known.</p> </div><div class="tsd-comment tsd-typography" /><aside class="tsd-sources"><p>Inherited from <a href="#resourcecontents">ResourceContents</a>.<a href="#resourcecontents-mimetype">mimeType</a></p></aside></section><section class="tsd-panel tsd-member tsd-is-inherited"><div data-typedoc-h="3" class="tsd-anchor-link" id="blobresourcecontents-uri"><span>uri</span><a href="#blobresourcecontents-uri" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">uri</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span></div><div class="tsd-comment tsd-typography"><p>The URI of this resource.</p> </div><div class="tsd-comment tsd-typography" /><aside class="tsd-sources"><p>Inherited from <a href="#resourcecontents">ResourceContents</a>.<a href="#resourcecontents-uri">uri</a></p></aside></section>

### `BooleanSchema`

<div class="tsd-signature"><span class="tsd-signature-keyword">interface</span> <span class="tsd-kind-interface">BooleanSchema</span> <span class="tsd-signature-symbol">\{</span><br />  <a class="tsd-kind-property" href="#">default</a><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">boolean</span><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#">description</a><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#">title</a><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#">type</a><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">"boolean"</span><span class="tsd-signature-symbol">;</span><br /><span class="tsd-signature-symbol">}</span></div>

### `ClientCapabilities`

<div class="tsd-signature"><span class="tsd-signature-keyword">interface</span> <span class="tsd-kind-interface">ClientCapabilities</span> <span class="tsd-signature-symbol">\{</span><br />  <a class="tsd-kind-property" href="#clientcapabilities-elicitation">elicitation</a><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">object</span><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#clientcapabilities-experimental">experimental</a><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-symbol">\{</span> <span class="tsd-signature-symbol">\[</span><span class="tsd-kind-index-signature">key</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">]:</span> <span class="tsd-signature-type">object</span> <span class="tsd-signature-symbol">}</span><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#clientcapabilities-roots">roots</a><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-symbol">\{</span> <span class="tsd-kind-property">listChanged</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">boolean</span> <span class="tsd-signature-symbol">}</span><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#clientcapabilities-sampling">sampling</a><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">object</span><span class="tsd-signature-symbol">;</span><br /><span class="tsd-signature-symbol">}</span></div><div class="tsd-comment tsd-typography"><p>Capabilities a client may support. Known capabilities are defined here, in this schema, but this is not a closed set: any client can define its own, additional capabilities.</p> </div><section class="tsd-panel tsd-member"><div data-typedoc-h="3" class="tsd-anchor-link" id="clientcapabilities-elicitation"><code class="tsd-tag">Optional</code><span>elicitation</span><a href="#clientcapabilities-elicitation" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">elicitation</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">object</span></div><div class="tsd-comment tsd-typography"><p>Present if the client supports elicitation from the server.</p> </div><div class="tsd-comment tsd-typography" /></section><section class="tsd-panel tsd-member"><div data-typedoc-h="3" class="tsd-anchor-link" id="clientcapabilities-experimental"><code class="tsd-tag">Optional</code><span>experimental</span><a href="#clientcapabilities-experimental" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">experimental</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-symbol">\{</span> <span class="tsd-signature-symbol">\[</span><span class="tsd-kind-index-signature">key</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">]:</span> <span class="tsd-signature-type">object</span> <span class="tsd-signature-symbol">}</span></div><div class="tsd-comment tsd-typography"><p>Experimental, non-standard capabilities that the client supports.</p> </div><div class="tsd-comment tsd-typography" /></section><section class="tsd-panel tsd-member"><div data-typedoc-h="3" class="tsd-anchor-link" id="clientcapabilities-roots"><code class="tsd-tag">Optional</code><span>roots</span><a href="#clientcapabilities-roots" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">roots</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-symbol">\{</span> <span class="tsd-kind-property">listChanged</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">boolean</span> <span class="tsd-signature-symbol">}</span></div><div class="tsd-comment tsd-typography"><p>Present if the client supports listing roots.</p> </div><div class="tsd-type-declaration"><div data-typedoc-h="4">Type declaration</div><ul class="tsd-parameters"><li class="tsd-parameter"><div data-typedoc-h="5"><code class="tsd-tag">Optional</code><span class="tsd-kind-property">listChanged</span><span class="tsd-signature-symbol">?: </span><span class="tsd-signature-type">boolean</span></div><div class="tsd-comment tsd-typography"><p>Whether the client supports notifications for changes to the roots list.</p> </div><div class="tsd-comment tsd-typography" /></li></ul></div><div class="tsd-comment tsd-typography" /></section><section class="tsd-panel tsd-member"><div data-typedoc-h="3" class="tsd-anchor-link" id="clientcapabilities-sampling"><code class="tsd-tag">Optional</code><span>sampling</span><a href="#clientcapabilities-sampling" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">sampling</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">object</span></div><div class="tsd-comment tsd-typography"><p>Present if the client supports sampling from an LLM.</p> </div><div class="tsd-comment tsd-typography" /></section>

### `ContentBlock`

<div class="tsd-signature"><span class="tsd-kind-type-alias">ContentBlock</span><span class="tsd-signature-symbol">:</span><br />  <span class="tsd-signature-symbol">|</span> <a href="#textcontent" class="tsd-signature-type tsd-kind-interface">TextContent</a><br />  <span class="tsd-signature-symbol">|</span> <a href="#imagecontent" class="tsd-signature-type tsd-kind-interface">ImageContent</a><br />  <span class="tsd-signature-symbol">|</span> <a href="#audiocontent" class="tsd-signature-type tsd-kind-interface">AudioContent</a><br />  <span class="tsd-signature-symbol">|</span> <a href="#resourcelink" class="tsd-signature-type tsd-kind-interface">ResourceLink</a><br />  <span class="tsd-signature-symbol">|</span> <a href="#embeddedresource" class="tsd-signature-type tsd-kind-interface">EmbeddedResource</a></div>

### `Cursor`

<div class="tsd-signature"><span class="tsd-kind-type-alias">Cursor</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span></div><div class="tsd-comment tsd-typography"><p>An opaque token used to represent a cursor for pagination.</p> </div><div class="tsd-comment tsd-typography" />

### `EmbeddedResource`

<div class="tsd-signature"><span class="tsd-signature-keyword">interface</span> <span class="tsd-kind-interface">EmbeddedResource</span> <span class="tsd-signature-symbol">\{</span><br />  <a class="tsd-kind-property" href="#embeddedresource-_meta">\_meta</a><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-symbol">\{</span> <span class="tsd-signature-symbol">\[</span><span class="tsd-kind-index-signature">key</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">]:</span> <span class="tsd-signature-type">unknown</span> <span class="tsd-signature-symbol">}</span><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#embeddedresource-annotations">annotations</a><span class="tsd-signature-symbol">?:</span> <a href="#annotations" class="tsd-signature-type tsd-kind-interface">Annotations</a><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#">resource</a><span class="tsd-signature-symbol">:</span> <a href="#textresourcecontents" class="tsd-signature-type tsd-kind-interface">TextResourceContents</a> <span class="tsd-signature-symbol">|</span> <a href="#blobresourcecontents" class="tsd-signature-type tsd-kind-interface">BlobResourceContents</a><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#">type</a><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">"resource"</span><span class="tsd-signature-symbol">;</span><br /><span class="tsd-signature-symbol">}</span></div><div class="tsd-comment tsd-typography"><p>The contents of a resource, embedded into a prompt or tool call result.</p> <p>It is up to the client how best to render embedded resources for the benefit
of the LLM and/or the user.</p> </div><section class="tsd-panel tsd-member"><div data-typedoc-h="3" class="tsd-anchor-link" id="embeddedresource-_meta"><code class="tsd-tag">Optional</code><span>\_<wbr />meta</span><a href="#embeddedresource-_meta" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">\_meta</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-symbol">\{</span> <span class="tsd-signature-symbol">\[</span><span class="tsd-kind-index-signature">key</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">]:</span> <span class="tsd-signature-type">unknown</span> <span class="tsd-signature-symbol">}</span></div><div class="tsd-comment tsd-typography"><p>See <a href="/specification/2025-06-18/basic/index#meta">General fields: <code>\_meta</code></a> for notes on <code>\_meta</code> usage.</p> </div><div class="tsd-comment tsd-typography" /></section><section class="tsd-panel tsd-member"><div data-typedoc-h="3" class="tsd-anchor-link" id="embeddedresource-annotations"><code class="tsd-tag">Optional</code><span>annotations</span><a href="#embeddedresource-annotations" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">annotations</span><span class="tsd-signature-symbol">?:</span> <a href="#annotations" class="tsd-signature-type tsd-kind-interface">Annotations</a></div><div class="tsd-comment tsd-typography"><p>Optional annotations for the client.</p> </div><div class="tsd-comment tsd-typography" /></section>

### `EmptyResult`

<div class="tsd-signature"><span class="tsd-kind-type-alias">EmptyResult</span><span class="tsd-signature-symbol">:</span> <a href="#result" class="tsd-signature-type tsd-kind-interface">Result</a></div><div class="tsd-comment tsd-typography"><p>A response that indicates success but carries no data.</p> </div><div class="tsd-comment tsd-typography" />

### `EnumSchema`

<div class="tsd-signature"><span class="tsd-signature-keyword">interface</span> <span class="tsd-kind-interface">EnumSchema</span> <span class="tsd-signature-symbol">\{</span><br />  <a class="tsd-kind-property" href="#">description</a><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#">enum</a><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">\[]</span><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#">enumNames</a><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">\[]</span><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#">title</a><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#">type</a><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">"string"</span><span class="tsd-signature-symbol">;</span><br /><span class="tsd-signature-symbol">}</span></div>

### `ImageContent`

<div class="tsd-signature"><span class="tsd-signature-keyword">interface</span> <span class="tsd-kind-interface">ImageContent</span> <span class="tsd-signature-symbol">\{</span><br />  <a class="tsd-kind-property" href="#imagecontent-_meta">\_meta</a><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-symbol">\{</span> <span class="tsd-signature-symbol">\[</span><span class="tsd-kind-index-signature">key</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">]:</span> <span class="tsd-signature-type">unknown</span> <span class="tsd-signature-symbol">}</span><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#imagecontent-annotations">annotations</a><span class="tsd-signature-symbol">?:</span> <a href="#annotations" class="tsd-signature-type tsd-kind-interface">Annotations</a><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#imagecontent-data">data</a><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#imagecontent-mimetype">mimeType</a><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#">type</a><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">"image"</span><span class="tsd-signature-symbol">;</span><br /><span class="tsd-signature-symbol">}</span></div><div class="tsd-comment tsd-typography"><p>An image provided to or from an LLM.</p> </div><section class="tsd-panel tsd-member"><div data-typedoc-h="3" class="tsd-anchor-link" id="imagecontent-_meta"><code class="tsd-tag">Optional</code><span>\_<wbr />meta</span><a href="#imagecontent-_meta" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">\_meta</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-symbol">\{</span> <span class="tsd-signature-symbol">\[</span><span class="tsd-kind-index-signature">key</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">]:</span> <span class="tsd-signature-type">unknown</span> <span class="tsd-signature-symbol">}</span></div><div class="tsd-comment tsd-typography"><p>See <a href="/specification/2025-06-18/basic/index#meta">General fields: <code>\_meta</code></a> for notes on <code>\_meta</code> usage.</p> </div><div class="tsd-comment tsd-typography" /></section><section class="tsd-panel tsd-member"><div data-typedoc-h="3" class="tsd-anchor-link" id="imagecontent-annotations"><code class="tsd-tag">Optional</code><span>annotations</span><a href="#imagecontent-annotations" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">annotations</span><span class="tsd-signature-symbol">?:</span> <a href="#annotations" class="tsd-signature-type tsd-kind-interface">Annotations</a></div><div class="tsd-comment tsd-typography"><p>Optional annotations for the client.</p> </div><div class="tsd-comment tsd-typography" /></section><section class="tsd-panel tsd-member"><div data-typedoc-h="3" class="tsd-anchor-link" id="imagecontent-data"><span>data</span><a href="#imagecontent-data" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">data</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span></div><div class="tsd-comment tsd-typography"><p>The base64-encoded image data.</p> </div><div class="tsd-comment tsd-typography" /></section><section class="tsd-panel tsd-member"><div data-typedoc-h="3" class="tsd-anchor-link" id="imagecontent-mimetype"><span>mime<wbr />Type</span><a href="#imagecontent-mimetype" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">mimeType</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span></div><div class="tsd-comment tsd-typography"><p>The MIME type of the image. Different providers may support different image types.</p> </div><div class="tsd-comment tsd-typography" /></section>

### `Implementation`

<div class="tsd-signature"><span class="tsd-signature-keyword">interface</span> <span class="tsd-kind-interface">Implementation</span> <span class="tsd-signature-symbol">\{</span><br />  <a class="tsd-kind-property" href="#implementation-name">name</a><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#implementation-title">title</a><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#">version</a><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">;</span><br /><span class="tsd-signature-symbol">}</span></div><div class="tsd-comment tsd-typography"><p>Describes the name and version of an MCP implementation, with an optional title for UI representation.</p> </div><section class="tsd-panel tsd-member tsd-is-inherited"><div data-typedoc-h="3" class="tsd-anchor-link" id="implementation-name"><span>name</span><a href="#implementation-name" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">name</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span></div><div class="tsd-comment tsd-typography"><p>Intended for programmatic or logical use, but used as a display name in past specs or fallback (if title isn't present).</p> </div><div class="tsd-comment tsd-typography" /><aside class="tsd-sources"><p>Inherited from BaseMetadata.name</p></aside></section><section class="tsd-panel tsd-member tsd-is-inherited"><div data-typedoc-h="3" class="tsd-anchor-link" id="implementation-title"><code class="tsd-tag">Optional</code><span>title</span><a href="#implementation-title" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">title</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">string</span></div><div class="tsd-comment tsd-typography"><p>Intended for UI and end-user contexts — optimized to be human-readable and easily understood,
even by those unfamiliar with domain-specific terminology.</p> <p>If not provided, the name should be used for display (except for Tool,
where <code>annotations.title</code> should be given precedence over using <code>name</code>,
if present).</p> </div><div class="tsd-comment tsd-typography" /><aside class="tsd-sources"><p>Inherited from BaseMetadata.title</p></aside></section>

### `JSONRPCError`

<div class="tsd-signature"><span class="tsd-signature-keyword">interface</span> <span class="tsd-kind-interface">JSONRPCError</span> <span class="tsd-signature-symbol">\{</span><br />  <a class="tsd-kind-property" href="#jsonrpcerror-error">error</a><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-symbol">\{</span> <span class="tsd-kind-property">code</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">number</span><span class="tsd-signature-symbol">;</span> <span class="tsd-kind-property">data</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">unknown</span><span class="tsd-signature-symbol">;</span> <span class="tsd-kind-property">message</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span> <span class="tsd-signature-symbol">}</span><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#">id</a><span class="tsd-signature-symbol">:</span> <a href="#requestid" class="tsd-signature-type tsd-kind-type-alias">RequestId</a><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#">jsonrpc</a><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">"2.0"</span><span class="tsd-signature-symbol">;</span><br /><span class="tsd-signature-symbol">}</span></div><div class="tsd-comment tsd-typography"><p>A response to a request that indicates an error occurred.</p> </div><section class="tsd-panel tsd-member"><div data-typedoc-h="3" class="tsd-anchor-link" id="jsonrpcerror-error"><span>error</span><a href="#jsonrpcerror-error" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">error</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-symbol">\{</span> <span class="tsd-kind-property">code</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">number</span><span class="tsd-signature-symbol">;</span> <span class="tsd-kind-property">data</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">unknown</span><span class="tsd-signature-symbol">;</span> <span class="tsd-kind-property">message</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span> <span class="tsd-signature-symbol">}</span></div><div class="tsd-type-declaration"><div data-typedoc-h="4">Type declaration</div><ul class="tsd-parameters"><li class="tsd-parameter"><div data-typedoc-h="5"><span class="tsd-kind-property">code</span><span class="tsd-signature-symbol">: </span><span class="tsd-signature-type">number</span></div><div class="tsd-comment tsd-typography"><p>The error type that occurred.</p> </div><div class="tsd-comment tsd-typography" /></li><li class="tsd-parameter"><div data-typedoc-h="5"><code class="tsd-tag">Optional</code><span class="tsd-kind-property">data</span><span class="tsd-signature-symbol">?: </span><span class="tsd-signature-type">unknown</span></div><div class="tsd-comment tsd-typography"><p>Additional information about the error. The value of this member is defined by the sender (e.g. detailed error information, nested errors etc.).</p> </div><div class="tsd-comment tsd-typography" /></li><li class="tsd-parameter"><div data-typedoc-h="5"><span class="tsd-kind-property">message</span><span class="tsd-signature-symbol">: </span><span class="tsd-signature-type">string</span></div><div class="tsd-comment tsd-typography"><p>A short description of the error. The message SHOULD be limited to a concise single sentence.</p> </div><div class="tsd-comment tsd-typography" /></li></ul></div></section>

### `JSONRPCNotification`

<div class="tsd-signature"><span class="tsd-signature-keyword">interface</span> <span class="tsd-kind-interface">JSONRPCNotification</span> <span class="tsd-signature-symbol">\{</span><br />  <a class="tsd-kind-property" href="#">jsonrpc</a><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">"2.0"</span><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#">method</a><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#jsonrpcnotification-params">params</a><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-symbol">\{</span> <span class="tsd-kind-property">\_meta</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-symbol">\{</span> <span class="tsd-signature-symbol">\[</span><span class="tsd-kind-index-signature">key</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">]:</span> <span class="tsd-signature-type">unknown</span> <span class="tsd-signature-symbol">}</span><span class="tsd-signature-symbol">;</span> <span class="tsd-signature-symbol">\[</span><span class="tsd-kind-index-signature">key</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">]:</span> <span class="tsd-signature-type">unknown</span> <span class="tsd-signature-symbol">}</span><span class="tsd-signature-symbol">;</span><br /><span class="tsd-signature-symbol">}</span></div><div class="tsd-comment tsd-typography"><p>A notification which does not expect a response.</p> </div><section class="tsd-panel tsd-member tsd-is-inherited"><div data-typedoc-h="3" class="tsd-anchor-link" id="jsonrpcnotification-params"><code class="tsd-tag">Optional</code><span>params</span><a href="#jsonrpcnotification-params" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">params</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-symbol">\{</span> <span class="tsd-kind-property">\_meta</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-symbol">\{</span> <span class="tsd-signature-symbol">\[</span><span class="tsd-kind-index-signature">key</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">]:</span> <span class="tsd-signature-type">unknown</span> <span class="tsd-signature-symbol">}</span><span class="tsd-signature-symbol">;</span> <span class="tsd-signature-symbol">\[</span><span class="tsd-kind-index-signature">key</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">]:</span> <span class="tsd-signature-type">unknown</span> <span class="tsd-signature-symbol">}</span></div><div class="tsd-type-declaration"><div data-typedoc-h="4">Type declaration</div><ul class="tsd-parameters"><li class="tsd-parameter-index-signature"><div data-typedoc-h="5"><span class="tsd-signature-symbol">\[</span><span class="tsd-kind-parameter">key</span>: <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">]:</span> <span class="tsd-signature-type">unknown</span></div></li><li class="tsd-parameter"><div data-typedoc-h="5"><code class="tsd-tag">Optional</code><span class="tsd-kind-property">\_meta</span><span class="tsd-signature-symbol">?: </span><span class="tsd-signature-symbol">\{</span> <span class="tsd-signature-symbol">\[</span><span class="tsd-kind-index-signature">key</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">]:</span> <span class="tsd-signature-type">unknown</span> <span class="tsd-signature-symbol">}</span></div><div class="tsd-comment tsd-typography"><p>See <a href="/specification/2025-06-18/basic/index#meta">General fields: <code>\_meta</code></a> for notes on <code>\_meta</code> usage.</p> </div><div class="tsd-comment tsd-typography" /></li></ul></div><aside class="tsd-sources"><p>Inherited from Notification.params</p></aside></section>

### `JSONRPCRequest`

<div class="tsd-signature"><span class="tsd-signature-keyword">interface</span> <span class="tsd-kind-interface">JSONRPCRequest</span> <span class="tsd-signature-symbol">\{</span><br />  <a class="tsd-kind-property" href="#">id</a><span class="tsd-signature-symbol">:</span> <a href="#requestid" class="tsd-signature-type tsd-kind-type-alias">RequestId</a><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#">jsonrpc</a><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">"2.0"</span><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#">method</a><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#jsonrpcrequest-params">params</a><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-symbol">\{</span><br />    <span class="tsd-kind-property">\_meta</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-symbol">\{</span> <span class="tsd-kind-property">progressToken</span><span class="tsd-signature-symbol">?:</span> <a href="#progresstoken" class="tsd-signature-type tsd-kind-type-alias">ProgressToken</a><span class="tsd-signature-symbol">;</span> <span class="tsd-signature-symbol">\[</span><span class="tsd-kind-index-signature">key</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">]:</span> <span class="tsd-signature-type">unknown</span> <span class="tsd-signature-symbol">}</span><span class="tsd-signature-symbol">;</span><br />    <span class="tsd-signature-symbol">\[</span><span class="tsd-kind-index-signature">key</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">]:</span> <span class="tsd-signature-type">unknown</span><span class="tsd-signature-symbol">;</span><br />  <span class="tsd-signature-symbol">}</span><span class="tsd-signature-symbol">;</span><br /><span class="tsd-signature-symbol">}</span></div><div class="tsd-comment tsd-typography"><p>A request that expects a response.</p> </div><section class="tsd-panel tsd-member tsd-is-inherited"><div data-typedoc-h="3" class="tsd-anchor-link" id="jsonrpcrequest-params"><code class="tsd-tag">Optional</code><span>params</span><a href="#jsonrpcrequest-params" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">params</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-symbol">\{</span><br />  <span class="tsd-kind-property">\_meta</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-symbol">\{</span> <span class="tsd-kind-property">progressToken</span><span class="tsd-signature-symbol">?:</span> <a href="#progresstoken" class="tsd-signature-type tsd-kind-type-alias">ProgressToken</a><span class="tsd-signature-symbol">;</span> <span class="tsd-signature-symbol">\[</span><span class="tsd-kind-index-signature">key</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">]:</span> <span class="tsd-signature-type">unknown</span> <span class="tsd-signature-symbol">}</span><span class="tsd-signature-symbol">;</span><br />  <span class="tsd-signature-symbol">\[</span><span class="tsd-kind-index-signature">key</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">]:</span> <span class="tsd-signature-type">unknown</span><span class="tsd-signature-symbol">;</span><br /><span class="tsd-signature-symbol">}</span></div><div class="tsd-type-declaration"><div data-typedoc-h="4">Type declaration</div><ul class="tsd-parameters"><li class="tsd-parameter-index-signature"><div data-typedoc-h="5"><span class="tsd-signature-symbol">\[</span><span class="tsd-kind-parameter">key</span>: <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">]:</span> <span class="tsd-signature-type">unknown</span></div></li><li class="tsd-parameter"><div data-typedoc-h="5"><code class="tsd-tag">Optional</code><span class="tsd-kind-property">\_meta</span><span class="tsd-signature-symbol">?: </span><span class="tsd-signature-symbol">\{</span> <span class="tsd-kind-property">progressToken</span><span class="tsd-signature-symbol">?:</span> <a href="#progresstoken" class="tsd-signature-type tsd-kind-type-alias">ProgressToken</a><span class="tsd-signature-symbol">;</span> <span class="tsd-signature-symbol">\[</span><span class="tsd-kind-index-signature">key</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">]:</span> <span class="tsd-signature-type">unknown</span> <span class="tsd-signature-symbol">}</span></div><div class="tsd-comment tsd-typography"><p>See <a href="/specification/2025-06-18/basic/index#meta">General fields: <code>\_meta</code></a> for notes on <code>\_meta</code> usage.</p> </div><div class="tsd-comment tsd-typography" /><ul class="tsd-parameters"><li class="tsd-parameter"><div data-typedoc-h="5"><code class="tsd-tag">Optional</code><span class="tsd-kind-property">progressToken</span><span class="tsd-signature-symbol">?: </span><a href="#progresstoken" class="tsd-signature-type tsd-kind-type-alias">ProgressToken</a></div><div class="tsd-comment tsd-typography"><p>If specified, the caller is requesting out-of-band progress notifications for this request (as represented by notifications/progress). The value of this parameter is an opaque token that will be attached to any subsequent notifications. The receiver is not obligated to provide these notifications.</p> </div><div class="tsd-comment tsd-typography" /></li></ul></li></ul></div><aside class="tsd-sources"><p>Inherited from Request.params</p></aside></section>

### `JSONRPCResponse`

<div class="tsd-signature"><span class="tsd-signature-keyword">interface</span> <span class="tsd-kind-interface">JSONRPCResponse</span> <span class="tsd-signature-symbol">\{</span><br />  <a class="tsd-kind-property" href="#">id</a><span class="tsd-signature-symbol">:</span> <a href="#requestid" class="tsd-signature-type tsd-kind-type-alias">RequestId</a><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#">jsonrpc</a><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">"2.0"</span><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#">result</a><span class="tsd-signature-symbol">:</span> <a href="#result" class="tsd-signature-type tsd-kind-interface">Result</a><span class="tsd-signature-symbol">;</span><br /><span class="tsd-signature-symbol">}</span></div><div class="tsd-comment tsd-typography"><p>A successful (non-error) response to a request.</p> </div>

### `LoggingLevel`

<div class="tsd-signature"><span class="tsd-kind-type-alias">LoggingLevel</span><span class="tsd-signature-symbol">:</span><br />  <span class="tsd-signature-symbol">|</span> <span class="tsd-signature-type">"debug"</span><br />  <span class="tsd-signature-symbol">|</span> <span class="tsd-signature-type">"info"</span><br />  <span class="tsd-signature-symbol">|</span> <span class="tsd-signature-type">"notice"</span><br />  <span class="tsd-signature-symbol">|</span> <span class="tsd-signature-type">"warning"</span><br />  <span class="tsd-signature-symbol">|</span> <span class="tsd-signature-type">"error"</span><br />  <span class="tsd-signature-symbol">|</span> <span class="tsd-signature-type">"critical"</span><br />  <span class="tsd-signature-symbol">|</span> <span class="tsd-signature-type">"alert"</span><br />  <span class="tsd-signature-symbol">|</span> <span class="tsd-signature-type">"emergency"</span></div><div class="tsd-comment tsd-typography"><p>The severity of a log message.</p> <p>These map to syslog message severities, as specified in RFC-5424: <a href="https://datatracker.ietf.org/doc/html/rfc5424#section-6.2.1">[https://datatracker.ietf.org/doc/html/rfc5424#section-6.2.1](https://datatracker.ietf.org/doc/html/rfc5424#section-6.2.1)</a></p> </div><div class="tsd-comment tsd-typography" />

### `ModelHint`

<div class="tsd-signature"><span class="tsd-signature-keyword">interface</span> <span class="tsd-kind-interface">ModelHint</span> <span class="tsd-signature-symbol">\{</span><br />  <a class="tsd-kind-property" href="#modelhint-name">name</a><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">;</span><br /><span class="tsd-signature-symbol">}</span></div><div class="tsd-comment tsd-typography"><p>Hints to use for model selection.</p> <p>Keys not declared here are currently left unspecified by the spec and are up
to the client to interpret.</p> </div><section class="tsd-panel tsd-member"><div data-typedoc-h="3" class="tsd-anchor-link" id="modelhint-name"><code class="tsd-tag">Optional</code><span>name</span><a href="#modelhint-name" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">name</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">string</span></div><div class="tsd-comment tsd-typography"><p>A hint for a model name.</p> <p>The client SHOULD treat this as a substring of a model name; for example:</p> <ul> <li><code>claude-3-5-sonnet</code> should match <code>claude-3-5-sonnet-20241022</code></li> <li><code>sonnet</code> should match <code>claude-3-5-sonnet-20241022</code>, <code>claude-3-sonnet-20240229</code>, etc.</li> <li><code>claude</code> should match any Claude model</li> </ul> <p>The client MAY also map the string to a different provider's model name or a different model family, as long as it fills a similar niche; for example:</p> <ul> <li><code>gemini-1.5-flash</code> could match <code>claude-3-haiku-20240307</code></li> </ul> </div><div class="tsd-comment tsd-typography" /></section>

### `ModelPreferences`

<div class="tsd-signature"><span class="tsd-signature-keyword">interface</span> <span class="tsd-kind-interface">ModelPreferences</span> <span class="tsd-signature-symbol">\{</span><br />  <a class="tsd-kind-property" href="#modelpreferences-costpriority">costPriority</a><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">number</span><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#modelpreferences-hints">hints</a><span class="tsd-signature-symbol">?:</span> <a href="#modelhint" class="tsd-signature-type tsd-kind-interface">ModelHint</a><span class="tsd-signature-symbol">\[]</span><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#modelpreferences-intelligencepriority">intelligencePriority</a><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">number</span><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#modelpreferences-speedpriority">speedPriority</a><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">number</span><span class="tsd-signature-symbol">;</span><br /><span class="tsd-signature-symbol">}</span></div><div class="tsd-comment tsd-typography"><p>The server's preferences for model selection, requested of the client during sampling.</p> <p>Because LLMs can vary along multiple dimensions, choosing the "best" model is
rarely straightforward.  Different models excel in different areas—some are
faster but less capable, others are more capable but more expensive, and so
on. This interface allows servers to express their priorities across multiple
dimensions to help clients make an appropriate selection for their use case.</p> <p>These preferences are always advisory. The client MAY ignore them. It is also
up to the client to decide how to interpret these preferences and how to
balance them against other considerations.</p> </div><section class="tsd-panel tsd-member"><div data-typedoc-h="3" class="tsd-anchor-link" id="modelpreferences-costpriority"><code class="tsd-tag">Optional</code><span>cost<wbr />Priority</span><a href="#modelpreferences-costpriority" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">costPriority</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">number</span></div><div class="tsd-comment tsd-typography"><p>How much to prioritize cost when selecting a model. A value of 0 means cost
is not important, while a value of 1 means cost is the most important
factor.</p> </div><div class="tsd-comment tsd-typography"><div class="tsd-tag-TJS-type"><div data-typedoc-h="4" class="tsd-anchor-link" id="tjs-type">TJS-type<a href="#tjs-type" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><p>number</p> </div></div></section><section class="tsd-panel tsd-member"><div data-typedoc-h="3" class="tsd-anchor-link" id="modelpreferences-hints"><code class="tsd-tag">Optional</code><span>hints</span><a href="#modelpreferences-hints" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">hints</span><span class="tsd-signature-symbol">?:</span> <a href="#modelhint" class="tsd-signature-type tsd-kind-interface">ModelHint</a><span class="tsd-signature-symbol">\[]</span></div><div class="tsd-comment tsd-typography"><p>Optional hints to use for model selection.</p> <p>If multiple hints are specified, the client MUST evaluate them in order
(such that the first match is taken).</p> <p>The client SHOULD prioritize these hints over the numeric priorities, but
MAY still use the priorities to select from ambiguous matches.</p> </div><div class="tsd-comment tsd-typography" /></section><section class="tsd-panel tsd-member"><div data-typedoc-h="3" class="tsd-anchor-link" id="modelpreferences-intelligencepriority"><code class="tsd-tag">Optional</code><span>intelligence<wbr />Priority</span><a href="#modelpreferences-intelligencepriority" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">intelligencePriority</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">number</span></div><div class="tsd-comment tsd-typography"><p>How much to prioritize intelligence and capabilities when selecting a
model. A value of 0 means intelligence is not important, while a value of 1
means intelligence is the most important factor.</p> </div><div class="tsd-comment tsd-typography"><div class="tsd-tag-TJS-type"><div data-typedoc-h="4" class="tsd-anchor-link" id="tjs-type-1">TJS-type<a href="#tjs-type-1" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><p>number</p> </div></div></section><section class="tsd-panel tsd-member"><div data-typedoc-h="3" class="tsd-anchor-link" id="modelpreferences-speedpriority"><code class="tsd-tag">Optional</code><span>speed<wbr />Priority</span><a href="#modelpreferences-speedpriority" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">speedPriority</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">number</span></div><div class="tsd-comment tsd-typography"><p>How much to prioritize sampling speed (latency) when selecting a model. A
value of 0 means speed is not important, while a value of 1 means speed is
the most important factor.</p> </div><div class="tsd-comment tsd-typography"><div class="tsd-tag-TJS-type"><div data-typedoc-h="4" class="tsd-anchor-link" id="tjs-type-2">TJS-type<a href="#tjs-type-2" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><p>number</p> </div></div></section>

### `NumberSchema`

<div class="tsd-signature"><span class="tsd-signature-keyword">interface</span> <span class="tsd-kind-interface">NumberSchema</span> <span class="tsd-signature-symbol">\{</span><br />  <a class="tsd-kind-property" href="#">description</a><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#">maximum</a><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">number</span><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#">minimum</a><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">number</span><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#">title</a><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#">type</a><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">"number"</span> <span class="tsd-signature-symbol">|</span> <span class="tsd-signature-type">"integer"</span><span class="tsd-signature-symbol">;</span><br /><span class="tsd-signature-symbol">}</span></div>

### `PrimitiveSchemaDefinition`

<div class="tsd-signature"><span class="tsd-kind-type-alias">PrimitiveSchemaDefinition</span><span class="tsd-signature-symbol">:</span><br />  <span class="tsd-signature-symbol">|</span> <a href="#stringschema" class="tsd-signature-type tsd-kind-interface">StringSchema</a><br />  <span class="tsd-signature-symbol">|</span> <a href="#numberschema" class="tsd-signature-type tsd-kind-interface">NumberSchema</a><br />  <span class="tsd-signature-symbol">|</span> <a href="#booleanschema" class="tsd-signature-type tsd-kind-interface">BooleanSchema</a><br />  <span class="tsd-signature-symbol">|</span> <a href="#enumschema" class="tsd-signature-type tsd-kind-interface">EnumSchema</a></div><div class="tsd-comment tsd-typography"><p>Restricted schema definitions that only allow primitive types
without nested objects or arrays.</p> </div><div class="tsd-comment tsd-typography" />

### `ProgressToken`

<div class="tsd-signature"><span class="tsd-kind-type-alias">ProgressToken</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span> <span class="tsd-signature-symbol">|</span> <span class="tsd-signature-type">number</span></div><div class="tsd-comment tsd-typography"><p>A progress token, used to associate progress notifications with the original request.</p> </div><div class="tsd-comment tsd-typography" />

### `Prompt`

<div class="tsd-signature"><span class="tsd-signature-keyword">interface</span> <span class="tsd-kind-interface">Prompt</span> <span class="tsd-signature-symbol">\{</span><br />  <a class="tsd-kind-property" href="#prompt-_meta">\_meta</a><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-symbol">\{</span> <span class="tsd-signature-symbol">\[</span><span class="tsd-kind-index-signature">key</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">]:</span> <span class="tsd-signature-type">unknown</span> <span class="tsd-signature-symbol">}</span><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#prompt-arguments">arguments</a><span class="tsd-signature-symbol">?:</span> <a href="#promptargument" class="tsd-signature-type tsd-kind-interface">PromptArgument</a><span class="tsd-signature-symbol">\[]</span><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#prompt-description">description</a><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#prompt-name">name</a><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#prompt-title">title</a><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">;</span><br /><span class="tsd-signature-symbol">}</span></div><div class="tsd-comment tsd-typography"><p>A prompt or prompt template that the server offers.</p> </div><section class="tsd-panel tsd-member"><div data-typedoc-h="3" class="tsd-anchor-link" id="prompt-_meta"><code class="tsd-tag">Optional</code><span>\_<wbr />meta</span><a href="#prompt-_meta" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">\_meta</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-symbol">\{</span> <span class="tsd-signature-symbol">\[</span><span class="tsd-kind-index-signature">key</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">]:</span> <span class="tsd-signature-type">unknown</span> <span class="tsd-signature-symbol">}</span></div><div class="tsd-comment tsd-typography"><p>See <a href="/specification/2025-06-18/basic/index#meta">General fields: <code>\_meta</code></a> for notes on <code>\_meta</code> usage.</p> </div><div class="tsd-comment tsd-typography" /></section><section class="tsd-panel tsd-member"><div data-typedoc-h="3" class="tsd-anchor-link" id="prompt-arguments"><code class="tsd-tag">Optional</code><span>arguments</span><a href="#prompt-arguments" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">arguments</span><span class="tsd-signature-symbol">?:</span> <a href="#promptargument" class="tsd-signature-type tsd-kind-interface">PromptArgument</a><span class="tsd-signature-symbol">\[]</span></div><div class="tsd-comment tsd-typography"><p>A list of arguments to use for templating the prompt.</p> </div><div class="tsd-comment tsd-typography" /></section><section class="tsd-panel tsd-member"><div data-typedoc-h="3" class="tsd-anchor-link" id="prompt-description"><code class="tsd-tag">Optional</code><span>description</span><a href="#prompt-description" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">description</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">string</span></div><div class="tsd-comment tsd-typography"><p>An optional description of what this prompt provides</p> </div><div class="tsd-comment tsd-typography" /></section><section class="tsd-panel tsd-member tsd-is-inherited"><div data-typedoc-h="3" class="tsd-anchor-link" id="prompt-name"><span>name</span><a href="#prompt-name" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">name</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span></div><div class="tsd-comment tsd-typography"><p>Intended for programmatic or logical use, but used as a display name in past specs or fallback (if title isn't present).</p> </div><div class="tsd-comment tsd-typography" /><aside class="tsd-sources"><p>Inherited from BaseMetadata.name</p></aside></section><section class="tsd-panel tsd-member tsd-is-inherited"><div data-typedoc-h="3" class="tsd-anchor-link" id="prompt-title"><code class="tsd-tag">Optional</code><span>title</span><a href="#prompt-title" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">title</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">string</span></div><div class="tsd-comment tsd-typography"><p>Intended for UI and end-user contexts — optimized to be human-readable and easily understood,
even by those unfamiliar with domain-specific terminology.</p> <p>If not provided, the name should be used for display (except for Tool,
where <code>annotations.title</code> should be given precedence over using <code>name</code>,
if present).</p> </div><div class="tsd-comment tsd-typography" /><aside class="tsd-sources"><p>Inherited from BaseMetadata.title</p></aside></section>

### `PromptArgument`

<div class="tsd-signature"><span class="tsd-signature-keyword">interface</span> <span class="tsd-kind-interface">PromptArgument</span> <span class="tsd-signature-symbol">\{</span><br />  <a class="tsd-kind-property" href="#promptargument-description">description</a><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#promptargument-name">name</a><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#promptargument-required">required</a><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">boolean</span><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#promptargument-title">title</a><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">;</span><br /><span class="tsd-signature-symbol">}</span></div><div class="tsd-comment tsd-typography"><p>Describes an argument that a prompt can accept.</p> </div><section class="tsd-panel tsd-member"><div data-typedoc-h="3" class="tsd-anchor-link" id="promptargument-description"><code class="tsd-tag">Optional</code><span>description</span><a href="#promptargument-description" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">description</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">string</span></div><div class="tsd-comment tsd-typography"><p>A human-readable description of the argument.</p> </div><div class="tsd-comment tsd-typography" /></section><section class="tsd-panel tsd-member tsd-is-inherited"><div data-typedoc-h="3" class="tsd-anchor-link" id="promptargument-name"><span>name</span><a href="#promptargument-name" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">name</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span></div><div class="tsd-comment tsd-typography"><p>Intended for programmatic or logical use, but used as a display name in past specs or fallback (if title isn't present).</p> </div><div class="tsd-comment tsd-typography" /><aside class="tsd-sources"><p>Inherited from BaseMetadata.name</p></aside></section><section class="tsd-panel tsd-member"><div data-typedoc-h="3" class="tsd-anchor-link" id="promptargument-required"><code class="tsd-tag">Optional</code><span>required</span><a href="#promptargument-required" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">required</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">boolean</span></div><div class="tsd-comment tsd-typography"><p>Whether this argument must be provided.</p> </div><div class="tsd-comment tsd-typography" /></section><section class="tsd-panel tsd-member tsd-is-inherited"><div data-typedoc-h="3" class="tsd-anchor-link" id="promptargument-title"><code class="tsd-tag">Optional</code><span>title</span><a href="#promptargument-title" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">title</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">string</span></div><div class="tsd-comment tsd-typography"><p>Intended for UI and end-user contexts — optimized to be human-readable and easily understood,
even by those unfamiliar with domain-specific terminology.</p> <p>If not provided, the name should be used for display (except for Tool,
where <code>annotations.title</code> should be given precedence over using <code>name</code>,
if present).</p> </div><div class="tsd-comment tsd-typography" /><aside class="tsd-sources"><p>Inherited from BaseMetadata.title</p></aside></section>

### `PromptMessage`

<div class="tsd-signature"><span class="tsd-signature-keyword">interface</span> <span class="tsd-kind-interface">PromptMessage</span> <span class="tsd-signature-symbol">\{</span><br />  <a class="tsd-kind-property" href="#">content</a><span class="tsd-signature-symbol">:</span> <a href="#contentblock" class="tsd-signature-type tsd-kind-type-alias">ContentBlock</a><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#">role</a><span class="tsd-signature-symbol">:</span> <a href="#role" class="tsd-signature-type tsd-kind-type-alias">Role</a><span class="tsd-signature-symbol">;</span><br /><span class="tsd-signature-symbol">}</span></div><div class="tsd-comment tsd-typography"><p>Describes a message returned as part of a prompt.</p> <p>This is similar to <code>SamplingMessage</code>, but also supports the embedding of
resources from the MCP server.</p> </div>

### `PromptReference`

<div class="tsd-signature"><span class="tsd-signature-keyword">interface</span> <span class="tsd-kind-interface">PromptReference</span> <span class="tsd-signature-symbol">\{</span><br />  <a class="tsd-kind-property" href="#promptreference-name">name</a><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#promptreference-title">title</a><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#">type</a><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">"ref/prompt"</span><span class="tsd-signature-symbol">;</span><br /><span class="tsd-signature-symbol">}</span></div><div class="tsd-comment tsd-typography"><p>Identifies a prompt.</p> </div><section class="tsd-panel tsd-member tsd-is-inherited"><div data-typedoc-h="3" class="tsd-anchor-link" id="promptreference-name"><span>name</span><a href="#promptreference-name" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">name</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span></div><div class="tsd-comment tsd-typography"><p>Intended for programmatic or logical use, but used as a display name in past specs or fallback (if title isn't present).</p> </div><div class="tsd-comment tsd-typography" /><aside class="tsd-sources"><p>Inherited from BaseMetadata.name</p></aside></section><section class="tsd-panel tsd-member tsd-is-inherited"><div data-typedoc-h="3" class="tsd-anchor-link" id="promptreference-title"><code class="tsd-tag">Optional</code><span>title</span><a href="#promptreference-title" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">title</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">string</span></div><div class="tsd-comment tsd-typography"><p>Intended for UI and end-user contexts — optimized to be human-readable and easily understood,
even by those unfamiliar with domain-specific terminology.</p> <p>If not provided, the name should be used for display (except for Tool,
where <code>annotations.title</code> should be given precedence over using <code>name</code>,
if present).</p> </div><div class="tsd-comment tsd-typography" /><aside class="tsd-sources"><p>Inherited from BaseMetadata.title</p></aside></section>

### `RequestId`

<div class="tsd-signature"><span class="tsd-kind-type-alias">RequestId</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span> <span class="tsd-signature-symbol">|</span> <span class="tsd-signature-type">number</span></div><div class="tsd-comment tsd-typography"><p>A uniquely identifying ID for a request in JSON-RPC.</p> </div><div class="tsd-comment tsd-typography" />

### `Resource`

<div class="tsd-signature"><span class="tsd-signature-keyword">interface</span> <span class="tsd-kind-interface">Resource</span> <span class="tsd-signature-symbol">\{</span><br />  <a class="tsd-kind-property" href="#resource-_meta">\_meta</a><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-symbol">\{</span> <span class="tsd-signature-symbol">\[</span><span class="tsd-kind-index-signature">key</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">]:</span> <span class="tsd-signature-type">unknown</span> <span class="tsd-signature-symbol">}</span><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#resource-annotations">annotations</a><span class="tsd-signature-symbol">?:</span> <a href="#annotations" class="tsd-signature-type tsd-kind-interface">Annotations</a><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#resource-description">description</a><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#resource-mimetype">mimeType</a><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#resource-name">name</a><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#resource-size">size</a><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">number</span><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#resource-title">title</a><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#resource-uri">uri</a><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">;</span><br /><span class="tsd-signature-symbol">}</span></div><div class="tsd-comment tsd-typography"><p>A known resource that the server is capable of reading.</p> </div><section class="tsd-panel tsd-member"><div data-typedoc-h="3" class="tsd-anchor-link" id="resource-_meta"><code class="tsd-tag">Optional</code><span>\_<wbr />meta</span><a href="#resource-_meta" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">\_meta</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-symbol">\{</span> <span class="tsd-signature-symbol">\[</span><span class="tsd-kind-index-signature">key</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">]:</span> <span class="tsd-signature-type">unknown</span> <span class="tsd-signature-symbol">}</span></div><div class="tsd-comment tsd-typography"><p>See <a href="/specification/2025-06-18/basic/index#meta">General fields: <code>\_meta</code></a> for notes on <code>\_meta</code> usage.</p> </div><div class="tsd-comment tsd-typography" /></section><section class="tsd-panel tsd-member"><div data-typedoc-h="3" class="tsd-anchor-link" id="resource-annotations"><code class="tsd-tag">Optional</code><span>annotations</span><a href="#resource-annotations" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">annotations</span><span class="tsd-signature-symbol">?:</span> <a href="#annotations" class="tsd-signature-type tsd-kind-interface">Annotations</a></div><div class="tsd-comment tsd-typography"><p>Optional annotations for the client.</p> </div><div class="tsd-comment tsd-typography" /></section><section class="tsd-panel tsd-member"><div data-typedoc-h="3" class="tsd-anchor-link" id="resource-description"><code class="tsd-tag">Optional</code><span>description</span><a href="#resource-description" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">description</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">string</span></div><div class="tsd-comment tsd-typography"><p>A description of what this resource represents.</p> <p>This can be used by clients to improve the LLM's understanding of available resources. It can be thought of like a "hint" to the model.</p> </div><div class="tsd-comment tsd-typography" /></section><section class="tsd-panel tsd-member"><div data-typedoc-h="3" class="tsd-anchor-link" id="resource-mimetype"><code class="tsd-tag">Optional</code><span>mime<wbr />Type</span><a href="#resource-mimetype" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">mimeType</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">string</span></div><div class="tsd-comment tsd-typography"><p>The MIME type of this resource, if known.</p> </div><div class="tsd-comment tsd-typography" /></section><section class="tsd-panel tsd-member tsd-is-inherited"><div data-typedoc-h="3" class="tsd-anchor-link" id="resource-name"><span>name</span><a href="#resource-name" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">name</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span></div><div class="tsd-comment tsd-typography"><p>Intended for programmatic or logical use, but used as a display name in past specs or fallback (if title isn't present).</p> </div><div class="tsd-comment tsd-typography" /><aside class="tsd-sources"><p>Inherited from BaseMetadata.name</p></aside></section><section class="tsd-panel tsd-member"><div data-typedoc-h="3" class="tsd-anchor-link" id="resource-size"><code class="tsd-tag">Optional</code><span>size</span><a href="#resource-size" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">size</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">number</span></div><div class="tsd-comment tsd-typography"><p>The size of the raw resource content, in bytes (i.e., before base64 encoding or any tokenization), if known.</p> <p>This can be used by Hosts to display file sizes and estimate context window usage.</p> </div><div class="tsd-comment tsd-typography" /></section><section class="tsd-panel tsd-member tsd-is-inherited"><div data-typedoc-h="3" class="tsd-anchor-link" id="resource-title"><code class="tsd-tag">Optional</code><span>title</span><a href="#resource-title" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">title</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">string</span></div><div class="tsd-comment tsd-typography"><p>Intended for UI and end-user contexts — optimized to be human-readable and easily understood,
even by those unfamiliar with domain-specific terminology.</p> <p>If not provided, the name should be used for display (except for Tool,
where <code>annotations.title</code> should be given precedence over using <code>name</code>,
if present).</p> </div><div class="tsd-comment tsd-typography" /><aside class="tsd-sources"><p>Inherited from BaseMetadata.title</p></aside></section><section class="tsd-panel tsd-member"><div data-typedoc-h="3" class="tsd-anchor-link" id="resource-uri"><span>uri</span><a href="#resource-uri" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">uri</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span></div><div class="tsd-comment tsd-typography"><p>The URI of this resource.</p> </div><div class="tsd-comment tsd-typography" /></section>

### `ResourceContents`

<div class="tsd-signature"><span class="tsd-signature-keyword">interface</span> <span class="tsd-kind-interface">ResourceContents</span> <span class="tsd-signature-symbol">\{</span><br />  <a class="tsd-kind-property" href="#resourcecontents-_meta">\_meta</a><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-symbol">\{</span> <span class="tsd-signature-symbol">\[</span><span class="tsd-kind-index-signature">key</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">]:</span> <span class="tsd-signature-type">unknown</span> <span class="tsd-signature-symbol">}</span><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#resourcecontents-mimetype">mimeType</a><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#resourcecontents-uri">uri</a><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">;</span><br /><span class="tsd-signature-symbol">}</span></div><div class="tsd-comment tsd-typography"><p>The contents of a specific resource or sub-resource.</p> </div><section class="tsd-panel tsd-member"><div data-typedoc-h="3" class="tsd-anchor-link" id="resourcecontents-_meta"><code class="tsd-tag">Optional</code><span>\_<wbr />meta</span><a href="#resourcecontents-_meta" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">\_meta</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-symbol">\{</span> <span class="tsd-signature-symbol">\[</span><span class="tsd-kind-index-signature">key</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">]:</span> <span class="tsd-signature-type">unknown</span> <span class="tsd-signature-symbol">}</span></div><div class="tsd-comment tsd-typography"><p>See <a href="/specification/2025-06-18/basic/index#meta">General fields: <code>\_meta</code></a> for notes on <code>\_meta</code> usage.</p> </div><div class="tsd-comment tsd-typography" /></section><section class="tsd-panel tsd-member"><div data-typedoc-h="3" class="tsd-anchor-link" id="resourcecontents-mimetype"><code class="tsd-tag">Optional</code><span>mime<wbr />Type</span><a href="#resourcecontents-mimetype" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">mimeType</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">string</span></div><div class="tsd-comment tsd-typography"><p>The MIME type of this resource, if known.</p> </div><div class="tsd-comment tsd-typography" /></section><section class="tsd-panel tsd-member"><div data-typedoc-h="3" class="tsd-anchor-link" id="resourcecontents-uri"><span>uri</span><a href="#resourcecontents-uri" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">uri</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span></div><div class="tsd-comment tsd-typography"><p>The URI of this resource.</p> </div><div class="tsd-comment tsd-typography" /></section>

### `ResourceLink`

<div class="tsd-signature"><span class="tsd-signature-keyword">interface</span> <span class="tsd-kind-interface">ResourceLink</span> <span class="tsd-signature-symbol">\{</span><br />  <a class="tsd-kind-property" href="#resourcelink-_meta">\_meta</a><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-symbol">\{</span> <span class="tsd-signature-symbol">\[</span><span class="tsd-kind-index-signature">key</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">]:</span> <span class="tsd-signature-type">unknown</span> <span class="tsd-signature-symbol">}</span><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#resourcelink-annotations">annotations</a><span class="tsd-signature-symbol">?:</span> <a href="#annotations" class="tsd-signature-type tsd-kind-interface">Annotations</a><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#resourcelink-description">description</a><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#resourcelink-mimetype">mimeType</a><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#resourcelink-name">name</a><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#resourcelink-size">size</a><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">number</span><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#resourcelink-title">title</a><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#">type</a><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">"resource\_link"</span><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#resourcelink-uri">uri</a><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">;</span><br /><span class="tsd-signature-symbol">}</span></div><div class="tsd-comment tsd-typography"><p>A resource that the server is capable of reading, included in a prompt or tool call result.</p> <p>Note: resource links returned by tools are not guaranteed to appear in the results of <code>resources/list</code> requests.</p> </div><section class="tsd-panel tsd-member tsd-is-inherited"><div data-typedoc-h="3" class="tsd-anchor-link" id="resourcelink-_meta"><code class="tsd-tag">Optional</code><span>\_<wbr />meta</span><a href="#resourcelink-_meta" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">\_meta</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-symbol">\{</span> <span class="tsd-signature-symbol">\[</span><span class="tsd-kind-index-signature">key</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">]:</span> <span class="tsd-signature-type">unknown</span> <span class="tsd-signature-symbol">}</span></div><div class="tsd-comment tsd-typography"><p>See <a href="/specification/2025-06-18/basic/index#meta">General fields: <code>\_meta</code></a> for notes on <code>\_meta</code> usage.</p> </div><div class="tsd-comment tsd-typography" /><aside class="tsd-sources"><p>Inherited from <a href="#resource">Resource</a>.<a href="#resource-_meta">\_meta</a></p></aside></section><section class="tsd-panel tsd-member tsd-is-inherited"><div data-typedoc-h="3" class="tsd-anchor-link" id="resourcelink-annotations"><code class="tsd-tag">Optional</code><span>annotations</span><a href="#resourcelink-annotations" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">annotations</span><span class="tsd-signature-symbol">?:</span> <a href="#annotations" class="tsd-signature-type tsd-kind-interface">Annotations</a></div><div class="tsd-comment tsd-typography"><p>Optional annotations for the client.</p> </div><div class="tsd-comment tsd-typography" /><aside class="tsd-sources"><p>Inherited from <a href="#resource">Resource</a>.<a href="#resource-annotations">annotations</a></p></aside></section><section class="tsd-panel tsd-member tsd-is-inherited"><div data-typedoc-h="3" class="tsd-anchor-link" id="resourcelink-description"><code class="tsd-tag">Optional</code><span>description</span><a href="#resourcelink-description" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">description</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">string</span></div><div class="tsd-comment tsd-typography"><p>A description of what this resource represents.</p> <p>This can be used by clients to improve the LLM's understanding of available resources. It can be thought of like a "hint" to the model.</p> </div><div class="tsd-comment tsd-typography" /><aside class="tsd-sources"><p>Inherited from <a href="#resource">Resource</a>.<a href="#resource-description">description</a></p></aside></section><section class="tsd-panel tsd-member tsd-is-inherited"><div data-typedoc-h="3" class="tsd-anchor-link" id="resourcelink-mimetype"><code class="tsd-tag">Optional</code><span>mime<wbr />Type</span><a href="#resourcelink-mimetype" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">mimeType</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">string</span></div><div class="tsd-comment tsd-typography"><p>The MIME type of this resource, if known.</p> </div><div class="tsd-comment tsd-typography" /><aside class="tsd-sources"><p>Inherited from <a href="#resource">Resource</a>.<a href="#resource-mimetype">mimeType</a></p></aside></section><section class="tsd-panel tsd-member tsd-is-inherited"><div data-typedoc-h="3" class="tsd-anchor-link" id="resourcelink-name"><span>name</span><a href="#resourcelink-name" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">name</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span></div><div class="tsd-comment tsd-typography"><p>Intended for programmatic or logical use, but used as a display name in past specs or fallback (if title isn't present).</p> </div><div class="tsd-comment tsd-typography" /><aside class="tsd-sources"><p>Inherited from <a href="#resource">Resource</a>.<a href="#resource-name">name</a></p></aside></section><section class="tsd-panel tsd-member tsd-is-inherited"><div data-typedoc-h="3" class="tsd-anchor-link" id="resourcelink-size"><code class="tsd-tag">Optional</code><span>size</span><a href="#resourcelink-size" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">size</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">number</span></div><div class="tsd-comment tsd-typography"><p>The size of the raw resource content, in bytes (i.e., before base64 encoding or any tokenization), if known.</p> <p>This can be used by Hosts to display file sizes and estimate context window usage.</p> </div><div class="tsd-comment tsd-typography" /><aside class="tsd-sources"><p>Inherited from <a href="#resource">Resource</a>.<a href="#resource-size">size</a></p></aside></section><section class="tsd-panel tsd-member tsd-is-inherited"><div data-typedoc-h="3" class="tsd-anchor-link" id="resourcelink-title"><code class="tsd-tag">Optional</code><span>title</span><a href="#resourcelink-title" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">title</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">string</span></div><div class="tsd-comment tsd-typography"><p>Intended for UI and end-user contexts — optimized to be human-readable and easily understood,
even by those unfamiliar with domain-specific terminology.</p> <p>If not provided, the name should be used for display (except for Tool,
where <code>annotations.title</code> should be given precedence over using <code>name</code>,
if present).</p> </div><div class="tsd-comment tsd-typography" /><aside class="tsd-sources"><p>Inherited from <a href="#resource">Resource</a>.<a href="#resource-title">title</a></p></aside></section><section class="tsd-panel tsd-member tsd-is-inherited"><div data-typedoc-h="3" class="tsd-anchor-link" id="resourcelink-uri"><span>uri</span><a href="#resourcelink-uri" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">uri</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span></div><div class="tsd-comment tsd-typography"><p>The URI of this resource.</p> </div><div class="tsd-comment tsd-typography" /><aside class="tsd-sources"><p>Inherited from <a href="#resource">Resource</a>.<a href="#resource-uri">uri</a></p></aside></section>

### `ResourceTemplate`

<div class="tsd-signature"><span class="tsd-signature-keyword">interface</span> <span class="tsd-kind-interface">ResourceTemplate</span> <span class="tsd-signature-symbol">\{</span><br />  <a class="tsd-kind-property" href="#resourcetemplate-_meta">\_meta</a><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-symbol">\{</span> <span class="tsd-signature-symbol">\[</span><span class="tsd-kind-index-signature">key</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">]:</span> <span class="tsd-signature-type">unknown</span> <span class="tsd-signature-symbol">}</span><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#resourcetemplate-annotations">annotations</a><span class="tsd-signature-symbol">?:</span> <a href="#annotations" class="tsd-signature-type tsd-kind-interface">Annotations</a><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#resourcetemplate-description">description</a><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#resourcetemplate-mimetype">mimeType</a><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#resourcetemplate-name">name</a><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#resourcetemplate-title">title</a><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#resourcetemplate-uritemplate">uriTemplate</a><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">;</span><br /><span class="tsd-signature-symbol">}</span></div><div class="tsd-comment tsd-typography"><p>A template description for resources available on the server.</p> </div><section class="tsd-panel tsd-member"><div data-typedoc-h="3" class="tsd-anchor-link" id="resourcetemplate-_meta"><code class="tsd-tag">Optional</code><span>\_<wbr />meta</span><a href="#resourcetemplate-_meta" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">\_meta</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-symbol">\{</span> <span class="tsd-signature-symbol">\[</span><span class="tsd-kind-index-signature">key</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">]:</span> <span class="tsd-signature-type">unknown</span> <span class="tsd-signature-symbol">}</span></div><div class="tsd-comment tsd-typography"><p>See <a href="/specification/2025-06-18/basic/index#meta">General fields: <code>\_meta</code></a> for notes on <code>\_meta</code> usage.</p> </div><div class="tsd-comment tsd-typography" /></section><section class="tsd-panel tsd-member"><div data-typedoc-h="3" class="tsd-anchor-link" id="resourcetemplate-annotations"><code class="tsd-tag">Optional</code><span>annotations</span><a href="#resourcetemplate-annotations" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">annotations</span><span class="tsd-signature-symbol">?:</span> <a href="#annotations" class="tsd-signature-type tsd-kind-interface">Annotations</a></div><div class="tsd-comment tsd-typography"><p>Optional annotations for the client.</p> </div><div class="tsd-comment tsd-typography" /></section><section class="tsd-panel tsd-member"><div data-typedoc-h="3" class="tsd-anchor-link" id="resourcetemplate-description"><code class="tsd-tag">Optional</code><span>description</span><a href="#resourcetemplate-description" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">description</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">string</span></div><div class="tsd-comment tsd-typography"><p>A description of what this template is for.</p> <p>This can be used by clients to improve the LLM's understanding of available resources. It can be thought of like a "hint" to the model.</p> </div><div class="tsd-comment tsd-typography" /></section><section class="tsd-panel tsd-member"><div data-typedoc-h="3" class="tsd-anchor-link" id="resourcetemplate-mimetype"><code class="tsd-tag">Optional</code><span>mime<wbr />Type</span><a href="#resourcetemplate-mimetype" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">mimeType</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">string</span></div><div class="tsd-comment tsd-typography"><p>The MIME type for all resources that match this template. This should only be included if all resources matching this template have the same type.</p> </div><div class="tsd-comment tsd-typography" /></section><section class="tsd-panel tsd-member tsd-is-inherited"><div data-typedoc-h="3" class="tsd-anchor-link" id="resourcetemplate-name"><span>name</span><a href="#resourcetemplate-name" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">name</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span></div><div class="tsd-comment tsd-typography"><p>Intended for programmatic or logical use, but used as a display name in past specs or fallback (if title isn't present).</p> </div><div class="tsd-comment tsd-typography" /><aside class="tsd-sources"><p>Inherited from BaseMetadata.name</p></aside></section><section class="tsd-panel tsd-member tsd-is-inherited"><div data-typedoc-h="3" class="tsd-anchor-link" id="resourcetemplate-title"><code class="tsd-tag">Optional</code><span>title</span><a href="#resourcetemplate-title" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">title</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">string</span></div><div class="tsd-comment tsd-typography"><p>Intended for UI and end-user contexts — optimized to be human-readable and easily understood,
even by those unfamiliar with domain-specific terminology.</p> <p>If not provided, the name should be used for display (except for Tool,
where <code>annotations.title</code> should be given precedence over using <code>name</code>,
if present).</p> </div><div class="tsd-comment tsd-typography" /><aside class="tsd-sources"><p>Inherited from BaseMetadata.title</p></aside></section><section class="tsd-panel tsd-member"><div data-typedoc-h="3" class="tsd-anchor-link" id="resourcetemplate-uritemplate"><span>uri<wbr />Template</span><a href="#resourcetemplate-uritemplate" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">uriTemplate</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span></div><div class="tsd-comment tsd-typography"><p>A URI template (according to RFC 6570) that can be used to construct resource URIs.</p> </div><div class="tsd-comment tsd-typography" /></section>

### `ResourceTemplateReference`

<div class="tsd-signature"><span class="tsd-signature-keyword">interface</span> <span class="tsd-kind-interface">ResourceTemplateReference</span> <span class="tsd-signature-symbol">\{</span><br />  <a class="tsd-kind-property" href="#">type</a><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">"ref/resource"</span><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#resourcetemplatereference-uri">uri</a><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">;</span><br /><span class="tsd-signature-symbol">}</span></div><div class="tsd-comment tsd-typography"><p>A reference to a resource or resource template definition.</p> </div><section class="tsd-panel tsd-member"><div data-typedoc-h="3" class="tsd-anchor-link" id="resourcetemplatereference-uri"><span>uri</span><a href="#resourcetemplatereference-uri" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">uri</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span></div><div class="tsd-comment tsd-typography"><p>The URI or URI template of the resource.</p> </div><div class="tsd-comment tsd-typography" /></section>

### `Result`

<div class="tsd-signature"><span class="tsd-signature-keyword">interface</span> <span class="tsd-kind-interface">Result</span> <span class="tsd-signature-symbol">\{</span><br />  <a class="tsd-kind-property" href="#result-_meta">\_meta</a><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-symbol">\{</span> <span class="tsd-signature-symbol">\[</span><span class="tsd-kind-index-signature">key</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">]:</span> <span class="tsd-signature-type">unknown</span> <span class="tsd-signature-symbol">}</span><span class="tsd-signature-symbol">;</span><br />  <span class="tsd-signature-symbol">\[</span><span class="tsd-kind-index-signature">key</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">]:</span> <span class="tsd-signature-type">unknown</span><span class="tsd-signature-symbol">;</span><br /><span class="tsd-signature-symbol">}</span></div><section class="tsd-panel tsd-member"><div data-typedoc-h="3" class="tsd-anchor-link" id="result-_meta"><code class="tsd-tag">Optional</code><span>\_<wbr />meta</span><a href="#result-_meta" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">\_meta</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-symbol">\{</span> <span class="tsd-signature-symbol">\[</span><span class="tsd-kind-index-signature">key</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">]:</span> <span class="tsd-signature-type">unknown</span> <span class="tsd-signature-symbol">}</span></div><div class="tsd-comment tsd-typography"><p>See <a href="/specification/2025-06-18/basic/index#meta">General fields: <code>\_meta</code></a> for notes on <code>\_meta</code> usage.</p> </div><div class="tsd-comment tsd-typography" /></section>

### `Role`

<div class="tsd-signature"><span class="tsd-kind-type-alias">Role</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">"user"</span> <span class="tsd-signature-symbol">|</span> <span class="tsd-signature-type">"assistant"</span></div><div class="tsd-comment tsd-typography"><p>The sender or recipient of messages and data in a conversation.</p> </div><div class="tsd-comment tsd-typography" />

### `Root`

<div class="tsd-signature"><span class="tsd-signature-keyword">interface</span> <span class="tsd-kind-interface">Root</span> <span class="tsd-signature-symbol">\{</span><br />  <a class="tsd-kind-property" href="#root-_meta">\_meta</a><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-symbol">\{</span> <span class="tsd-signature-symbol">\[</span><span class="tsd-kind-index-signature">key</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">]:</span> <span class="tsd-signature-type">unknown</span> <span class="tsd-signature-symbol">}</span><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#root-name">name</a><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#root-uri">uri</a><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">;</span><br /><span class="tsd-signature-symbol">}</span></div><div class="tsd-comment tsd-typography"><p>Represents a root directory or file that the server can operate on.</p> </div><section class="tsd-panel tsd-member"><div data-typedoc-h="3" class="tsd-anchor-link" id="root-_meta"><code class="tsd-tag">Optional</code><span>\_<wbr />meta</span><a href="#root-_meta" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">\_meta</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-symbol">\{</span> <span class="tsd-signature-symbol">\[</span><span class="tsd-kind-index-signature">key</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">]:</span> <span class="tsd-signature-type">unknown</span> <span class="tsd-signature-symbol">}</span></div><div class="tsd-comment tsd-typography"><p>See <a href="/specification/2025-06-18/basic/index#meta">General fields: <code>\_meta</code></a> for notes on <code>\_meta</code> usage.</p> </div><div class="tsd-comment tsd-typography" /></section><section class="tsd-panel tsd-member"><div data-typedoc-h="3" class="tsd-anchor-link" id="root-name"><code class="tsd-tag">Optional</code><span>name</span><a href="#root-name" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">name</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">string</span></div><div class="tsd-comment tsd-typography"><p>An optional name for the root. This can be used to provide a human-readable
identifier for the root, which may be useful for display purposes or for
referencing the root in other parts of the application.</p> </div><div class="tsd-comment tsd-typography" /></section><section class="tsd-panel tsd-member"><div data-typedoc-h="3" class="tsd-anchor-link" id="root-uri"><span>uri</span><a href="#root-uri" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">uri</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span></div><div class="tsd-comment tsd-typography"><p>The URI identifying the root. This <em>must</em> start with file:// for now.
This restriction may be relaxed in future versions of the protocol to allow
other URI schemes.</p> </div><div class="tsd-comment tsd-typography" /></section>

### `SamplingMessage`

<div class="tsd-signature"><span class="tsd-signature-keyword">interface</span> <span class="tsd-kind-interface">SamplingMessage</span> <span class="tsd-signature-symbol">\{</span><br />  <a class="tsd-kind-property" href="#">content</a><span class="tsd-signature-symbol">:</span> <a href="#textcontent" class="tsd-signature-type tsd-kind-interface">TextContent</a> <span class="tsd-signature-symbol">|</span> <a href="#imagecontent" class="tsd-signature-type tsd-kind-interface">ImageContent</a> <span class="tsd-signature-symbol">|</span> <a href="#audiocontent" class="tsd-signature-type tsd-kind-interface">AudioContent</a><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#">role</a><span class="tsd-signature-symbol">:</span> <a href="#role" class="tsd-signature-type tsd-kind-type-alias">Role</a><span class="tsd-signature-symbol">;</span><br /><span class="tsd-signature-symbol">}</span></div><div class="tsd-comment tsd-typography"><p>Describes a message issued to or received from an LLM API.</p> </div>

### `ServerCapabilities`

<div class="tsd-signature"><span class="tsd-signature-keyword">interface</span> <span class="tsd-kind-interface">ServerCapabilities</span> <span class="tsd-signature-symbol">\{</span><br />  <a class="tsd-kind-property" href="#servercapabilities-completions">completions</a><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">object</span><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#servercapabilities-experimental">experimental</a><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-symbol">\{</span> <span class="tsd-signature-symbol">\[</span><span class="tsd-kind-index-signature">key</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">]:</span> <span class="tsd-signature-type">object</span> <span class="tsd-signature-symbol">}</span><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#servercapabilities-logging">logging</a><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">object</span><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#servercapabilities-prompts">prompts</a><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-symbol">\{</span> <span class="tsd-kind-property">listChanged</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">boolean</span> <span class="tsd-signature-symbol">}</span><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#servercapabilities-resources">resources</a><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-symbol">\{</span> <span class="tsd-kind-property">listChanged</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">boolean</span><span class="tsd-signature-symbol">;</span> <span class="tsd-kind-property">subscribe</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">boolean</span> <span class="tsd-signature-symbol">}</span><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#servercapabilities-tools">tools</a><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-symbol">\{</span> <span class="tsd-kind-property">listChanged</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">boolean</span> <span class="tsd-signature-symbol">}</span><span class="tsd-signature-symbol">;</span><br /><span class="tsd-signature-symbol">}</span></div><div class="tsd-comment tsd-typography"><p>Capabilities that a server may support. Known capabilities are defined here, in this schema, but this is not a closed set: any server can define its own, additional capabilities.</p> </div><section class="tsd-panel tsd-member"><div data-typedoc-h="3" class="tsd-anchor-link" id="servercapabilities-completions"><code class="tsd-tag">Optional</code><span>completions</span><a href="#servercapabilities-completions" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">completions</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">object</span></div><div class="tsd-comment tsd-typography"><p>Present if the server supports argument autocompletion suggestions.</p> </div><div class="tsd-comment tsd-typography" /></section><section class="tsd-panel tsd-member"><div data-typedoc-h="3" class="tsd-anchor-link" id="servercapabilities-experimental"><code class="tsd-tag">Optional</code><span>experimental</span><a href="#servercapabilities-experimental" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">experimental</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-symbol">\{</span> <span class="tsd-signature-symbol">\[</span><span class="tsd-kind-index-signature">key</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">]:</span> <span class="tsd-signature-type">object</span> <span class="tsd-signature-symbol">}</span></div><div class="tsd-comment tsd-typography"><p>Experimental, non-standard capabilities that the server supports.</p> </div><div class="tsd-comment tsd-typography" /></section><section class="tsd-panel tsd-member"><div data-typedoc-h="3" class="tsd-anchor-link" id="servercapabilities-logging"><code class="tsd-tag">Optional</code><span>logging</span><a href="#servercapabilities-logging" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">logging</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">object</span></div><div class="tsd-comment tsd-typography"><p>Present if the server supports sending log messages to the client.</p> </div><div class="tsd-comment tsd-typography" /></section><section class="tsd-panel tsd-member"><div data-typedoc-h="3" class="tsd-anchor-link" id="servercapabilities-prompts"><code class="tsd-tag">Optional</code><span>prompts</span><a href="#servercapabilities-prompts" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">prompts</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-symbol">\{</span> <span class="tsd-kind-property">listChanged</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">boolean</span> <span class="tsd-signature-symbol">}</span></div><div class="tsd-comment tsd-typography"><p>Present if the server offers any prompt templates.</p> </div><div class="tsd-type-declaration"><div data-typedoc-h="4">Type declaration</div><ul class="tsd-parameters"><li class="tsd-parameter"><div data-typedoc-h="5"><code class="tsd-tag">Optional</code><span class="tsd-kind-property">listChanged</span><span class="tsd-signature-symbol">?: </span><span class="tsd-signature-type">boolean</span></div><div class="tsd-comment tsd-typography"><p>Whether this server supports notifications for changes to the prompt list.</p> </div><div class="tsd-comment tsd-typography" /></li></ul></div><div class="tsd-comment tsd-typography" /></section><section class="tsd-panel tsd-member"><div data-typedoc-h="3" class="tsd-anchor-link" id="servercapabilities-resources"><code class="tsd-tag">Optional</code><span>resources</span><a href="#servercapabilities-resources" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">resources</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-symbol">\{</span> <span class="tsd-kind-property">listChanged</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">boolean</span><span class="tsd-signature-symbol">;</span> <span class="tsd-kind-property">subscribe</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">boolean</span> <span class="tsd-signature-symbol">}</span></div><div class="tsd-comment tsd-typography"><p>Present if the server offers any resources to read.</p> </div><div class="tsd-type-declaration"><div data-typedoc-h="4">Type declaration</div><ul class="tsd-parameters"><li class="tsd-parameter"><div data-typedoc-h="5"><code class="tsd-tag">Optional</code><span class="tsd-kind-property">listChanged</span><span class="tsd-signature-symbol">?: </span><span class="tsd-signature-type">boolean</span></div><div class="tsd-comment tsd-typography"><p>Whether this server supports notifications for changes to the resource list.</p> </div><div class="tsd-comment tsd-typography" /></li><li class="tsd-parameter"><div data-typedoc-h="5"><code class="tsd-tag">Optional</code><span class="tsd-kind-property">subscribe</span><span class="tsd-signature-symbol">?: </span><span class="tsd-signature-type">boolean</span></div><div class="tsd-comment tsd-typography"><p>Whether this server supports subscribing to resource updates.</p> </div><div class="tsd-comment tsd-typography" /></li></ul></div><div class="tsd-comment tsd-typography" /></section><section class="tsd-panel tsd-member"><div data-typedoc-h="3" class="tsd-anchor-link" id="servercapabilities-tools"><code class="tsd-tag">Optional</code><span>tools</span><a href="#servercapabilities-tools" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">tools</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-symbol">\{</span> <span class="tsd-kind-property">listChanged</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">boolean</span> <span class="tsd-signature-symbol">}</span></div><div class="tsd-comment tsd-typography"><p>Present if the server offers any tools to call.</p> </div><div class="tsd-type-declaration"><div data-typedoc-h="4">Type declaration</div><ul class="tsd-parameters"><li class="tsd-parameter"><div data-typedoc-h="5"><code class="tsd-tag">Optional</code><span class="tsd-kind-property">listChanged</span><span class="tsd-signature-symbol">?: </span><span class="tsd-signature-type">boolean</span></div><div class="tsd-comment tsd-typography"><p>Whether this server supports notifications for changes to the tool list.</p> </div><div class="tsd-comment tsd-typography" /></li></ul></div><div class="tsd-comment tsd-typography" /></section>

### `StringSchema`

<div class="tsd-signature"><span class="tsd-signature-keyword">interface</span> <span class="tsd-kind-interface">StringSchema</span> <span class="tsd-signature-symbol">\{</span><br />  <a class="tsd-kind-property" href="#">description</a><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#">format</a><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">"uri"</span> <span class="tsd-signature-symbol">|</span> <span class="tsd-signature-type">"email"</span> <span class="tsd-signature-symbol">|</span> <span class="tsd-signature-type">"date"</span> <span class="tsd-signature-symbol">|</span> <span class="tsd-signature-type">"date-time"</span><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#">maxLength</a><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">number</span><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#">minLength</a><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">number</span><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#">title</a><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#">type</a><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">"string"</span><span class="tsd-signature-symbol">;</span><br /><span class="tsd-signature-symbol">}</span></div>

### `TextContent`

<div class="tsd-signature"><span class="tsd-signature-keyword">interface</span> <span class="tsd-kind-interface">TextContent</span> <span class="tsd-signature-symbol">\{</span><br />  <a class="tsd-kind-property" href="#textcontent-_meta">\_meta</a><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-symbol">\{</span> <span class="tsd-signature-symbol">\[</span><span class="tsd-kind-index-signature">key</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">]:</span> <span class="tsd-signature-type">unknown</span> <span class="tsd-signature-symbol">}</span><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#textcontent-annotations">annotations</a><span class="tsd-signature-symbol">?:</span> <a href="#annotations" class="tsd-signature-type tsd-kind-interface">Annotations</a><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#textcontent-text">text</a><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#">type</a><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">"text"</span><span class="tsd-signature-symbol">;</span><br /><span class="tsd-signature-symbol">}</span></div><div class="tsd-comment tsd-typography"><p>Text provided to or from an LLM.</p> </div><section class="tsd-panel tsd-member"><div data-typedoc-h="3" class="tsd-anchor-link" id="textcontent-_meta"><code class="tsd-tag">Optional</code><span>\_<wbr />meta</span><a href="#textcontent-_meta" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">\_meta</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-symbol">\{</span> <span class="tsd-signature-symbol">\[</span><span class="tsd-kind-index-signature">key</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">]:</span> <span class="tsd-signature-type">unknown</span> <span class="tsd-signature-symbol">}</span></div><div class="tsd-comment tsd-typography"><p>See <a href="/specification/2025-06-18/basic/index#meta">General fields: <code>\_meta</code></a> for notes on <code>\_meta</code> usage.</p> </div><div class="tsd-comment tsd-typography" /></section><section class="tsd-panel tsd-member"><div data-typedoc-h="3" class="tsd-anchor-link" id="textcontent-annotations"><code class="tsd-tag">Optional</code><span>annotations</span><a href="#textcontent-annotations" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">annotations</span><span class="tsd-signature-symbol">?:</span> <a href="#annotations" class="tsd-signature-type tsd-kind-interface">Annotations</a></div><div class="tsd-comment tsd-typography"><p>Optional annotations for the client.</p> </div><div class="tsd-comment tsd-typography" /></section><section class="tsd-panel tsd-member"><div data-typedoc-h="3" class="tsd-anchor-link" id="textcontent-text"><span>text</span><a href="#textcontent-text" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">text</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span></div><div class="tsd-comment tsd-typography"><p>The text content of the message.</p> </div><div class="tsd-comment tsd-typography" /></section>

### `TextResourceContents`

<div class="tsd-signature"><span class="tsd-signature-keyword">interface</span> <span class="tsd-kind-interface">TextResourceContents</span> <span class="tsd-signature-symbol">\{</span><br />  <a class="tsd-kind-property" href="#textresourcecontents-_meta">\_meta</a><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-symbol">\{</span> <span class="tsd-signature-symbol">\[</span><span class="tsd-kind-index-signature">key</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">]:</span> <span class="tsd-signature-type">unknown</span> <span class="tsd-signature-symbol">}</span><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#textresourcecontents-mimetype">mimeType</a><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#textresourcecontents-text">text</a><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#textresourcecontents-uri">uri</a><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">;</span><br /><span class="tsd-signature-symbol">}</span></div><div class="tsd-comment tsd-typography"><p>The contents of a specific resource or sub-resource.</p> </div><section class="tsd-panel tsd-member tsd-is-inherited"><div data-typedoc-h="3" class="tsd-anchor-link" id="textresourcecontents-_meta"><code class="tsd-tag">Optional</code><span>\_<wbr />meta</span><a href="#textresourcecontents-_meta" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">\_meta</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-symbol">\{</span> <span class="tsd-signature-symbol">\[</span><span class="tsd-kind-index-signature">key</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">]:</span> <span class="tsd-signature-type">unknown</span> <span class="tsd-signature-symbol">}</span></div><div class="tsd-comment tsd-typography"><p>See <a href="/specification/2025-06-18/basic/index#meta">General fields: <code>\_meta</code></a> for notes on <code>\_meta</code> usage.</p> </div><div class="tsd-comment tsd-typography" /><aside class="tsd-sources"><p>Inherited from <a href="#resourcecontents">ResourceContents</a>.<a href="#resourcecontents-_meta">\_meta</a></p></aside></section><section class="tsd-panel tsd-member tsd-is-inherited"><div data-typedoc-h="3" class="tsd-anchor-link" id="textresourcecontents-mimetype"><code class="tsd-tag">Optional</code><span>mime<wbr />Type</span><a href="#textresourcecontents-mimetype" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">mimeType</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">string</span></div><div class="tsd-comment tsd-typography"><p>The MIME type of this resource, if known.</p> </div><div class="tsd-comment tsd-typography" /><aside class="tsd-sources"><p>Inherited from <a href="#resourcecontents">ResourceContents</a>.<a href="#resourcecontents-mimetype">mimeType</a></p></aside></section><section class="tsd-panel tsd-member"><div data-typedoc-h="3" class="tsd-anchor-link" id="textresourcecontents-text"><span>text</span><a href="#textresourcecontents-text" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">text</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span></div><div class="tsd-comment tsd-typography"><p>The text of the item. This must only be set if the item can actually be represented as text (not binary data).</p> </div><div class="tsd-comment tsd-typography" /></section><section class="tsd-panel tsd-member tsd-is-inherited"><div data-typedoc-h="3" class="tsd-anchor-link" id="textresourcecontents-uri"><span>uri</span><a href="#textresourcecontents-uri" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">uri</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span></div><div class="tsd-comment tsd-typography"><p>The URI of this resource.</p> </div><div class="tsd-comment tsd-typography" /><aside class="tsd-sources"><p>Inherited from <a href="#resourcecontents">ResourceContents</a>.<a href="#resourcecontents-uri">uri</a></p></aside></section>

### `Tool`

<div class="tsd-signature"><span class="tsd-signature-keyword">interface</span> <span class="tsd-kind-interface">Tool</span> <span class="tsd-signature-symbol">\{</span><br />  <a class="tsd-kind-property" href="#tool-_meta">\_meta</a><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-symbol">\{</span> <span class="tsd-signature-symbol">\[</span><span class="tsd-kind-index-signature">key</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">]:</span> <span class="tsd-signature-type">unknown</span> <span class="tsd-signature-symbol">}</span><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#tool-annotations">annotations</a><span class="tsd-signature-symbol">?:</span> <a href="#toolannotations" class="tsd-signature-type tsd-kind-interface">ToolAnnotations</a><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#tool-description">description</a><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#tool-inputschema">inputSchema</a><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-symbol">\{</span><br />    <span class="tsd-kind-property">properties</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-symbol">\{</span> <span class="tsd-signature-symbol">\[</span><span class="tsd-kind-index-signature">key</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">]:</span> <span class="tsd-signature-type">object</span> <span class="tsd-signature-symbol">}</span><span class="tsd-signature-symbol">;</span><br />    <span class="tsd-kind-property">required</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">\[]</span><span class="tsd-signature-symbol">;</span><br />    <span class="tsd-kind-property">type</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">"object"</span><span class="tsd-signature-symbol">;</span><br />  <span class="tsd-signature-symbol">}</span><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#tool-name">name</a><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#tool-outputschema">outputSchema</a><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-symbol">\{</span><br />    <span class="tsd-kind-property">properties</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-symbol">\{</span> <span class="tsd-signature-symbol">\[</span><span class="tsd-kind-index-signature">key</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">]:</span> <span class="tsd-signature-type">object</span> <span class="tsd-signature-symbol">}</span><span class="tsd-signature-symbol">;</span><br />    <span class="tsd-kind-property">required</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">\[]</span><span class="tsd-signature-symbol">;</span><br />    <span class="tsd-kind-property">type</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">"object"</span><span class="tsd-signature-symbol">;</span><br />  <span class="tsd-signature-symbol">}</span><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#tool-title">title</a><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">;</span><br /><span class="tsd-signature-symbol">}</span></div><div class="tsd-comment tsd-typography"><p>Definition for a tool the client can call.</p> </div><section class="tsd-panel tsd-member"><div data-typedoc-h="3" class="tsd-anchor-link" id="tool-_meta"><code class="tsd-tag">Optional</code><span>\_<wbr />meta</span><a href="#tool-_meta" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">\_meta</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-symbol">\{</span> <span class="tsd-signature-symbol">\[</span><span class="tsd-kind-index-signature">key</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">]:</span> <span class="tsd-signature-type">unknown</span> <span class="tsd-signature-symbol">}</span></div><div class="tsd-comment tsd-typography"><p>See <a href="/specification/2025-06-18/basic/index#meta">General fields: <code>\_meta</code></a> for notes on <code>\_meta</code> usage.</p> </div><div class="tsd-comment tsd-typography" /></section><section class="tsd-panel tsd-member"><div data-typedoc-h="3" class="tsd-anchor-link" id="tool-annotations"><code class="tsd-tag">Optional</code><span>annotations</span><a href="#tool-annotations" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">annotations</span><span class="tsd-signature-symbol">?:</span> <a href="#toolannotations" class="tsd-signature-type tsd-kind-interface">ToolAnnotations</a></div><div class="tsd-comment tsd-typography"><p>Optional additional tool information.</p> <p>Display name precedence order is: title, annotations.title, then name.</p> </div><div class="tsd-comment tsd-typography" /></section><section class="tsd-panel tsd-member"><div data-typedoc-h="3" class="tsd-anchor-link" id="tool-description"><code class="tsd-tag">Optional</code><span>description</span><a href="#tool-description" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">description</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">string</span></div><div class="tsd-comment tsd-typography"><p>A human-readable description of the tool.</p> <p>This can be used by clients to improve the LLM's understanding of available tools. It can be thought of like a "hint" to the model.</p> </div><div class="tsd-comment tsd-typography" /></section><section class="tsd-panel tsd-member"><div data-typedoc-h="3" class="tsd-anchor-link" id="tool-inputschema"><span>input<wbr />Schema</span><a href="#tool-inputschema" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">inputSchema</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-symbol">\{</span><br />  <span class="tsd-kind-property">properties</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-symbol">\{</span> <span class="tsd-signature-symbol">\[</span><span class="tsd-kind-index-signature">key</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">]:</span> <span class="tsd-signature-type">object</span> <span class="tsd-signature-symbol">}</span><span class="tsd-signature-symbol">;</span><br />  <span class="tsd-kind-property">required</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">\[]</span><span class="tsd-signature-symbol">;</span><br />  <span class="tsd-kind-property">type</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">"object"</span><span class="tsd-signature-symbol">;</span><br /><span class="tsd-signature-symbol">}</span></div><div class="tsd-comment tsd-typography"><p>A JSON Schema object defining the expected parameters for the tool.</p> </div><div class="tsd-comment tsd-typography" /></section><section class="tsd-panel tsd-member tsd-is-inherited"><div data-typedoc-h="3" class="tsd-anchor-link" id="tool-name"><span>name</span><a href="#tool-name" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">name</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span></div><div class="tsd-comment tsd-typography"><p>Intended for programmatic or logical use, but used as a display name in past specs or fallback (if title isn't present).</p> </div><div class="tsd-comment tsd-typography" /><aside class="tsd-sources"><p>Inherited from BaseMetadata.name</p></aside></section><section class="tsd-panel tsd-member"><div data-typedoc-h="3" class="tsd-anchor-link" id="tool-outputschema"><code class="tsd-tag">Optional</code><span>output<wbr />Schema</span><a href="#tool-outputschema" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">outputSchema</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-symbol">\{</span><br />  <span class="tsd-kind-property">properties</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-symbol">\{</span> <span class="tsd-signature-symbol">\[</span><span class="tsd-kind-index-signature">key</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">]:</span> <span class="tsd-signature-type">object</span> <span class="tsd-signature-symbol">}</span><span class="tsd-signature-symbol">;</span><br />  <span class="tsd-kind-property">required</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">\[]</span><span class="tsd-signature-symbol">;</span><br />  <span class="tsd-kind-property">type</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">"object"</span><span class="tsd-signature-symbol">;</span><br /><span class="tsd-signature-symbol">}</span></div><div class="tsd-comment tsd-typography"><p>An optional JSON Schema object defining the structure of the tool's output returned in
the structuredContent field of a CallToolResult.</p> </div><div class="tsd-comment tsd-typography" /></section><section class="tsd-panel tsd-member tsd-is-inherited"><div data-typedoc-h="3" class="tsd-anchor-link" id="tool-title"><code class="tsd-tag">Optional</code><span>title</span><a href="#tool-title" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">title</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">string</span></div><div class="tsd-comment tsd-typography"><p>Intended for UI and end-user contexts — optimized to be human-readable and easily understood,
even by those unfamiliar with domain-specific terminology.</p> <p>If not provided, the name should be used for display (except for Tool,
where <code>annotations.title</code> should be given precedence over using <code>name</code>,
if present).</p> </div><div class="tsd-comment tsd-typography" /><aside class="tsd-sources"><p>Inherited from BaseMetadata.title</p></aside></section>

### `ToolAnnotations`

<div class="tsd-signature"><span class="tsd-signature-keyword">interface</span> <span class="tsd-kind-interface">ToolAnnotations</span> <span class="tsd-signature-symbol">\{</span><br />  <a class="tsd-kind-property" href="#toolannotations-destructivehint">destructiveHint</a><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">boolean</span><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#toolannotations-idempotenthint">idempotentHint</a><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">boolean</span><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#toolannotations-openworldhint">openWorldHint</a><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">boolean</span><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#toolannotations-readonlyhint">readOnlyHint</a><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">boolean</span><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#toolannotations-title">title</a><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">;</span><br /><span class="tsd-signature-symbol">}</span></div><div class="tsd-comment tsd-typography"><p>Additional properties describing a Tool to clients.</p> <p>NOTE: all properties in ToolAnnotations are <strong>hints</strong>.
They are not guaranteed to provide a faithful description of
tool behavior (including descriptive properties like <code>title</code>).</p> <p>Clients should never make tool use decisions based on ToolAnnotations
received from untrusted servers.</p> </div><section class="tsd-panel tsd-member"><div data-typedoc-h="3" class="tsd-anchor-link" id="toolannotations-destructivehint"><code class="tsd-tag">Optional</code><span>destructive<wbr />Hint</span><a href="#toolannotations-destructivehint" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">destructiveHint</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">boolean</span></div><div class="tsd-comment tsd-typography"><p>If true, the tool may perform destructive updates to its environment.
If false, the tool performs only additive updates.</p> <p>(This property is meaningful only when <code>readOnlyHint == false</code>)</p> <p>Default: true</p> </div><div class="tsd-comment tsd-typography" /></section><section class="tsd-panel tsd-member"><div data-typedoc-h="3" class="tsd-anchor-link" id="toolannotations-idempotenthint"><code class="tsd-tag">Optional</code><span>idempotent<wbr />Hint</span><a href="#toolannotations-idempotenthint" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">idempotentHint</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">boolean</span></div><div class="tsd-comment tsd-typography"><p>If true, calling the tool repeatedly with the same arguments
will have no additional effect on the its environment.</p> <p>(This property is meaningful only when <code>readOnlyHint == false</code>)</p> <p>Default: false</p> </div><div class="tsd-comment tsd-typography" /></section><section class="tsd-panel tsd-member"><div data-typedoc-h="3" class="tsd-anchor-link" id="toolannotations-openworldhint"><code class="tsd-tag">Optional</code><span>open<wbr />World<wbr />Hint</span><a href="#toolannotations-openworldhint" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">openWorldHint</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">boolean</span></div><div class="tsd-comment tsd-typography"><p>If true, this tool may interact with an "open world" of external
entities. If false, the tool's domain of interaction is closed.
For example, the world of a web search tool is open, whereas that
of a memory tool is not.</p> <p>Default: true</p> </div><div class="tsd-comment tsd-typography" /></section><section class="tsd-panel tsd-member"><div data-typedoc-h="3" class="tsd-anchor-link" id="toolannotations-readonlyhint"><code class="tsd-tag">Optional</code><span>read<wbr />Only<wbr />Hint</span><a href="#toolannotations-readonlyhint" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">readOnlyHint</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">boolean</span></div><div class="tsd-comment tsd-typography"><p>If true, the tool does not modify its environment.</p> <p>Default: false</p> </div><div class="tsd-comment tsd-typography" /></section><section class="tsd-panel tsd-member"><div data-typedoc-h="3" class="tsd-anchor-link" id="toolannotations-title"><code class="tsd-tag">Optional</code><span>title</span><a href="#toolannotations-title" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">title</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">string</span></div><div class="tsd-comment tsd-typography"><p>A human-readable title for the tool.</p> </div><div class="tsd-comment tsd-typography" /></section>

## `completion/complete`

### `CompleteRequest`

<div class="tsd-signature"><span class="tsd-signature-keyword">interface</span> <span class="tsd-kind-interface">CompleteRequest</span> <span class="tsd-signature-symbol">\{</span><br />  <a class="tsd-kind-property" href="#">method</a><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">"completion/complete"</span><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#completerequest-params">params</a><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-symbol">\{</span><br />    <span class="tsd-kind-property">argument</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-symbol">\{</span> <span class="tsd-kind-property">name</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">;</span> <span class="tsd-kind-property">value</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span> <span class="tsd-signature-symbol">}</span><span class="tsd-signature-symbol">;</span><br />    <span class="tsd-kind-property">context</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-symbol">\{</span> <span class="tsd-kind-property">arguments</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-symbol">\{</span> <span class="tsd-signature-symbol">\[</span><span class="tsd-kind-index-signature">key</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">]:</span> <span class="tsd-signature-type">string</span> <span class="tsd-signature-symbol">}</span> <span class="tsd-signature-symbol">}</span><span class="tsd-signature-symbol">;</span><br />    <span class="tsd-kind-property">ref</span><span class="tsd-signature-symbol">:</span> <a href="#promptreference" class="tsd-signature-type tsd-kind-interface">PromptReference</a> <span class="tsd-signature-symbol">|</span> <a href="#resourcetemplatereference" class="tsd-signature-type tsd-kind-interface">ResourceTemplateReference</a><span class="tsd-signature-symbol">;</span><br />  <span class="tsd-signature-symbol">}</span><span class="tsd-signature-symbol">;</span><br /><span class="tsd-signature-symbol">}</span></div><div class="tsd-comment tsd-typography"><p>A request from the client to the server, to ask for completion options.</p> </div><section class="tsd-panel tsd-member"><div data-typedoc-h="3" class="tsd-anchor-link" id="completerequest-params"><span>params</span><a href="#completerequest-params" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">params</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-symbol">\{</span><br />  <span class="tsd-kind-property">argument</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-symbol">\{</span> <span class="tsd-kind-property">name</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">;</span> <span class="tsd-kind-property">value</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span> <span class="tsd-signature-symbol">}</span><span class="tsd-signature-symbol">;</span><br />  <span class="tsd-kind-property">context</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-symbol">\{</span> <span class="tsd-kind-property">arguments</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-symbol">\{</span> <span class="tsd-signature-symbol">\[</span><span class="tsd-kind-index-signature">key</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">]:</span> <span class="tsd-signature-type">string</span> <span class="tsd-signature-symbol">}</span> <span class="tsd-signature-symbol">}</span><span class="tsd-signature-symbol">;</span><br />  <span class="tsd-kind-property">ref</span><span class="tsd-signature-symbol">:</span> <a href="#promptreference" class="tsd-signature-type tsd-kind-interface">PromptReference</a> <span class="tsd-signature-symbol">|</span> <a href="#resourcetemplatereference" class="tsd-signature-type tsd-kind-interface">ResourceTemplateReference</a><span class="tsd-signature-symbol">;</span><br /><span class="tsd-signature-symbol">}</span></div><div class="tsd-type-declaration"><div data-typedoc-h="4">Type declaration</div><ul class="tsd-parameters"><li class="tsd-parameter"><div data-typedoc-h="5"><span class="tsd-kind-property">argument</span><span class="tsd-signature-symbol">: </span><span class="tsd-signature-symbol">\{</span> <span class="tsd-kind-property">name</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">;</span> <span class="tsd-kind-property">value</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span> <span class="tsd-signature-symbol">}</span></div><div class="tsd-comment tsd-typography"><p>The argument's information</p> </div><div class="tsd-comment tsd-typography" /><ul class="tsd-parameters"><li class="tsd-parameter"><div data-typedoc-h="5"><span class="tsd-kind-property">name</span><span class="tsd-signature-symbol">: </span><span class="tsd-signature-type">string</span></div><div class="tsd-comment tsd-typography"><p>The name of the argument</p> </div><div class="tsd-comment tsd-typography" /></li><li class="tsd-parameter"><div data-typedoc-h="5"><span class="tsd-kind-property">value</span><span class="tsd-signature-symbol">: </span><span class="tsd-signature-type">string</span></div><div class="tsd-comment tsd-typography"><p>The value of the argument to use for completion matching.</p> </div><div class="tsd-comment tsd-typography" /></li></ul></li><li class="tsd-parameter"><div data-typedoc-h="5"><code class="tsd-tag">Optional</code><span class="tsd-kind-property">context</span><span class="tsd-signature-symbol">?: </span><span class="tsd-signature-symbol">\{</span> <span class="tsd-kind-property">arguments</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-symbol">\{</span> <span class="tsd-signature-symbol">\[</span><span class="tsd-kind-index-signature">key</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">]:</span> <span class="tsd-signature-type">string</span> <span class="tsd-signature-symbol">}</span> <span class="tsd-signature-symbol">}</span></div><div class="tsd-comment tsd-typography"><p>Additional, optional context for completions</p> </div><div class="tsd-comment tsd-typography" /><ul class="tsd-parameters"><li class="tsd-parameter"><div data-typedoc-h="5"><code class="tsd-tag">Optional</code><span class="tsd-kind-property">arguments</span><span class="tsd-signature-symbol">?: </span><span class="tsd-signature-symbol">\{</span> <span class="tsd-signature-symbol">\[</span><span class="tsd-kind-index-signature">key</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">]:</span> <span class="tsd-signature-type">string</span> <span class="tsd-signature-symbol">}</span></div><div class="tsd-comment tsd-typography"><p>Previously-resolved variables in a URI template or prompt.</p> </div><div class="tsd-comment tsd-typography" /></li></ul></li><li class="tsd-parameter"><div data-typedoc-h="5"><span class="tsd-kind-property">ref</span><span class="tsd-signature-symbol">: </span><a href="#promptreference" class="tsd-signature-type tsd-kind-interface">PromptReference</a> <span class="tsd-signature-symbol">|</span> <a href="#resourcetemplatereference" class="tsd-signature-type tsd-kind-interface">ResourceTemplateReference</a></div></li></ul></div><aside class="tsd-sources"><p>Overrides Request.params</p></aside></section>

### `CompleteResult`

<div class="tsd-signature"><span class="tsd-signature-keyword">interface</span> <span class="tsd-kind-interface">CompleteResult</span> <span class="tsd-signature-symbol">\{</span><br />  <a class="tsd-kind-property" href="#completeresult-_meta">\_meta</a><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-symbol">\{</span> <span class="tsd-signature-symbol">\[</span><span class="tsd-kind-index-signature">key</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">]:</span> <span class="tsd-signature-type">unknown</span> <span class="tsd-signature-symbol">}</span><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#completeresult-completion">completion</a><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-symbol">\{</span> <span class="tsd-kind-property">hasMore</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">boolean</span><span class="tsd-signature-symbol">;</span> <span class="tsd-kind-property">total</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">number</span><span class="tsd-signature-symbol">;</span> <span class="tsd-kind-property">values</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">\[]</span> <span class="tsd-signature-symbol">}</span><span class="tsd-signature-symbol">;</span><br />  <span class="tsd-signature-symbol">\[</span><span class="tsd-kind-index-signature">key</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">]:</span> <span class="tsd-signature-type">unknown</span><span class="tsd-signature-symbol">;</span><br /><span class="tsd-signature-symbol">}</span></div><div class="tsd-comment tsd-typography"><p>The server's response to a completion/complete request</p> </div><section class="tsd-panel tsd-member tsd-is-inherited"><div data-typedoc-h="3" class="tsd-anchor-link" id="completeresult-_meta"><code class="tsd-tag">Optional</code><span>\_<wbr />meta</span><a href="#completeresult-_meta" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">\_meta</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-symbol">\{</span> <span class="tsd-signature-symbol">\[</span><span class="tsd-kind-index-signature">key</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">]:</span> <span class="tsd-signature-type">unknown</span> <span class="tsd-signature-symbol">}</span></div><div class="tsd-comment tsd-typography"><p>See <a href="/specification/2025-06-18/basic/index#meta">General fields: <code>\_meta</code></a> for notes on <code>\_meta</code> usage.</p> </div><div class="tsd-comment tsd-typography" /><aside class="tsd-sources"><p>Inherited from <a href="#result">Result</a>.<a href="#result-_meta">\_meta</a></p></aside></section><section class="tsd-panel tsd-member"><div data-typedoc-h="3" class="tsd-anchor-link" id="completeresult-completion"><span>completion</span><a href="#completeresult-completion" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">completion</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-symbol">\{</span> <span class="tsd-kind-property">hasMore</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">boolean</span><span class="tsd-signature-symbol">;</span> <span class="tsd-kind-property">total</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">number</span><span class="tsd-signature-symbol">;</span> <span class="tsd-kind-property">values</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">\[]</span> <span class="tsd-signature-symbol">}</span></div><div class="tsd-type-declaration"><div data-typedoc-h="4">Type declaration</div><ul class="tsd-parameters"><li class="tsd-parameter"><div data-typedoc-h="5"><code class="tsd-tag">Optional</code><span class="tsd-kind-property">hasMore</span><span class="tsd-signature-symbol">?: </span><span class="tsd-signature-type">boolean</span></div><div class="tsd-comment tsd-typography"><p>Indicates whether there are additional completion options beyond those provided in the current response, even if the exact total is unknown.</p> </div><div class="tsd-comment tsd-typography" /></li><li class="tsd-parameter"><div data-typedoc-h="5"><code class="tsd-tag">Optional</code><span class="tsd-kind-property">total</span><span class="tsd-signature-symbol">?: </span><span class="tsd-signature-type">number</span></div><div class="tsd-comment tsd-typography"><p>The total number of completion options available. This can exceed the number of values actually sent in the response.</p> </div><div class="tsd-comment tsd-typography" /></li><li class="tsd-parameter"><div data-typedoc-h="5"><span class="tsd-kind-property">values</span><span class="tsd-signature-symbol">: </span><span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">\[]</span></div><div class="tsd-comment tsd-typography"><p>An array of completion values. Must not exceed 100 items.</p> </div><div class="tsd-comment tsd-typography" /></li></ul></div></section>

## `elicitation/create`

### `ElicitRequest`

<div class="tsd-signature"><span class="tsd-signature-keyword">interface</span> <span class="tsd-kind-interface">ElicitRequest</span> <span class="tsd-signature-symbol">\{</span><br />  <a class="tsd-kind-property" href="#">method</a><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">"elicitation/create"</span><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#elicitrequest-params">params</a><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-symbol">\{</span><br />    <span class="tsd-kind-property">message</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">;</span><br />    <span class="tsd-kind-property">requestedSchema</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-symbol">\{</span><br />      <span class="tsd-kind-property">properties</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-symbol">\{</span> <span class="tsd-signature-symbol">\[</span><span class="tsd-kind-index-signature">key</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">]:</span> <a href="#primitiveschemadefinition" class="tsd-signature-type tsd-kind-type-alias">PrimitiveSchemaDefinition</a> <span class="tsd-signature-symbol">}</span><span class="tsd-signature-symbol">;</span><br />      <span class="tsd-kind-property">required</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">\[]</span><span class="tsd-signature-symbol">;</span><br />      <span class="tsd-kind-property">type</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">"object"</span><span class="tsd-signature-symbol">;</span><br />    <span class="tsd-signature-symbol">}</span><span class="tsd-signature-symbol">;</span><br />  <span class="tsd-signature-symbol">}</span><span class="tsd-signature-symbol">;</span><br /><span class="tsd-signature-symbol">}</span></div><div class="tsd-comment tsd-typography"><p>A request from the server to elicit additional information from the user via the client.</p> </div><section class="tsd-panel tsd-member"><div data-typedoc-h="3" class="tsd-anchor-link" id="elicitrequest-params"><span>params</span><a href="#elicitrequest-params" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">params</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-symbol">\{</span><br />  <span class="tsd-kind-property">message</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">;</span><br />  <span class="tsd-kind-property">requestedSchema</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-symbol">\{</span><br />    <span class="tsd-kind-property">properties</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-symbol">\{</span> <span class="tsd-signature-symbol">\[</span><span class="tsd-kind-index-signature">key</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">]:</span> <a href="#primitiveschemadefinition" class="tsd-signature-type tsd-kind-type-alias">PrimitiveSchemaDefinition</a> <span class="tsd-signature-symbol">}</span><span class="tsd-signature-symbol">;</span><br />    <span class="tsd-kind-property">required</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">\[]</span><span class="tsd-signature-symbol">;</span><br />    <span class="tsd-kind-property">type</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">"object"</span><span class="tsd-signature-symbol">;</span><br />  <span class="tsd-signature-symbol">}</span><span class="tsd-signature-symbol">;</span><br /><span class="tsd-signature-symbol">}</span></div><div class="tsd-type-declaration"><div data-typedoc-h="4">Type declaration</div><ul class="tsd-parameters"><li class="tsd-parameter"><div data-typedoc-h="5"><span class="tsd-kind-property">message</span><span class="tsd-signature-symbol">: </span><span class="tsd-signature-type">string</span></div><div class="tsd-comment tsd-typography"><p>The message to present to the user.</p> </div><div class="tsd-comment tsd-typography" /></li><li class="tsd-parameter"><div data-typedoc-h="5"><span class="tsd-kind-property">requestedSchema</span><span class="tsd-signature-symbol">: </span><span class="tsd-signature-symbol">\{</span><br />  <span class="tsd-kind-property">properties</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-symbol">\{</span> <span class="tsd-signature-symbol">\[</span><span class="tsd-kind-index-signature">key</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">]:</span> <a href="#primitiveschemadefinition" class="tsd-signature-type tsd-kind-type-alias">PrimitiveSchemaDefinition</a> <span class="tsd-signature-symbol">}</span><span class="tsd-signature-symbol">;</span><br />  <span class="tsd-kind-property">required</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">\[]</span><span class="tsd-signature-symbol">;</span><br />  <span class="tsd-kind-property">type</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">"object"</span><span class="tsd-signature-symbol">;</span><br /><span class="tsd-signature-symbol">}</span></div><div class="tsd-comment tsd-typography"><p>A restricted subset of JSON Schema.
Only top-level properties are allowed, without nesting.</p> </div><div class="tsd-comment tsd-typography" /></li></ul></div><aside class="tsd-sources"><p>Overrides Request.params</p></aside></section>

### `ElicitResult`

<div class="tsd-signature"><span class="tsd-signature-keyword">interface</span> <span class="tsd-kind-interface">ElicitResult</span> <span class="tsd-signature-symbol">\{</span><br />  <a class="tsd-kind-property" href="#elicitresult-_meta">\_meta</a><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-symbol">\{</span> <span class="tsd-signature-symbol">\[</span><span class="tsd-kind-index-signature">key</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">]:</span> <span class="tsd-signature-type">unknown</span> <span class="tsd-signature-symbol">}</span><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#elicitresult-action">action</a><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">"accept"</span> <span class="tsd-signature-symbol">|</span> <span class="tsd-signature-type">"decline"</span> <span class="tsd-signature-symbol">|</span> <span class="tsd-signature-type">"cancel"</span><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#elicitresult-content">content</a><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-symbol">\{</span> <span class="tsd-signature-symbol">\[</span><span class="tsd-kind-index-signature">key</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">]:</span> <span class="tsd-signature-type">string</span> <span class="tsd-signature-symbol">|</span> <span class="tsd-signature-type">number</span> <span class="tsd-signature-symbol">|</span> <span class="tsd-signature-type">boolean</span> <span class="tsd-signature-symbol">}</span><span class="tsd-signature-symbol">;</span><br />  <span class="tsd-signature-symbol">\[</span><span class="tsd-kind-index-signature">key</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">]:</span> <span class="tsd-signature-type">unknown</span><span class="tsd-signature-symbol">;</span><br /><span class="tsd-signature-symbol">}</span></div><div class="tsd-comment tsd-typography"><p>The client's response to an elicitation request.</p> </div><section class="tsd-panel tsd-member tsd-is-inherited"><div data-typedoc-h="3" class="tsd-anchor-link" id="elicitresult-_meta"><code class="tsd-tag">Optional</code><span>\_<wbr />meta</span><a href="#elicitresult-_meta" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">\_meta</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-symbol">\{</span> <span class="tsd-signature-symbol">\[</span><span class="tsd-kind-index-signature">key</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">]:</span> <span class="tsd-signature-type">unknown</span> <span class="tsd-signature-symbol">}</span></div><div class="tsd-comment tsd-typography"><p>See <a href="/specification/2025-06-18/basic/index#meta">General fields: <code>\_meta</code></a> for notes on <code>\_meta</code> usage.</p> </div><div class="tsd-comment tsd-typography" /><aside class="tsd-sources"><p>Inherited from <a href="#result">Result</a>.<a href="#result-_meta">\_meta</a></p></aside></section><section class="tsd-panel tsd-member"><div data-typedoc-h="3" class="tsd-anchor-link" id="elicitresult-action"><span>action</span><a href="#elicitresult-action" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">action</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">"accept"</span> <span class="tsd-signature-symbol">|</span> <span class="tsd-signature-type">"decline"</span> <span class="tsd-signature-symbol">|</span> <span class="tsd-signature-type">"cancel"</span></div><div class="tsd-comment tsd-typography"><p>The user action in response to the elicitation.</p> <ul> <li>"accept": User submitted the form/confirmed the action</li> <li>"decline": User explicitly declined the action</li> <li>"cancel": User dismissed without making an explicit choice</li> </ul> </div><div class="tsd-comment tsd-typography" /></section><section class="tsd-panel tsd-member"><div data-typedoc-h="3" class="tsd-anchor-link" id="elicitresult-content"><code class="tsd-tag">Optional</code><span>content</span><a href="#elicitresult-content" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">content</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-symbol">\{</span> <span class="tsd-signature-symbol">\[</span><span class="tsd-kind-index-signature">key</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">]:</span> <span class="tsd-signature-type">string</span> <span class="tsd-signature-symbol">|</span> <span class="tsd-signature-type">number</span> <span class="tsd-signature-symbol">|</span> <span class="tsd-signature-type">boolean</span> <span class="tsd-signature-symbol">}</span></div><div class="tsd-comment tsd-typography"><p>The submitted form data, only present when action is "accept".
Contains values matching the requested schema.</p> </div><div class="tsd-comment tsd-typography" /></section>

## `initialize`

### `InitializeRequest`

<div class="tsd-signature"><span class="tsd-signature-keyword">interface</span> <span class="tsd-kind-interface">InitializeRequest</span> <span class="tsd-signature-symbol">\{</span><br />  <a class="tsd-kind-property" href="#">method</a><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">"initialize"</span><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#initializerequest-params">params</a><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-symbol">\{</span><br />    <span class="tsd-kind-property">capabilities</span><span class="tsd-signature-symbol">:</span> <a href="#clientcapabilities" class="tsd-signature-type tsd-kind-interface">ClientCapabilities</a><span class="tsd-signature-symbol">;</span><br />    <span class="tsd-kind-property">clientInfo</span><span class="tsd-signature-symbol">:</span> <a href="#implementation" class="tsd-signature-type tsd-kind-interface">Implementation</a><span class="tsd-signature-symbol">;</span><br />    <span class="tsd-kind-property">protocolVersion</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">;</span><br />  <span class="tsd-signature-symbol">}</span><span class="tsd-signature-symbol">;</span><br /><span class="tsd-signature-symbol">}</span></div><div class="tsd-comment tsd-typography"><p>This request is sent from the client to the server when it first connects, asking it to begin initialization.</p> </div><section class="tsd-panel tsd-member"><div data-typedoc-h="3" class="tsd-anchor-link" id="initializerequest-params"><span>params</span><a href="#initializerequest-params" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">params</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-symbol">\{</span><br />  <span class="tsd-kind-property">capabilities</span><span class="tsd-signature-symbol">:</span> <a href="#clientcapabilities" class="tsd-signature-type tsd-kind-interface">ClientCapabilities</a><span class="tsd-signature-symbol">;</span><br />  <span class="tsd-kind-property">clientInfo</span><span class="tsd-signature-symbol">:</span> <a href="#implementation" class="tsd-signature-type tsd-kind-interface">Implementation</a><span class="tsd-signature-symbol">;</span><br />  <span class="tsd-kind-property">protocolVersion</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">;</span><br /><span class="tsd-signature-symbol">}</span></div><div class="tsd-type-declaration"><div data-typedoc-h="4">Type declaration</div><ul class="tsd-parameters"><li class="tsd-parameter"><div data-typedoc-h="5"><span class="tsd-kind-property">capabilities</span><span class="tsd-signature-symbol">: </span><a href="#clientcapabilities" class="tsd-signature-type tsd-kind-interface">ClientCapabilities</a></div></li><li class="tsd-parameter"><div data-typedoc-h="5"><span class="tsd-kind-property">clientInfo</span><span class="tsd-signature-symbol">: </span><a href="#implementation" class="tsd-signature-type tsd-kind-interface">Implementation</a></div></li><li class="tsd-parameter"><div data-typedoc-h="5"><span class="tsd-kind-property">protocolVersion</span><span class="tsd-signature-symbol">: </span><span class="tsd-signature-type">string</span></div><div class="tsd-comment tsd-typography"><p>The latest version of the Model Context Protocol that the client supports. The client MAY decide to support older versions as well.</p> </div><div class="tsd-comment tsd-typography" /></li></ul></div><aside class="tsd-sources"><p>Overrides Request.params</p></aside></section>

### `InitializeResult`

<div class="tsd-signature"><span class="tsd-signature-keyword">interface</span> <span class="tsd-kind-interface">InitializeResult</span> <span class="tsd-signature-symbol">\{</span><br />  <a class="tsd-kind-property" href="#initializeresult-_meta">\_meta</a><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-symbol">\{</span> <span class="tsd-signature-symbol">\[</span><span class="tsd-kind-index-signature">key</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">]:</span> <span class="tsd-signature-type">unknown</span> <span class="tsd-signature-symbol">}</span><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#">capabilities</a><span class="tsd-signature-symbol">:</span> <a href="#servercapabilities" class="tsd-signature-type tsd-kind-interface">ServerCapabilities</a><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#initializeresult-instructions">instructions</a><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#initializeresult-protocolversion">protocolVersion</a><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#">serverInfo</a><span class="tsd-signature-symbol">:</span> <a href="#implementation" class="tsd-signature-type tsd-kind-interface">Implementation</a><span class="tsd-signature-symbol">;</span><br />  <span class="tsd-signature-symbol">\[</span><span class="tsd-kind-index-signature">key</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">]:</span> <span class="tsd-signature-type">unknown</span><span class="tsd-signature-symbol">;</span><br /><span class="tsd-signature-symbol">}</span></div><div class="tsd-comment tsd-typography"><p>After receiving an initialize request from the client, the server sends this response.</p> </div><section class="tsd-panel tsd-member tsd-is-inherited"><div data-typedoc-h="3" class="tsd-anchor-link" id="initializeresult-_meta"><code class="tsd-tag">Optional</code><span>\_<wbr />meta</span><a href="#initializeresult-_meta" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">\_meta</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-symbol">\{</span> <span class="tsd-signature-symbol">\[</span><span class="tsd-kind-index-signature">key</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">]:</span> <span class="tsd-signature-type">unknown</span> <span class="tsd-signature-symbol">}</span></div><div class="tsd-comment tsd-typography"><p>See <a href="/specification/2025-06-18/basic/index#meta">General fields: <code>\_meta</code></a> for notes on <code>\_meta</code> usage.</p> </div><div class="tsd-comment tsd-typography" /><aside class="tsd-sources"><p>Inherited from <a href="#result">Result</a>.<a href="#result-_meta">\_meta</a></p></aside></section><section class="tsd-panel tsd-member"><div data-typedoc-h="3" class="tsd-anchor-link" id="initializeresult-instructions"><code class="tsd-tag">Optional</code><span>instructions</span><a href="#initializeresult-instructions" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">instructions</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">string</span></div><div class="tsd-comment tsd-typography"><p>Instructions describing how to use the server and its features.</p> <p>This can be used by clients to improve the LLM's understanding of available tools, resources, etc. It can be thought of like a "hint" to the model. For example, this information MAY be added to the system prompt.</p> </div><div class="tsd-comment tsd-typography" /></section><section class="tsd-panel tsd-member"><div data-typedoc-h="3" class="tsd-anchor-link" id="initializeresult-protocolversion"><span>protocol<wbr />Version</span><a href="#initializeresult-protocolversion" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">protocolVersion</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span></div><div class="tsd-comment tsd-typography"><p>The version of the Model Context Protocol that the server wants to use. This may not match the version that the client requested. If the client cannot support this version, it MUST disconnect.</p> </div><div class="tsd-comment tsd-typography" /></section>

## `logging/setLevel`

### `SetLevelRequest`

<div class="tsd-signature"><span class="tsd-signature-keyword">interface</span> <span class="tsd-kind-interface">SetLevelRequest</span> <span class="tsd-signature-symbol">\{</span><br />  <a class="tsd-kind-property" href="#">method</a><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">"logging/setLevel"</span><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#setlevelrequest-params">params</a><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-symbol">\{</span> <span class="tsd-kind-property">level</span><span class="tsd-signature-symbol">:</span> <a href="#logginglevel" class="tsd-signature-type tsd-kind-type-alias">LoggingLevel</a> <span class="tsd-signature-symbol">}</span><span class="tsd-signature-symbol">;</span><br /><span class="tsd-signature-symbol">}</span></div><div class="tsd-comment tsd-typography"><p>A request from the client to the server, to enable or adjust logging.</p> </div><section class="tsd-panel tsd-member"><div data-typedoc-h="3" class="tsd-anchor-link" id="setlevelrequest-params"><span>params</span><a href="#setlevelrequest-params" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">params</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-symbol">\{</span> <span class="tsd-kind-property">level</span><span class="tsd-signature-symbol">:</span> <a href="#logginglevel" class="tsd-signature-type tsd-kind-type-alias">LoggingLevel</a> <span class="tsd-signature-symbol">}</span></div><div class="tsd-type-declaration"><div data-typedoc-h="4">Type declaration</div><ul class="tsd-parameters"><li class="tsd-parameter"><div data-typedoc-h="5"><span class="tsd-kind-property">level</span><span class="tsd-signature-symbol">: </span><a href="#logginglevel" class="tsd-signature-type tsd-kind-type-alias">LoggingLevel</a></div><div class="tsd-comment tsd-typography"><p>The level of logging that the client wants to receive from the server. The server should send all logs at this level and higher (i.e., more severe) to the client as notifications/message.</p> </div><div class="tsd-comment tsd-typography" /></li></ul></div><aside class="tsd-sources"><p>Overrides Request.params</p></aside></section>

## `notifications/cancelled`

### `CancelledNotification`

<div class="tsd-signature"><span class="tsd-signature-keyword">interface</span> <span class="tsd-kind-interface">CancelledNotification</span> <span class="tsd-signature-symbol">\{</span><br />  <a class="tsd-kind-property" href="#">method</a><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">"notifications/cancelled"</span><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#cancellednotification-params">params</a><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-symbol">\{</span> <span class="tsd-kind-property">reason</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">;</span> <span class="tsd-kind-property">requestId</span><span class="tsd-signature-symbol">:</span> <a href="#requestid" class="tsd-signature-type tsd-kind-type-alias">RequestId</a> <span class="tsd-signature-symbol">}</span><span class="tsd-signature-symbol">;</span><br /><span class="tsd-signature-symbol">}</span></div><div class="tsd-comment tsd-typography"><p>This notification can be sent by either side to indicate that it is cancelling a previously-issued request.</p> <p>The request SHOULD still be in-flight, but due to communication latency, it is always possible that this notification MAY arrive after the request has already finished.</p> <p>This notification indicates that the result will be unused, so any associated processing SHOULD cease.</p> <p>A client MUST NOT attempt to cancel its <code>initialize</code> request.</p> </div><section class="tsd-panel tsd-member"><div data-typedoc-h="3" class="tsd-anchor-link" id="cancellednotification-params"><span>params</span><a href="#cancellednotification-params" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">params</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-symbol">\{</span> <span class="tsd-kind-property">reason</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">;</span> <span class="tsd-kind-property">requestId</span><span class="tsd-signature-symbol">:</span> <a href="#requestid" class="tsd-signature-type tsd-kind-type-alias">RequestId</a> <span class="tsd-signature-symbol">}</span></div><div class="tsd-type-declaration"><div data-typedoc-h="4">Type declaration</div><ul class="tsd-parameters"><li class="tsd-parameter"><div data-typedoc-h="5"><code class="tsd-tag">Optional</code><span class="tsd-kind-property">reason</span><span class="tsd-signature-symbol">?: </span><span class="tsd-signature-type">string</span></div><div class="tsd-comment tsd-typography"><p>An optional string describing the reason for the cancellation. This MAY be logged or presented to the user.</p> </div><div class="tsd-comment tsd-typography" /></li><li class="tsd-parameter"><div data-typedoc-h="5"><span class="tsd-kind-property">requestId</span><span class="tsd-signature-symbol">: </span><a href="#requestid" class="tsd-signature-type tsd-kind-type-alias">RequestId</a></div><div class="tsd-comment tsd-typography"><p>The ID of the request to cancel.</p> <p>This MUST correspond to the ID of a request previously issued in the same direction.</p> </div><div class="tsd-comment tsd-typography" /></li></ul></div><aside class="tsd-sources"><p>Overrides Notification.params</p></aside></section>

## `notifications/initialized`

### `InitializedNotification`

<div class="tsd-signature"><span class="tsd-signature-keyword">interface</span> <span class="tsd-kind-interface">InitializedNotification</span> <span class="tsd-signature-symbol">\{</span><br />  <a class="tsd-kind-property" href="#">method</a><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">"notifications/initialized"</span><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#initializednotification-params">params</a><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-symbol">\{</span> <span class="tsd-kind-property">\_meta</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-symbol">\{</span> <span class="tsd-signature-symbol">\[</span><span class="tsd-kind-index-signature">key</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">]:</span> <span class="tsd-signature-type">unknown</span> <span class="tsd-signature-symbol">}</span><span class="tsd-signature-symbol">;</span> <span class="tsd-signature-symbol">\[</span><span class="tsd-kind-index-signature">key</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">]:</span> <span class="tsd-signature-type">unknown</span> <span class="tsd-signature-symbol">}</span><span class="tsd-signature-symbol">;</span><br /><span class="tsd-signature-symbol">}</span></div><div class="tsd-comment tsd-typography"><p>This notification is sent from the client to the server after initialization has finished.</p> </div><section class="tsd-panel tsd-member tsd-is-inherited"><div data-typedoc-h="3" class="tsd-anchor-link" id="initializednotification-params"><code class="tsd-tag">Optional</code><span>params</span><a href="#initializednotification-params" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">params</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-symbol">\{</span> <span class="tsd-kind-property">\_meta</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-symbol">\{</span> <span class="tsd-signature-symbol">\[</span><span class="tsd-kind-index-signature">key</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">]:</span> <span class="tsd-signature-type">unknown</span> <span class="tsd-signature-symbol">}</span><span class="tsd-signature-symbol">;</span> <span class="tsd-signature-symbol">\[</span><span class="tsd-kind-index-signature">key</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">]:</span> <span class="tsd-signature-type">unknown</span> <span class="tsd-signature-symbol">}</span></div><div class="tsd-type-declaration"><div data-typedoc-h="4">Type declaration</div><ul class="tsd-parameters"><li class="tsd-parameter-index-signature"><div data-typedoc-h="5"><span class="tsd-signature-symbol">\[</span><span class="tsd-kind-parameter">key</span>: <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">]:</span> <span class="tsd-signature-type">unknown</span></div></li><li class="tsd-parameter"><div data-typedoc-h="5"><code class="tsd-tag">Optional</code><span class="tsd-kind-property">\_meta</span><span class="tsd-signature-symbol">?: </span><span class="tsd-signature-symbol">\{</span> <span class="tsd-signature-symbol">\[</span><span class="tsd-kind-index-signature">key</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">]:</span> <span class="tsd-signature-type">unknown</span> <span class="tsd-signature-symbol">}</span></div><div class="tsd-comment tsd-typography"><p>See <a href="/specification/2025-06-18/basic/index#meta">General fields: <code>\_meta</code></a> for notes on <code>\_meta</code> usage.</p> </div><div class="tsd-comment tsd-typography" /></li></ul></div><aside class="tsd-sources"><p>Inherited from Notification.params</p></aside></section>

## `notifications/message`

### `LoggingMessageNotification`

<div class="tsd-signature"><span class="tsd-signature-keyword">interface</span> <span class="tsd-kind-interface">LoggingMessageNotification</span> <span class="tsd-signature-symbol">\{</span><br />  <a class="tsd-kind-property" href="#">method</a><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">"notifications/message"</span><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#loggingmessagenotification-params">params</a><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-symbol">\{</span> <span class="tsd-kind-property">data</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">unknown</span><span class="tsd-signature-symbol">;</span> <span class="tsd-kind-property">level</span><span class="tsd-signature-symbol">:</span> <a href="#logginglevel" class="tsd-signature-type tsd-kind-type-alias">LoggingLevel</a><span class="tsd-signature-symbol">;</span> <span class="tsd-kind-property">logger</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">string</span> <span class="tsd-signature-symbol">}</span><span class="tsd-signature-symbol">;</span><br /><span class="tsd-signature-symbol">}</span></div><div class="tsd-comment tsd-typography"><p>Notification of a log message passed from server to client. If no logging/setLevel request has been sent from the client, the server MAY decide which messages to send automatically.</p> </div><section class="tsd-panel tsd-member"><div data-typedoc-h="3" class="tsd-anchor-link" id="loggingmessagenotification-params"><span>params</span><a href="#loggingmessagenotification-params" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">params</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-symbol">\{</span> <span class="tsd-kind-property">data</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">unknown</span><span class="tsd-signature-symbol">;</span> <span class="tsd-kind-property">level</span><span class="tsd-signature-symbol">:</span> <a href="#logginglevel" class="tsd-signature-type tsd-kind-type-alias">LoggingLevel</a><span class="tsd-signature-symbol">;</span> <span class="tsd-kind-property">logger</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">string</span> <span class="tsd-signature-symbol">}</span></div><div class="tsd-type-declaration"><div data-typedoc-h="4">Type declaration</div><ul class="tsd-parameters"><li class="tsd-parameter"><div data-typedoc-h="5"><span class="tsd-kind-property">data</span><span class="tsd-signature-symbol">: </span><span class="tsd-signature-type">unknown</span></div><div class="tsd-comment tsd-typography"><p>The data to be logged, such as a string message or an object. Any JSON serializable type is allowed here.</p> </div><div class="tsd-comment tsd-typography" /></li><li class="tsd-parameter"><div data-typedoc-h="5"><span class="tsd-kind-property">level</span><span class="tsd-signature-symbol">: </span><a href="#logginglevel" class="tsd-signature-type tsd-kind-type-alias">LoggingLevel</a></div><div class="tsd-comment tsd-typography"><p>The severity of this log message.</p> </div><div class="tsd-comment tsd-typography" /></li><li class="tsd-parameter"><div data-typedoc-h="5"><code class="tsd-tag">Optional</code><span class="tsd-kind-property">logger</span><span class="tsd-signature-symbol">?: </span><span class="tsd-signature-type">string</span></div><div class="tsd-comment tsd-typography"><p>An optional name of the logger issuing this message.</p> </div><div class="tsd-comment tsd-typography" /></li></ul></div><aside class="tsd-sources"><p>Overrides Notification.params</p></aside></section>

## `notifications/progress`

### `ProgressNotification`

<div class="tsd-signature"><span class="tsd-signature-keyword">interface</span> <span class="tsd-kind-interface">ProgressNotification</span> <span class="tsd-signature-symbol">\{</span><br />  <a class="tsd-kind-property" href="#">method</a><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">"notifications/progress"</span><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#progressnotification-params">params</a><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-symbol">\{</span><br />    <span class="tsd-kind-property">message</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">;</span><br />    <span class="tsd-kind-property">progress</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">number</span><span class="tsd-signature-symbol">;</span><br />    <span class="tsd-kind-property">progressToken</span><span class="tsd-signature-symbol">:</span> <a href="#progresstoken" class="tsd-signature-type tsd-kind-type-alias">ProgressToken</a><span class="tsd-signature-symbol">;</span><br />    <span class="tsd-kind-property">total</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">number</span><span class="tsd-signature-symbol">;</span><br />  <span class="tsd-signature-symbol">}</span><span class="tsd-signature-symbol">;</span><br /><span class="tsd-signature-symbol">}</span></div><div class="tsd-comment tsd-typography"><p>An out-of-band notification used to inform the receiver of a progress update for a long-running request.</p> </div><section class="tsd-panel tsd-member"><div data-typedoc-h="3" class="tsd-anchor-link" id="progressnotification-params"><span>params</span><a href="#progressnotification-params" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">params</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-symbol">\{</span><br />  <span class="tsd-kind-property">message</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">;</span><br />  <span class="tsd-kind-property">progress</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">number</span><span class="tsd-signature-symbol">;</span><br />  <span class="tsd-kind-property">progressToken</span><span class="tsd-signature-symbol">:</span> <a href="#progresstoken" class="tsd-signature-type tsd-kind-type-alias">ProgressToken</a><span class="tsd-signature-symbol">;</span><br />  <span class="tsd-kind-property">total</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">number</span><span class="tsd-signature-symbol">;</span><br /><span class="tsd-signature-symbol">}</span></div><div class="tsd-type-declaration"><div data-typedoc-h="4">Type declaration</div><ul class="tsd-parameters"><li class="tsd-parameter"><div data-typedoc-h="5"><code class="tsd-tag">Optional</code><span class="tsd-kind-property">message</span><span class="tsd-signature-symbol">?: </span><span class="tsd-signature-type">string</span></div><div class="tsd-comment tsd-typography"><p>An optional message describing the current progress.</p> </div><div class="tsd-comment tsd-typography" /></li><li class="tsd-parameter"><div data-typedoc-h="5"><span class="tsd-kind-property">progress</span><span class="tsd-signature-symbol">: </span><span class="tsd-signature-type">number</span></div><div class="tsd-comment tsd-typography"><p>The progress thus far. This should increase every time progress is made, even if the total is unknown.</p> </div><div class="tsd-comment tsd-typography"><div class="tsd-tag-TJS-type"><div data-typedoc-h="4" class="tsd-anchor-link" id="tjs-type">TJS-type<a href="#tjs-type" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><p>number</p> </div></div></li><li class="tsd-parameter"><div data-typedoc-h="5"><span class="tsd-kind-property">progressToken</span><span class="tsd-signature-symbol">: </span><a href="#progresstoken" class="tsd-signature-type tsd-kind-type-alias">ProgressToken</a></div><div class="tsd-comment tsd-typography"><p>The progress token which was given in the initial request, used to associate this notification with the request that is proceeding.</p> </div><div class="tsd-comment tsd-typography" /></li><li class="tsd-parameter"><div data-typedoc-h="5"><code class="tsd-tag">Optional</code><span class="tsd-kind-property">total</span><span class="tsd-signature-symbol">?: </span><span class="tsd-signature-type">number</span></div><div class="tsd-comment tsd-typography"><p>Total number of items to process (or total progress required), if known.</p> </div><div class="tsd-comment tsd-typography"><div class="tsd-tag-TJS-type"><div data-typedoc-h="4" class="tsd-anchor-link" id="tjs-type-1">TJS-type<a href="#tjs-type-1" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><p>number</p> </div></div></li></ul></div><aside class="tsd-sources"><p>Overrides Notification.params</p></aside></section>

## `notifications/prompts/list_changed`

### `PromptListChangedNotification`

<div class="tsd-signature"><span class="tsd-signature-keyword">interface</span> <span class="tsd-kind-interface">PromptListChangedNotification</span> <span class="tsd-signature-symbol">\{</span><br />  <a class="tsd-kind-property" href="#">method</a><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">"notifications/prompts/list\_changed"</span><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#promptlistchangednotification-params">params</a><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-symbol">\{</span> <span class="tsd-kind-property">\_meta</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-symbol">\{</span> <span class="tsd-signature-symbol">\[</span><span class="tsd-kind-index-signature">key</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">]:</span> <span class="tsd-signature-type">unknown</span> <span class="tsd-signature-symbol">}</span><span class="tsd-signature-symbol">;</span> <span class="tsd-signature-symbol">\[</span><span class="tsd-kind-index-signature">key</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">]:</span> <span class="tsd-signature-type">unknown</span> <span class="tsd-signature-symbol">}</span><span class="tsd-signature-symbol">;</span><br /><span class="tsd-signature-symbol">}</span></div><div class="tsd-comment tsd-typography"><p>An optional notification from the server to the client, informing it that the list of prompts it offers has changed. This may be issued by servers without any previous subscription from the client.</p> </div><section class="tsd-panel tsd-member tsd-is-inherited"><div data-typedoc-h="3" class="tsd-anchor-link" id="promptlistchangednotification-params"><code class="tsd-tag">Optional</code><span>params</span><a href="#promptlistchangednotification-params" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">params</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-symbol">\{</span> <span class="tsd-kind-property">\_meta</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-symbol">\{</span> <span class="tsd-signature-symbol">\[</span><span class="tsd-kind-index-signature">key</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">]:</span> <span class="tsd-signature-type">unknown</span> <span class="tsd-signature-symbol">}</span><span class="tsd-signature-symbol">;</span> <span class="tsd-signature-symbol">\[</span><span class="tsd-kind-index-signature">key</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">]:</span> <span class="tsd-signature-type">unknown</span> <span class="tsd-signature-symbol">}</span></div><div class="tsd-type-declaration"><div data-typedoc-h="4">Type declaration</div><ul class="tsd-parameters"><li class="tsd-parameter-index-signature"><div data-typedoc-h="5"><span class="tsd-signature-symbol">\[</span><span class="tsd-kind-parameter">key</span>: <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">]:</span> <span class="tsd-signature-type">unknown</span></div></li><li class="tsd-parameter"><div data-typedoc-h="5"><code class="tsd-tag">Optional</code><span class="tsd-kind-property">\_meta</span><span class="tsd-signature-symbol">?: </span><span class="tsd-signature-symbol">\{</span> <span class="tsd-signature-symbol">\[</span><span class="tsd-kind-index-signature">key</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">]:</span> <span class="tsd-signature-type">unknown</span> <span class="tsd-signature-symbol">}</span></div><div class="tsd-comment tsd-typography"><p>See <a href="/specification/2025-06-18/basic/index#meta">General fields: <code>\_meta</code></a> for notes on <code>\_meta</code> usage.</p> </div><div class="tsd-comment tsd-typography" /></li></ul></div><aside class="tsd-sources"><p>Inherited from Notification.params</p></aside></section>

## `notifications/resources/list_changed`

### `ResourceListChangedNotification`

<div class="tsd-signature"><span class="tsd-signature-keyword">interface</span> <span class="tsd-kind-interface">ResourceListChangedNotification</span> <span class="tsd-signature-symbol">\{</span><br />  <a class="tsd-kind-property" href="#">method</a><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">"notifications/resources/list\_changed"</span><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#resourcelistchangednotification-params">params</a><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-symbol">\{</span> <span class="tsd-kind-property">\_meta</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-symbol">\{</span> <span class="tsd-signature-symbol">\[</span><span class="tsd-kind-index-signature">key</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">]:</span> <span class="tsd-signature-type">unknown</span> <span class="tsd-signature-symbol">}</span><span class="tsd-signature-symbol">;</span> <span class="tsd-signature-symbol">\[</span><span class="tsd-kind-index-signature">key</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">]:</span> <span class="tsd-signature-type">unknown</span> <span class="tsd-signature-symbol">}</span><span class="tsd-signature-symbol">;</span><br /><span class="tsd-signature-symbol">}</span></div><div class="tsd-comment tsd-typography"><p>An optional notification from the server to the client, informing it that the list of resources it can read from has changed. This may be issued by servers without any previous subscription from the client.</p> </div><section class="tsd-panel tsd-member tsd-is-inherited"><div data-typedoc-h="3" class="tsd-anchor-link" id="resourcelistchangednotification-params"><code class="tsd-tag">Optional</code><span>params</span><a href="#resourcelistchangednotification-params" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">params</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-symbol">\{</span> <span class="tsd-kind-property">\_meta</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-symbol">\{</span> <span class="tsd-signature-symbol">\[</span><span class="tsd-kind-index-signature">key</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">]:</span> <span class="tsd-signature-type">unknown</span> <span class="tsd-signature-symbol">}</span><span class="tsd-signature-symbol">;</span> <span class="tsd-signature-symbol">\[</span><span class="tsd-kind-index-signature">key</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">]:</span> <span class="tsd-signature-type">unknown</span> <span class="tsd-signature-symbol">}</span></div><div class="tsd-type-declaration"><div data-typedoc-h="4">Type declaration</div><ul class="tsd-parameters"><li class="tsd-parameter-index-signature"><div data-typedoc-h="5"><span class="tsd-signature-symbol">\[</span><span class="tsd-kind-parameter">key</span>: <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">]:</span> <span class="tsd-signature-type">unknown</span></div></li><li class="tsd-parameter"><div data-typedoc-h="5"><code class="tsd-tag">Optional</code><span class="tsd-kind-property">\_meta</span><span class="tsd-signature-symbol">?: </span><span class="tsd-signature-symbol">\{</span> <span class="tsd-signature-symbol">\[</span><span class="tsd-kind-index-signature">key</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">]:</span> <span class="tsd-signature-type">unknown</span> <span class="tsd-signature-symbol">}</span></div><div class="tsd-comment tsd-typography"><p>See <a href="/specification/2025-06-18/basic/index#meta">General fields: <code>\_meta</code></a> for notes on <code>\_meta</code> usage.</p> </div><div class="tsd-comment tsd-typography" /></li></ul></div><aside class="tsd-sources"><p>Inherited from Notification.params</p></aside></section>

## `notifications/resources/updated`

### `ResourceUpdatedNotification`

<div class="tsd-signature"><span class="tsd-signature-keyword">interface</span> <span class="tsd-kind-interface">ResourceUpdatedNotification</span> <span class="tsd-signature-symbol">\{</span><br />  <a class="tsd-kind-property" href="#">method</a><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">"notifications/resources/updated"</span><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#resourceupdatednotification-params">params</a><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-symbol">\{</span> <span class="tsd-kind-property">uri</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span> <span class="tsd-signature-symbol">}</span><span class="tsd-signature-symbol">;</span><br /><span class="tsd-signature-symbol">}</span></div><div class="tsd-comment tsd-typography"><p>A notification from the server to the client, informing it that a resource has changed and may need to be read again. This should only be sent if the client previously sent a resources/subscribe request.</p> </div><section class="tsd-panel tsd-member"><div data-typedoc-h="3" class="tsd-anchor-link" id="resourceupdatednotification-params"><span>params</span><a href="#resourceupdatednotification-params" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">params</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-symbol">\{</span> <span class="tsd-kind-property">uri</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span> <span class="tsd-signature-symbol">}</span></div><div class="tsd-type-declaration"><div data-typedoc-h="4">Type declaration</div><ul class="tsd-parameters"><li class="tsd-parameter"><div data-typedoc-h="5"><span class="tsd-kind-property">uri</span><span class="tsd-signature-symbol">: </span><span class="tsd-signature-type">string</span></div><div class="tsd-comment tsd-typography"><p>The URI of the resource that has been updated. This might be a sub-resource of the one that the client actually subscribed to.</p> </div><div class="tsd-comment tsd-typography" /></li></ul></div><aside class="tsd-sources"><p>Overrides Notification.params</p></aside></section>

## `notifications/roots/list_changed`

### `RootsListChangedNotification`

<div class="tsd-signature"><span class="tsd-signature-keyword">interface</span> <span class="tsd-kind-interface">RootsListChangedNotification</span> <span class="tsd-signature-symbol">\{</span><br />  <a class="tsd-kind-property" href="#">method</a><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">"notifications/roots/list\_changed"</span><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#rootslistchangednotification-params">params</a><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-symbol">\{</span> <span class="tsd-kind-property">\_meta</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-symbol">\{</span> <span class="tsd-signature-symbol">\[</span><span class="tsd-kind-index-signature">key</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">]:</span> <span class="tsd-signature-type">unknown</span> <span class="tsd-signature-symbol">}</span><span class="tsd-signature-symbol">;</span> <span class="tsd-signature-symbol">\[</span><span class="tsd-kind-index-signature">key</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">]:</span> <span class="tsd-signature-type">unknown</span> <span class="tsd-signature-symbol">}</span><span class="tsd-signature-symbol">;</span><br /><span class="tsd-signature-symbol">}</span></div><div class="tsd-comment tsd-typography"><p>A notification from the client to the server, informing it that the list of roots has changed.
This notification should be sent whenever the client adds, removes, or modifies any root.
The server should then request an updated list of roots using the ListRootsRequest.</p> </div><section class="tsd-panel tsd-member tsd-is-inherited"><div data-typedoc-h="3" class="tsd-anchor-link" id="rootslistchangednotification-params"><code class="tsd-tag">Optional</code><span>params</span><a href="#rootslistchangednotification-params" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">params</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-symbol">\{</span> <span class="tsd-kind-property">\_meta</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-symbol">\{</span> <span class="tsd-signature-symbol">\[</span><span class="tsd-kind-index-signature">key</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">]:</span> <span class="tsd-signature-type">unknown</span> <span class="tsd-signature-symbol">}</span><span class="tsd-signature-symbol">;</span> <span class="tsd-signature-symbol">\[</span><span class="tsd-kind-index-signature">key</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">]:</span> <span class="tsd-signature-type">unknown</span> <span class="tsd-signature-symbol">}</span></div><div class="tsd-type-declaration"><div data-typedoc-h="4">Type declaration</div><ul class="tsd-parameters"><li class="tsd-parameter-index-signature"><div data-typedoc-h="5"><span class="tsd-signature-symbol">\[</span><span class="tsd-kind-parameter">key</span>: <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">]:</span> <span class="tsd-signature-type">unknown</span></div></li><li class="tsd-parameter"><div data-typedoc-h="5"><code class="tsd-tag">Optional</code><span class="tsd-kind-property">\_meta</span><span class="tsd-signature-symbol">?: </span><span class="tsd-signature-symbol">\{</span> <span class="tsd-signature-symbol">\[</span><span class="tsd-kind-index-signature">key</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">]:</span> <span class="tsd-signature-type">unknown</span> <span class="tsd-signature-symbol">}</span></div><div class="tsd-comment tsd-typography"><p>See <a href="/specification/2025-06-18/basic/index#meta">General fields: <code>\_meta</code></a> for notes on <code>\_meta</code> usage.</p> </div><div class="tsd-comment tsd-typography" /></li></ul></div><aside class="tsd-sources"><p>Inherited from Notification.params</p></aside></section>

## `notifications/tools/list_changed`

### `ToolListChangedNotification`

<div class="tsd-signature"><span class="tsd-signature-keyword">interface</span> <span class="tsd-kind-interface">ToolListChangedNotification</span> <span class="tsd-signature-symbol">\{</span><br />  <a class="tsd-kind-property" href="#">method</a><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">"notifications/tools/list\_changed"</span><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#toollistchangednotification-params">params</a><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-symbol">\{</span> <span class="tsd-kind-property">\_meta</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-symbol">\{</span> <span class="tsd-signature-symbol">\[</span><span class="tsd-kind-index-signature">key</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">]:</span> <span class="tsd-signature-type">unknown</span> <span class="tsd-signature-symbol">}</span><span class="tsd-signature-symbol">;</span> <span class="tsd-signature-symbol">\[</span><span class="tsd-kind-index-signature">key</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">]:</span> <span class="tsd-signature-type">unknown</span> <span class="tsd-signature-symbol">}</span><span class="tsd-signature-symbol">;</span><br /><span class="tsd-signature-symbol">}</span></div><div class="tsd-comment tsd-typography"><p>An optional notification from the server to the client, informing it that the list of tools it offers has changed. This may be issued by servers without any previous subscription from the client.</p> </div><section class="tsd-panel tsd-member tsd-is-inherited"><div data-typedoc-h="3" class="tsd-anchor-link" id="toollistchangednotification-params"><code class="tsd-tag">Optional</code><span>params</span><a href="#toollistchangednotification-params" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">params</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-symbol">\{</span> <span class="tsd-kind-property">\_meta</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-symbol">\{</span> <span class="tsd-signature-symbol">\[</span><span class="tsd-kind-index-signature">key</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">]:</span> <span class="tsd-signature-type">unknown</span> <span class="tsd-signature-symbol">}</span><span class="tsd-signature-symbol">;</span> <span class="tsd-signature-symbol">\[</span><span class="tsd-kind-index-signature">key</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">]:</span> <span class="tsd-signature-type">unknown</span> <span class="tsd-signature-symbol">}</span></div><div class="tsd-type-declaration"><div data-typedoc-h="4">Type declaration</div><ul class="tsd-parameters"><li class="tsd-parameter-index-signature"><div data-typedoc-h="5"><span class="tsd-signature-symbol">\[</span><span class="tsd-kind-parameter">key</span>: <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">]:</span> <span class="tsd-signature-type">unknown</span></div></li><li class="tsd-parameter"><div data-typedoc-h="5"><code class="tsd-tag">Optional</code><span class="tsd-kind-property">\_meta</span><span class="tsd-signature-symbol">?: </span><span class="tsd-signature-symbol">\{</span> <span class="tsd-signature-symbol">\[</span><span class="tsd-kind-index-signature">key</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">]:</span> <span class="tsd-signature-type">unknown</span> <span class="tsd-signature-symbol">}</span></div><div class="tsd-comment tsd-typography"><p>See <a href="/specification/2025-06-18/basic/index#meta">General fields: <code>\_meta</code></a> for notes on <code>\_meta</code> usage.</p> </div><div class="tsd-comment tsd-typography" /></li></ul></div><aside class="tsd-sources"><p>Inherited from Notification.params</p></aside></section>

## `ping`

### `PingRequest`

<div class="tsd-signature"><span class="tsd-signature-keyword">interface</span> <span class="tsd-kind-interface">PingRequest</span> <span class="tsd-signature-symbol">\{</span><br />  <a class="tsd-kind-property" href="#">method</a><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">"ping"</span><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#pingrequest-params">params</a><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-symbol">\{</span><br />    <span class="tsd-kind-property">\_meta</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-symbol">\{</span> <span class="tsd-kind-property">progressToken</span><span class="tsd-signature-symbol">?:</span> <a href="#progresstoken" class="tsd-signature-type tsd-kind-type-alias">ProgressToken</a><span class="tsd-signature-symbol">;</span> <span class="tsd-signature-symbol">\[</span><span class="tsd-kind-index-signature">key</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">]:</span> <span class="tsd-signature-type">unknown</span> <span class="tsd-signature-symbol">}</span><span class="tsd-signature-symbol">;</span><br />    <span class="tsd-signature-symbol">\[</span><span class="tsd-kind-index-signature">key</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">]:</span> <span class="tsd-signature-type">unknown</span><span class="tsd-signature-symbol">;</span><br />  <span class="tsd-signature-symbol">}</span><span class="tsd-signature-symbol">;</span><br /><span class="tsd-signature-symbol">}</span></div><div class="tsd-comment tsd-typography"><p>A ping, issued by either the server or the client, to check that the other party is still alive. The receiver must promptly respond, or else may be disconnected.</p> </div><section class="tsd-panel tsd-member tsd-is-inherited"><div data-typedoc-h="3" class="tsd-anchor-link" id="pingrequest-params"><code class="tsd-tag">Optional</code><span>params</span><a href="#pingrequest-params" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">params</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-symbol">\{</span><br />  <span class="tsd-kind-property">\_meta</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-symbol">\{</span> <span class="tsd-kind-property">progressToken</span><span class="tsd-signature-symbol">?:</span> <a href="#progresstoken" class="tsd-signature-type tsd-kind-type-alias">ProgressToken</a><span class="tsd-signature-symbol">;</span> <span class="tsd-signature-symbol">\[</span><span class="tsd-kind-index-signature">key</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">]:</span> <span class="tsd-signature-type">unknown</span> <span class="tsd-signature-symbol">}</span><span class="tsd-signature-symbol">;</span><br />  <span class="tsd-signature-symbol">\[</span><span class="tsd-kind-index-signature">key</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">]:</span> <span class="tsd-signature-type">unknown</span><span class="tsd-signature-symbol">;</span><br /><span class="tsd-signature-symbol">}</span></div><div class="tsd-type-declaration"><div data-typedoc-h="4">Type declaration</div><ul class="tsd-parameters"><li class="tsd-parameter-index-signature"><div data-typedoc-h="5"><span class="tsd-signature-symbol">\[</span><span class="tsd-kind-parameter">key</span>: <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">]:</span> <span class="tsd-signature-type">unknown</span></div></li><li class="tsd-parameter"><div data-typedoc-h="5"><code class="tsd-tag">Optional</code><span class="tsd-kind-property">\_meta</span><span class="tsd-signature-symbol">?: </span><span class="tsd-signature-symbol">\{</span> <span class="tsd-kind-property">progressToken</span><span class="tsd-signature-symbol">?:</span> <a href="#progresstoken" class="tsd-signature-type tsd-kind-type-alias">ProgressToken</a><span class="tsd-signature-symbol">;</span> <span class="tsd-signature-symbol">\[</span><span class="tsd-kind-index-signature">key</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">]:</span> <span class="tsd-signature-type">unknown</span> <span class="tsd-signature-symbol">}</span></div><div class="tsd-comment tsd-typography"><p>See <a href="/specification/2025-06-18/basic/index#meta">General fields: <code>\_meta</code></a> for notes on <code>\_meta</code> usage.</p> </div><div class="tsd-comment tsd-typography" /><ul class="tsd-parameters"><li class="tsd-parameter"><div data-typedoc-h="5"><code class="tsd-tag">Optional</code><span class="tsd-kind-property">progressToken</span><span class="tsd-signature-symbol">?: </span><a href="#progresstoken" class="tsd-signature-type tsd-kind-type-alias">ProgressToken</a></div><div class="tsd-comment tsd-typography"><p>If specified, the caller is requesting out-of-band progress notifications for this request (as represented by notifications/progress). The value of this parameter is an opaque token that will be attached to any subsequent notifications. The receiver is not obligated to provide these notifications.</p> </div><div class="tsd-comment tsd-typography" /></li></ul></li></ul></div><aside class="tsd-sources"><p>Inherited from Request.params</p></aside></section>

## `prompts/get`

### `GetPromptRequest`

<div class="tsd-signature"><span class="tsd-signature-keyword">interface</span> <span class="tsd-kind-interface">GetPromptRequest</span> <span class="tsd-signature-symbol">\{</span><br />  <a class="tsd-kind-property" href="#">method</a><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">"prompts/get"</span><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#getpromptrequest-params">params</a><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-symbol">\{</span> <span class="tsd-kind-property">arguments</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-symbol">\{</span> <span class="tsd-signature-symbol">\[</span><span class="tsd-kind-index-signature">key</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">]:</span> <span class="tsd-signature-type">string</span> <span class="tsd-signature-symbol">}</span><span class="tsd-signature-symbol">;</span> <span class="tsd-kind-property">name</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span> <span class="tsd-signature-symbol">}</span><span class="tsd-signature-symbol">;</span><br /><span class="tsd-signature-symbol">}</span></div><div class="tsd-comment tsd-typography"><p>Used by the client to get a prompt provided by the server.</p> </div><section class="tsd-panel tsd-member"><div data-typedoc-h="3" class="tsd-anchor-link" id="getpromptrequest-params"><span>params</span><a href="#getpromptrequest-params" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">params</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-symbol">\{</span> <span class="tsd-kind-property">arguments</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-symbol">\{</span> <span class="tsd-signature-symbol">\[</span><span class="tsd-kind-index-signature">key</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">]:</span> <span class="tsd-signature-type">string</span> <span class="tsd-signature-symbol">}</span><span class="tsd-signature-symbol">;</span> <span class="tsd-kind-property">name</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span> <span class="tsd-signature-symbol">}</span></div><div class="tsd-type-declaration"><div data-typedoc-h="4">Type declaration</div><ul class="tsd-parameters"><li class="tsd-parameter"><div data-typedoc-h="5"><code class="tsd-tag">Optional</code><span class="tsd-kind-property">arguments</span><span class="tsd-signature-symbol">?: </span><span class="tsd-signature-symbol">\{</span> <span class="tsd-signature-symbol">\[</span><span class="tsd-kind-index-signature">key</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">]:</span> <span class="tsd-signature-type">string</span> <span class="tsd-signature-symbol">}</span></div><div class="tsd-comment tsd-typography"><p>Arguments to use for templating the prompt.</p> </div><div class="tsd-comment tsd-typography" /></li><li class="tsd-parameter"><div data-typedoc-h="5"><span class="tsd-kind-property">name</span><span class="tsd-signature-symbol">: </span><span class="tsd-signature-type">string</span></div><div class="tsd-comment tsd-typography"><p>The name of the prompt or prompt template.</p> </div><div class="tsd-comment tsd-typography" /></li></ul></div><aside class="tsd-sources"><p>Overrides Request.params</p></aside></section>

### `GetPromptResult`

<div class="tsd-signature"><span class="tsd-signature-keyword">interface</span> <span class="tsd-kind-interface">GetPromptResult</span> <span class="tsd-signature-symbol">\{</span><br />  <a class="tsd-kind-property" href="#getpromptresult-_meta">\_meta</a><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-symbol">\{</span> <span class="tsd-signature-symbol">\[</span><span class="tsd-kind-index-signature">key</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">]:</span> <span class="tsd-signature-type">unknown</span> <span class="tsd-signature-symbol">}</span><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#getpromptresult-description">description</a><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#">messages</a><span class="tsd-signature-symbol">:</span> <a href="#promptmessage" class="tsd-signature-type tsd-kind-interface">PromptMessage</a><span class="tsd-signature-symbol">\[]</span><span class="tsd-signature-symbol">;</span><br />  <span class="tsd-signature-symbol">\[</span><span class="tsd-kind-index-signature">key</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">]:</span> <span class="tsd-signature-type">unknown</span><span class="tsd-signature-symbol">;</span><br /><span class="tsd-signature-symbol">}</span></div><div class="tsd-comment tsd-typography"><p>The server's response to a prompts/get request from the client.</p> </div><section class="tsd-panel tsd-member tsd-is-inherited"><div data-typedoc-h="3" class="tsd-anchor-link" id="getpromptresult-_meta"><code class="tsd-tag">Optional</code><span>\_<wbr />meta</span><a href="#getpromptresult-_meta" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">\_meta</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-symbol">\{</span> <span class="tsd-signature-symbol">\[</span><span class="tsd-kind-index-signature">key</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">]:</span> <span class="tsd-signature-type">unknown</span> <span class="tsd-signature-symbol">}</span></div><div class="tsd-comment tsd-typography"><p>See <a href="/specification/2025-06-18/basic/index#meta">General fields: <code>\_meta</code></a> for notes on <code>\_meta</code> usage.</p> </div><div class="tsd-comment tsd-typography" /><aside class="tsd-sources"><p>Inherited from <a href="#result">Result</a>.<a href="#result-_meta">\_meta</a></p></aside></section><section class="tsd-panel tsd-member"><div data-typedoc-h="3" class="tsd-anchor-link" id="getpromptresult-description"><code class="tsd-tag">Optional</code><span>description</span><a href="#getpromptresult-description" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">description</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">string</span></div><div class="tsd-comment tsd-typography"><p>An optional description for the prompt.</p> </div><div class="tsd-comment tsd-typography" /></section>

## `prompts/list`

### `ListPromptsRequest`

<div class="tsd-signature"><span class="tsd-signature-keyword">interface</span> <span class="tsd-kind-interface">ListPromptsRequest</span> <span class="tsd-signature-symbol">\{</span><br />  <a class="tsd-kind-property" href="#">method</a><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">"prompts/list"</span><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#listpromptsrequest-params">params</a><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-symbol">\{</span> <span class="tsd-kind-property">cursor</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">string</span> <span class="tsd-signature-symbol">}</span><span class="tsd-signature-symbol">;</span><br /><span class="tsd-signature-symbol">}</span></div><div class="tsd-comment tsd-typography"><p>Sent from the client to request a list of prompts and prompt templates the server has.</p> </div><section class="tsd-panel tsd-member tsd-is-inherited"><div data-typedoc-h="3" class="tsd-anchor-link" id="listpromptsrequest-params"><code class="tsd-tag">Optional</code><span>params</span><a href="#listpromptsrequest-params" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">params</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-symbol">\{</span> <span class="tsd-kind-property">cursor</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">string</span> <span class="tsd-signature-symbol">}</span></div><div class="tsd-type-declaration"><div data-typedoc-h="4">Type declaration</div><ul class="tsd-parameters"><li class="tsd-parameter"><div data-typedoc-h="5"><code class="tsd-tag">Optional</code><span class="tsd-kind-property">cursor</span><span class="tsd-signature-symbol">?: </span><span class="tsd-signature-type">string</span></div><div class="tsd-comment tsd-typography"><p>An opaque token representing the current pagination position.
If provided, the server should return results starting after this cursor.</p> </div><div class="tsd-comment tsd-typography" /></li></ul></div><aside class="tsd-sources"><p>Inherited from PaginatedRequest.params</p></aside></section>

### `ListPromptsResult`

<div class="tsd-signature"><span class="tsd-signature-keyword">interface</span> <span class="tsd-kind-interface">ListPromptsResult</span> <span class="tsd-signature-symbol">\{</span><br />  <a class="tsd-kind-property" href="#listpromptsresult-_meta">\_meta</a><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-symbol">\{</span> <span class="tsd-signature-symbol">\[</span><span class="tsd-kind-index-signature">key</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">]:</span> <span class="tsd-signature-type">unknown</span> <span class="tsd-signature-symbol">}</span><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#listpromptsresult-nextcursor">nextCursor</a><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#">prompts</a><span class="tsd-signature-symbol">:</span> <a href="#prompt" class="tsd-signature-type tsd-kind-interface">Prompt</a><span class="tsd-signature-symbol">\[]</span><span class="tsd-signature-symbol">;</span><br />  <span class="tsd-signature-symbol">\[</span><span class="tsd-kind-index-signature">key</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">]:</span> <span class="tsd-signature-type">unknown</span><span class="tsd-signature-symbol">;</span><br /><span class="tsd-signature-symbol">}</span></div><div class="tsd-comment tsd-typography"><p>The server's response to a prompts/list request from the client.</p> </div><section class="tsd-panel tsd-member tsd-is-inherited"><div data-typedoc-h="3" class="tsd-anchor-link" id="listpromptsresult-_meta"><code class="tsd-tag">Optional</code><span>\_<wbr />meta</span><a href="#listpromptsresult-_meta" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">\_meta</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-symbol">\{</span> <span class="tsd-signature-symbol">\[</span><span class="tsd-kind-index-signature">key</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">]:</span> <span class="tsd-signature-type">unknown</span> <span class="tsd-signature-symbol">}</span></div><div class="tsd-comment tsd-typography"><p>See <a href="/specification/2025-06-18/basic/index#meta">General fields: <code>\_meta</code></a> for notes on <code>\_meta</code> usage.</p> </div><div class="tsd-comment tsd-typography" /><aside class="tsd-sources"><p>Inherited from PaginatedResult.\_meta</p></aside></section><section class="tsd-panel tsd-member tsd-is-inherited"><div data-typedoc-h="3" class="tsd-anchor-link" id="listpromptsresult-nextcursor"><code class="tsd-tag">Optional</code><span>next<wbr />Cursor</span><a href="#listpromptsresult-nextcursor" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">nextCursor</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">string</span></div><div class="tsd-comment tsd-typography"><p>An opaque token representing the pagination position after the last returned result.
If present, there may be more results available.</p> </div><div class="tsd-comment tsd-typography" /><aside class="tsd-sources"><p>Inherited from PaginatedResult.nextCursor</p></aside></section>

## `resources/list`

### `ListResourcesRequest`

<div class="tsd-signature"><span class="tsd-signature-keyword">interface</span> <span class="tsd-kind-interface">ListResourcesRequest</span> <span class="tsd-signature-symbol">\{</span><br />  <a class="tsd-kind-property" href="#">method</a><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">"resources/list"</span><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#listresourcesrequest-params">params</a><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-symbol">\{</span> <span class="tsd-kind-property">cursor</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">string</span> <span class="tsd-signature-symbol">}</span><span class="tsd-signature-symbol">;</span><br /><span class="tsd-signature-symbol">}</span></div><div class="tsd-comment tsd-typography"><p>Sent from the client to request a list of resources the server has.</p> </div><section class="tsd-panel tsd-member tsd-is-inherited"><div data-typedoc-h="3" class="tsd-anchor-link" id="listresourcesrequest-params"><code class="tsd-tag">Optional</code><span>params</span><a href="#listresourcesrequest-params" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">params</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-symbol">\{</span> <span class="tsd-kind-property">cursor</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">string</span> <span class="tsd-signature-symbol">}</span></div><div class="tsd-type-declaration"><div data-typedoc-h="4">Type declaration</div><ul class="tsd-parameters"><li class="tsd-parameter"><div data-typedoc-h="5"><code class="tsd-tag">Optional</code><span class="tsd-kind-property">cursor</span><span class="tsd-signature-symbol">?: </span><span class="tsd-signature-type">string</span></div><div class="tsd-comment tsd-typography"><p>An opaque token representing the current pagination position.
If provided, the server should return results starting after this cursor.</p> </div><div class="tsd-comment tsd-typography" /></li></ul></div><aside class="tsd-sources"><p>Inherited from PaginatedRequest.params</p></aside></section>

### `ListResourcesResult`

<div class="tsd-signature"><span class="tsd-signature-keyword">interface</span> <span class="tsd-kind-interface">ListResourcesResult</span> <span class="tsd-signature-symbol">\{</span><br />  <a class="tsd-kind-property" href="#listresourcesresult-_meta">\_meta</a><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-symbol">\{</span> <span class="tsd-signature-symbol">\[</span><span class="tsd-kind-index-signature">key</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">]:</span> <span class="tsd-signature-type">unknown</span> <span class="tsd-signature-symbol">}</span><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#listresourcesresult-nextcursor">nextCursor</a><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#">resources</a><span class="tsd-signature-symbol">:</span> <a href="#resource" class="tsd-signature-type tsd-kind-interface">Resource</a><span class="tsd-signature-symbol">\[]</span><span class="tsd-signature-symbol">;</span><br />  <span class="tsd-signature-symbol">\[</span><span class="tsd-kind-index-signature">key</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">]:</span> <span class="tsd-signature-type">unknown</span><span class="tsd-signature-symbol">;</span><br /><span class="tsd-signature-symbol">}</span></div><div class="tsd-comment tsd-typography"><p>The server's response to a resources/list request from the client.</p> </div><section class="tsd-panel tsd-member tsd-is-inherited"><div data-typedoc-h="3" class="tsd-anchor-link" id="listresourcesresult-_meta"><code class="tsd-tag">Optional</code><span>\_<wbr />meta</span><a href="#listresourcesresult-_meta" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">\_meta</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-symbol">\{</span> <span class="tsd-signature-symbol">\[</span><span class="tsd-kind-index-signature">key</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">]:</span> <span class="tsd-signature-type">unknown</span> <span class="tsd-signature-symbol">}</span></div><div class="tsd-comment tsd-typography"><p>See <a href="/specification/2025-06-18/basic/index#meta">General fields: <code>\_meta</code></a> for notes on <code>\_meta</code> usage.</p> </div><div class="tsd-comment tsd-typography" /><aside class="tsd-sources"><p>Inherited from PaginatedResult.\_meta</p></aside></section><section class="tsd-panel tsd-member tsd-is-inherited"><div data-typedoc-h="3" class="tsd-anchor-link" id="listresourcesresult-nextcursor"><code class="tsd-tag">Optional</code><span>next<wbr />Cursor</span><a href="#listresourcesresult-nextcursor" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">nextCursor</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">string</span></div><div class="tsd-comment tsd-typography"><p>An opaque token representing the pagination position after the last returned result.
If present, there may be more results available.</p> </div><div class="tsd-comment tsd-typography" /><aside class="tsd-sources"><p>Inherited from PaginatedResult.nextCursor</p></aside></section>

## `resources/read`

### `ReadResourceRequest`

<div class="tsd-signature"><span class="tsd-signature-keyword">interface</span> <span class="tsd-kind-interface">ReadResourceRequest</span> <span class="tsd-signature-symbol">\{</span><br />  <a class="tsd-kind-property" href="#">method</a><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">"resources/read"</span><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#readresourcerequest-params">params</a><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-symbol">\{</span> <span class="tsd-kind-property">uri</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span> <span class="tsd-signature-symbol">}</span><span class="tsd-signature-symbol">;</span><br /><span class="tsd-signature-symbol">}</span></div><div class="tsd-comment tsd-typography"><p>Sent from the client to the server, to read a specific resource URI.</p> </div><section class="tsd-panel tsd-member"><div data-typedoc-h="3" class="tsd-anchor-link" id="readresourcerequest-params"><span>params</span><a href="#readresourcerequest-params" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">params</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-symbol">\{</span> <span class="tsd-kind-property">uri</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span> <span class="tsd-signature-symbol">}</span></div><div class="tsd-type-declaration"><div data-typedoc-h="4">Type declaration</div><ul class="tsd-parameters"><li class="tsd-parameter"><div data-typedoc-h="5"><span class="tsd-kind-property">uri</span><span class="tsd-signature-symbol">: </span><span class="tsd-signature-type">string</span></div><div class="tsd-comment tsd-typography"><p>The URI of the resource to read. The URI can use any protocol; it is up to the server how to interpret it.</p> </div><div class="tsd-comment tsd-typography" /></li></ul></div><aside class="tsd-sources"><p>Overrides Request.params</p></aside></section>

### `ReadResourceResult`

<div class="tsd-signature"><span class="tsd-signature-keyword">interface</span> <span class="tsd-kind-interface">ReadResourceResult</span> <span class="tsd-signature-symbol">\{</span><br />  <a class="tsd-kind-property" href="#readresourceresult-_meta">\_meta</a><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-symbol">\{</span> <span class="tsd-signature-symbol">\[</span><span class="tsd-kind-index-signature">key</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">]:</span> <span class="tsd-signature-type">unknown</span> <span class="tsd-signature-symbol">}</span><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#">contents</a><span class="tsd-signature-symbol">:</span> (<a href="#textresourcecontents" class="tsd-signature-type tsd-kind-interface">TextResourceContents</a> <span class="tsd-signature-symbol">|</span> <a href="#blobresourcecontents" class="tsd-signature-type tsd-kind-interface">BlobResourceContents</a>)<span class="tsd-signature-symbol">\[]</span><span class="tsd-signature-symbol">;</span><br />  <span class="tsd-signature-symbol">\[</span><span class="tsd-kind-index-signature">key</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">]:</span> <span class="tsd-signature-type">unknown</span><span class="tsd-signature-symbol">;</span><br /><span class="tsd-signature-symbol">}</span></div><div class="tsd-comment tsd-typography"><p>The server's response to a resources/read request from the client.</p> </div><section class="tsd-panel tsd-member tsd-is-inherited"><div data-typedoc-h="3" class="tsd-anchor-link" id="readresourceresult-_meta"><code class="tsd-tag">Optional</code><span>\_<wbr />meta</span><a href="#readresourceresult-_meta" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">\_meta</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-symbol">\{</span> <span class="tsd-signature-symbol">\[</span><span class="tsd-kind-index-signature">key</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">]:</span> <span class="tsd-signature-type">unknown</span> <span class="tsd-signature-symbol">}</span></div><div class="tsd-comment tsd-typography"><p>See <a href="/specification/2025-06-18/basic/index#meta">General fields: <code>\_meta</code></a> for notes on <code>\_meta</code> usage.</p> </div><div class="tsd-comment tsd-typography" /><aside class="tsd-sources"><p>Inherited from <a href="#result">Result</a>.<a href="#result-_meta">\_meta</a></p></aside></section>

## `resources/subscribe`

### `SubscribeRequest`

<div class="tsd-signature"><span class="tsd-signature-keyword">interface</span> <span class="tsd-kind-interface">SubscribeRequest</span> <span class="tsd-signature-symbol">\{</span><br />  <a class="tsd-kind-property" href="#">method</a><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">"resources/subscribe"</span><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#subscriberequest-params">params</a><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-symbol">\{</span> <span class="tsd-kind-property">uri</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span> <span class="tsd-signature-symbol">}</span><span class="tsd-signature-symbol">;</span><br /><span class="tsd-signature-symbol">}</span></div><div class="tsd-comment tsd-typography"><p>Sent from the client to request resources/updated notifications from the server whenever a particular resource changes.</p> </div><section class="tsd-panel tsd-member"><div data-typedoc-h="3" class="tsd-anchor-link" id="subscriberequest-params"><span>params</span><a href="#subscriberequest-params" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">params</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-symbol">\{</span> <span class="tsd-kind-property">uri</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span> <span class="tsd-signature-symbol">}</span></div><div class="tsd-type-declaration"><div data-typedoc-h="4">Type declaration</div><ul class="tsd-parameters"><li class="tsd-parameter"><div data-typedoc-h="5"><span class="tsd-kind-property">uri</span><span class="tsd-signature-symbol">: </span><span class="tsd-signature-type">string</span></div><div class="tsd-comment tsd-typography"><p>The URI of the resource to subscribe to. The URI can use any protocol; it is up to the server how to interpret it.</p> </div><div class="tsd-comment tsd-typography" /></li></ul></div><aside class="tsd-sources"><p>Overrides Request.params</p></aside></section>

## `resources/templates/list`

### `ListResourceTemplatesRequest`

<div class="tsd-signature"><span class="tsd-signature-keyword">interface</span> <span class="tsd-kind-interface">ListResourceTemplatesRequest</span> <span class="tsd-signature-symbol">\{</span><br />  <a class="tsd-kind-property" href="#">method</a><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">"resources/templates/list"</span><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#listresourcetemplatesrequest-params">params</a><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-symbol">\{</span> <span class="tsd-kind-property">cursor</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">string</span> <span class="tsd-signature-symbol">}</span><span class="tsd-signature-symbol">;</span><br /><span class="tsd-signature-symbol">}</span></div><div class="tsd-comment tsd-typography"><p>Sent from the client to request a list of resource templates the server has.</p> </div><section class="tsd-panel tsd-member tsd-is-inherited"><div data-typedoc-h="3" class="tsd-anchor-link" id="listresourcetemplatesrequest-params"><code class="tsd-tag">Optional</code><span>params</span><a href="#listresourcetemplatesrequest-params" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">params</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-symbol">\{</span> <span class="tsd-kind-property">cursor</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">string</span> <span class="tsd-signature-symbol">}</span></div><div class="tsd-type-declaration"><div data-typedoc-h="4">Type declaration</div><ul class="tsd-parameters"><li class="tsd-parameter"><div data-typedoc-h="5"><code class="tsd-tag">Optional</code><span class="tsd-kind-property">cursor</span><span class="tsd-signature-symbol">?: </span><span class="tsd-signature-type">string</span></div><div class="tsd-comment tsd-typography"><p>An opaque token representing the current pagination position.
If provided, the server should return results starting after this cursor.</p> </div><div class="tsd-comment tsd-typography" /></li></ul></div><aside class="tsd-sources"><p>Inherited from PaginatedRequest.params</p></aside></section>

### `ListResourceTemplatesResult`

<div class="tsd-signature"><span class="tsd-signature-keyword">interface</span> <span class="tsd-kind-interface">ListResourceTemplatesResult</span> <span class="tsd-signature-symbol">\{</span><br />  <a class="tsd-kind-property" href="#listresourcetemplatesresult-_meta">\_meta</a><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-symbol">\{</span> <span class="tsd-signature-symbol">\[</span><span class="tsd-kind-index-signature">key</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">]:</span> <span class="tsd-signature-type">unknown</span> <span class="tsd-signature-symbol">}</span><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#listresourcetemplatesresult-nextcursor">nextCursor</a><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#">resourceTemplates</a><span class="tsd-signature-symbol">:</span> <a href="#resourcetemplate" class="tsd-signature-type tsd-kind-interface">ResourceTemplate</a><span class="tsd-signature-symbol">\[]</span><span class="tsd-signature-symbol">;</span><br />  <span class="tsd-signature-symbol">\[</span><span class="tsd-kind-index-signature">key</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">]:</span> <span class="tsd-signature-type">unknown</span><span class="tsd-signature-symbol">;</span><br /><span class="tsd-signature-symbol">}</span></div><div class="tsd-comment tsd-typography"><p>The server's response to a resources/templates/list request from the client.</p> </div><section class="tsd-panel tsd-member tsd-is-inherited"><div data-typedoc-h="3" class="tsd-anchor-link" id="listresourcetemplatesresult-_meta"><code class="tsd-tag">Optional</code><span>\_<wbr />meta</span><a href="#listresourcetemplatesresult-_meta" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">\_meta</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-symbol">\{</span> <span class="tsd-signature-symbol">\[</span><span class="tsd-kind-index-signature">key</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">]:</span> <span class="tsd-signature-type">unknown</span> <span class="tsd-signature-symbol">}</span></div><div class="tsd-comment tsd-typography"><p>See <a href="/specification/2025-06-18/basic/index#meta">General fields: <code>\_meta</code></a> for notes on <code>\_meta</code> usage.</p> </div><div class="tsd-comment tsd-typography" /><aside class="tsd-sources"><p>Inherited from PaginatedResult.\_meta</p></aside></section><section class="tsd-panel tsd-member tsd-is-inherited"><div data-typedoc-h="3" class="tsd-anchor-link" id="listresourcetemplatesresult-nextcursor"><code class="tsd-tag">Optional</code><span>next<wbr />Cursor</span><a href="#listresourcetemplatesresult-nextcursor" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">nextCursor</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">string</span></div><div class="tsd-comment tsd-typography"><p>An opaque token representing the pagination position after the last returned result.
If present, there may be more results available.</p> </div><div class="tsd-comment tsd-typography" /><aside class="tsd-sources"><p>Inherited from PaginatedResult.nextCursor</p></aside></section>

## `resources/unsubscribe`

### `UnsubscribeRequest`

<div class="tsd-signature"><span class="tsd-signature-keyword">interface</span> <span class="tsd-kind-interface">UnsubscribeRequest</span> <span class="tsd-signature-symbol">\{</span><br />  <a class="tsd-kind-property" href="#">method</a><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">"resources/unsubscribe"</span><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#unsubscriberequest-params">params</a><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-symbol">\{</span> <span class="tsd-kind-property">uri</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span> <span class="tsd-signature-symbol">}</span><span class="tsd-signature-symbol">;</span><br /><span class="tsd-signature-symbol">}</span></div><div class="tsd-comment tsd-typography"><p>Sent from the client to request cancellation of resources/updated notifications from the server. This should follow a previous resources/subscribe request.</p> </div><section class="tsd-panel tsd-member"><div data-typedoc-h="3" class="tsd-anchor-link" id="unsubscriberequest-params"><span>params</span><a href="#unsubscriberequest-params" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">params</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-symbol">\{</span> <span class="tsd-kind-property">uri</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span> <span class="tsd-signature-symbol">}</span></div><div class="tsd-type-declaration"><div data-typedoc-h="4">Type declaration</div><ul class="tsd-parameters"><li class="tsd-parameter"><div data-typedoc-h="5"><span class="tsd-kind-property">uri</span><span class="tsd-signature-symbol">: </span><span class="tsd-signature-type">string</span></div><div class="tsd-comment tsd-typography"><p>The URI of the resource to unsubscribe from.</p> </div><div class="tsd-comment tsd-typography" /></li></ul></div><aside class="tsd-sources"><p>Overrides Request.params</p></aside></section>

## `roots/list`

### `ListRootsRequest`

<div class="tsd-signature"><span class="tsd-signature-keyword">interface</span> <span class="tsd-kind-interface">ListRootsRequest</span> <span class="tsd-signature-symbol">\{</span><br />  <a class="tsd-kind-property" href="#">method</a><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">"roots/list"</span><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#listrootsrequest-params">params</a><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-symbol">\{</span><br />    <span class="tsd-kind-property">\_meta</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-symbol">\{</span> <span class="tsd-kind-property">progressToken</span><span class="tsd-signature-symbol">?:</span> <a href="#progresstoken" class="tsd-signature-type tsd-kind-type-alias">ProgressToken</a><span class="tsd-signature-symbol">;</span> <span class="tsd-signature-symbol">\[</span><span class="tsd-kind-index-signature">key</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">]:</span> <span class="tsd-signature-type">unknown</span> <span class="tsd-signature-symbol">}</span><span class="tsd-signature-symbol">;</span><br />    <span class="tsd-signature-symbol">\[</span><span class="tsd-kind-index-signature">key</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">]:</span> <span class="tsd-signature-type">unknown</span><span class="tsd-signature-symbol">;</span><br />  <span class="tsd-signature-symbol">}</span><span class="tsd-signature-symbol">;</span><br /><span class="tsd-signature-symbol">}</span></div><div class="tsd-comment tsd-typography"><p>Sent from the server to request a list of root URIs from the client. Roots allow
servers to ask for specific directories or files to operate on. A common example
for roots is providing a set of repositories or directories a server should operate
on.</p> <p>This request is typically used when the server needs to understand the file system
structure or access specific locations that the client has permission to read from.</p> </div><section class="tsd-panel tsd-member tsd-is-inherited"><div data-typedoc-h="3" class="tsd-anchor-link" id="listrootsrequest-params"><code class="tsd-tag">Optional</code><span>params</span><a href="#listrootsrequest-params" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">params</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-symbol">\{</span><br />  <span class="tsd-kind-property">\_meta</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-symbol">\{</span> <span class="tsd-kind-property">progressToken</span><span class="tsd-signature-symbol">?:</span> <a href="#progresstoken" class="tsd-signature-type tsd-kind-type-alias">ProgressToken</a><span class="tsd-signature-symbol">;</span> <span class="tsd-signature-symbol">\[</span><span class="tsd-kind-index-signature">key</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">]:</span> <span class="tsd-signature-type">unknown</span> <span class="tsd-signature-symbol">}</span><span class="tsd-signature-symbol">;</span><br />  <span class="tsd-signature-symbol">\[</span><span class="tsd-kind-index-signature">key</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">]:</span> <span class="tsd-signature-type">unknown</span><span class="tsd-signature-symbol">;</span><br /><span class="tsd-signature-symbol">}</span></div><div class="tsd-type-declaration"><div data-typedoc-h="4">Type declaration</div><ul class="tsd-parameters"><li class="tsd-parameter-index-signature"><div data-typedoc-h="5"><span class="tsd-signature-symbol">\[</span><span class="tsd-kind-parameter">key</span>: <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">]:</span> <span class="tsd-signature-type">unknown</span></div></li><li class="tsd-parameter"><div data-typedoc-h="5"><code class="tsd-tag">Optional</code><span class="tsd-kind-property">\_meta</span><span class="tsd-signature-symbol">?: </span><span class="tsd-signature-symbol">\{</span> <span class="tsd-kind-property">progressToken</span><span class="tsd-signature-symbol">?:</span> <a href="#progresstoken" class="tsd-signature-type tsd-kind-type-alias">ProgressToken</a><span class="tsd-signature-symbol">;</span> <span class="tsd-signature-symbol">\[</span><span class="tsd-kind-index-signature">key</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">]:</span> <span class="tsd-signature-type">unknown</span> <span class="tsd-signature-symbol">}</span></div><div class="tsd-comment tsd-typography"><p>See <a href="/specification/2025-06-18/basic/index#meta">General fields: <code>\_meta</code></a> for notes on <code>\_meta</code> usage.</p> </div><div class="tsd-comment tsd-typography" /><ul class="tsd-parameters"><li class="tsd-parameter"><div data-typedoc-h="5"><code class="tsd-tag">Optional</code><span class="tsd-kind-property">progressToken</span><span class="tsd-signature-symbol">?: </span><a href="#progresstoken" class="tsd-signature-type tsd-kind-type-alias">ProgressToken</a></div><div class="tsd-comment tsd-typography"><p>If specified, the caller is requesting out-of-band progress notifications for this request (as represented by notifications/progress). The value of this parameter is an opaque token that will be attached to any subsequent notifications. The receiver is not obligated to provide these notifications.</p> </div><div class="tsd-comment tsd-typography" /></li></ul></li></ul></div><aside class="tsd-sources"><p>Inherited from Request.params</p></aside></section>

### `ListRootsResult`

<div class="tsd-signature"><span class="tsd-signature-keyword">interface</span> <span class="tsd-kind-interface">ListRootsResult</span> <span class="tsd-signature-symbol">\{</span><br />  <a class="tsd-kind-property" href="#listrootsresult-_meta">\_meta</a><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-symbol">\{</span> <span class="tsd-signature-symbol">\[</span><span class="tsd-kind-index-signature">key</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">]:</span> <span class="tsd-signature-type">unknown</span> <span class="tsd-signature-symbol">}</span><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#">roots</a><span class="tsd-signature-symbol">:</span> <a href="#root" class="tsd-signature-type tsd-kind-interface">Root</a><span class="tsd-signature-symbol">\[]</span><span class="tsd-signature-symbol">;</span><br />  <span class="tsd-signature-symbol">\[</span><span class="tsd-kind-index-signature">key</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">]:</span> <span class="tsd-signature-type">unknown</span><span class="tsd-signature-symbol">;</span><br /><span class="tsd-signature-symbol">}</span></div><div class="tsd-comment tsd-typography"><p>The client's response to a roots/list request from the server.
This result contains an array of Root objects, each representing a root directory
or file that the server can operate on.</p> </div><section class="tsd-panel tsd-member tsd-is-inherited"><div data-typedoc-h="3" class="tsd-anchor-link" id="listrootsresult-_meta"><code class="tsd-tag">Optional</code><span>\_<wbr />meta</span><a href="#listrootsresult-_meta" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">\_meta</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-symbol">\{</span> <span class="tsd-signature-symbol">\[</span><span class="tsd-kind-index-signature">key</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">]:</span> <span class="tsd-signature-type">unknown</span> <span class="tsd-signature-symbol">}</span></div><div class="tsd-comment tsd-typography"><p>See <a href="/specification/2025-06-18/basic/index#meta">General fields: <code>\_meta</code></a> for notes on <code>\_meta</code> usage.</p> </div><div class="tsd-comment tsd-typography" /><aside class="tsd-sources"><p>Inherited from <a href="#result">Result</a>.<a href="#result-_meta">\_meta</a></p></aside></section>

## `sampling/createMessage`

### `CreateMessageRequest`

<div class="tsd-signature"><span class="tsd-signature-keyword">interface</span> <span class="tsd-kind-interface">CreateMessageRequest</span> <span class="tsd-signature-symbol">\{</span><br />  <a class="tsd-kind-property" href="#">method</a><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">"sampling/createMessage"</span><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#createmessagerequest-params">params</a><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-symbol">\{</span><br />    <span class="tsd-kind-property">includeContext</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">"none"</span> <span class="tsd-signature-symbol">|</span> <span class="tsd-signature-type">"thisServer"</span> <span class="tsd-signature-symbol">|</span> <span class="tsd-signature-type">"allServers"</span><span class="tsd-signature-symbol">;</span><br />    <span class="tsd-kind-property">maxTokens</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">number</span><span class="tsd-signature-symbol">;</span><br />    <span class="tsd-kind-property">messages</span><span class="tsd-signature-symbol">:</span> <a href="#samplingmessage" class="tsd-signature-type tsd-kind-interface">SamplingMessage</a><span class="tsd-signature-symbol">\[]</span><span class="tsd-signature-symbol">;</span><br />    <span class="tsd-kind-property">metadata</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">object</span><span class="tsd-signature-symbol">;</span><br />    <span class="tsd-kind-property">modelPreferences</span><span class="tsd-signature-symbol">?:</span> <a href="#modelpreferences" class="tsd-signature-type tsd-kind-interface">ModelPreferences</a><span class="tsd-signature-symbol">;</span><br />    <span class="tsd-kind-property">stopSequences</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">\[]</span><span class="tsd-signature-symbol">;</span><br />    <span class="tsd-kind-property">systemPrompt</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">;</span><br />    <span class="tsd-kind-property">temperature</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">number</span><span class="tsd-signature-symbol">;</span><br />  <span class="tsd-signature-symbol">}</span><span class="tsd-signature-symbol">;</span><br /><span class="tsd-signature-symbol">}</span></div><div class="tsd-comment tsd-typography"><p>A request from the server to sample an LLM via the client. The client has full discretion over which model to select. The client should also inform the user before beginning sampling, to allow them to inspect the request (human in the loop) and decide whether to approve it.</p> </div><section class="tsd-panel tsd-member"><div data-typedoc-h="3" class="tsd-anchor-link" id="createmessagerequest-params"><span>params</span><a href="#createmessagerequest-params" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">params</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-symbol">\{</span><br />  <span class="tsd-kind-property">includeContext</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">"none"</span> <span class="tsd-signature-symbol">|</span> <span class="tsd-signature-type">"thisServer"</span> <span class="tsd-signature-symbol">|</span> <span class="tsd-signature-type">"allServers"</span><span class="tsd-signature-symbol">;</span><br />  <span class="tsd-kind-property">maxTokens</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">number</span><span class="tsd-signature-symbol">;</span><br />  <span class="tsd-kind-property">messages</span><span class="tsd-signature-symbol">:</span> <a href="#samplingmessage" class="tsd-signature-type tsd-kind-interface">SamplingMessage</a><span class="tsd-signature-symbol">\[]</span><span class="tsd-signature-symbol">;</span><br />  <span class="tsd-kind-property">metadata</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">object</span><span class="tsd-signature-symbol">;</span><br />  <span class="tsd-kind-property">modelPreferences</span><span class="tsd-signature-symbol">?:</span> <a href="#modelpreferences" class="tsd-signature-type tsd-kind-interface">ModelPreferences</a><span class="tsd-signature-symbol">;</span><br />  <span class="tsd-kind-property">stopSequences</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">\[]</span><span class="tsd-signature-symbol">;</span><br />  <span class="tsd-kind-property">systemPrompt</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">;</span><br />  <span class="tsd-kind-property">temperature</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">number</span><span class="tsd-signature-symbol">;</span><br /><span class="tsd-signature-symbol">}</span></div><div class="tsd-type-declaration"><div data-typedoc-h="4">Type declaration</div><ul class="tsd-parameters"><li class="tsd-parameter"><div data-typedoc-h="5"><code class="tsd-tag">Optional</code><span class="tsd-kind-property">includeContext</span><span class="tsd-signature-symbol">?: </span><span class="tsd-signature-type">"none"</span> <span class="tsd-signature-symbol">|</span> <span class="tsd-signature-type">"thisServer"</span> <span class="tsd-signature-symbol">|</span> <span class="tsd-signature-type">"allServers"</span></div><div class="tsd-comment tsd-typography"><p>A request to include context from one or more MCP servers (including the caller), to be attached to the prompt. The client MAY ignore this request.</p> </div><div class="tsd-comment tsd-typography" /></li><li class="tsd-parameter"><div data-typedoc-h="5"><span class="tsd-kind-property">maxTokens</span><span class="tsd-signature-symbol">: </span><span class="tsd-signature-type">number</span></div><div class="tsd-comment tsd-typography"><p>The maximum number of tokens to sample, as requested by the server. The client MAY choose to sample fewer tokens than requested.</p> </div><div class="tsd-comment tsd-typography" /></li><li class="tsd-parameter"><div data-typedoc-h="5"><span class="tsd-kind-property">messages</span><span class="tsd-signature-symbol">: </span><a href="#samplingmessage" class="tsd-signature-type tsd-kind-interface">SamplingMessage</a><span class="tsd-signature-symbol">\[]</span></div></li><li class="tsd-parameter"><div data-typedoc-h="5"><code class="tsd-tag">Optional</code><span class="tsd-kind-property">metadata</span><span class="tsd-signature-symbol">?: </span><span class="tsd-signature-type">object</span></div><div class="tsd-comment tsd-typography"><p>Optional metadata to pass through to the LLM provider. The format of this metadata is provider-specific.</p> </div><div class="tsd-comment tsd-typography" /></li><li class="tsd-parameter"><div data-typedoc-h="5"><code class="tsd-tag">Optional</code><span class="tsd-kind-property">modelPreferences</span><span class="tsd-signature-symbol">?: </span><a href="#modelpreferences" class="tsd-signature-type tsd-kind-interface">ModelPreferences</a></div><div class="tsd-comment tsd-typography"><p>The server's preferences for which model to select. The client MAY ignore these preferences.</p> </div><div class="tsd-comment tsd-typography" /></li><li class="tsd-parameter"><div data-typedoc-h="5"><code class="tsd-tag">Optional</code><span class="tsd-kind-property">stopSequences</span><span class="tsd-signature-symbol">?: </span><span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">\[]</span></div></li><li class="tsd-parameter"><div data-typedoc-h="5"><code class="tsd-tag">Optional</code><span class="tsd-kind-property">systemPrompt</span><span class="tsd-signature-symbol">?: </span><span class="tsd-signature-type">string</span></div><div class="tsd-comment tsd-typography"><p>An optional system prompt the server wants to use for sampling. The client MAY modify or omit this prompt.</p> </div><div class="tsd-comment tsd-typography" /></li><li class="tsd-parameter"><div data-typedoc-h="5"><code class="tsd-tag">Optional</code><span class="tsd-kind-property">temperature</span><span class="tsd-signature-symbol">?: </span><span class="tsd-signature-type">number</span></div><div class="tsd-comment tsd-typography"><div class="tsd-tag-TJS-type"><div data-typedoc-h="4" class="tsd-anchor-link" id="tjs-type">TJS-type<a href="#tjs-type" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><p>number</p> </div></div></li></ul></div><aside class="tsd-sources"><p>Overrides Request.params</p></aside></section>

### `CreateMessageResult`

<div class="tsd-signature"><span class="tsd-signature-keyword">interface</span> <span class="tsd-kind-interface">CreateMessageResult</span> <span class="tsd-signature-symbol">\{</span><br />  <a class="tsd-kind-property" href="#createmessageresult-_meta">\_meta</a><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-symbol">\{</span> <span class="tsd-signature-symbol">\[</span><span class="tsd-kind-index-signature">key</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">]:</span> <span class="tsd-signature-type">unknown</span> <span class="tsd-signature-symbol">}</span><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#">content</a><span class="tsd-signature-symbol">:</span> <a href="#textcontent" class="tsd-signature-type tsd-kind-interface">TextContent</a> <span class="tsd-signature-symbol">|</span> <a href="#imagecontent" class="tsd-signature-type tsd-kind-interface">ImageContent</a> <span class="tsd-signature-symbol">|</span> <a href="#audiocontent" class="tsd-signature-type tsd-kind-interface">AudioContent</a><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#createmessageresult-model">model</a><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#">role</a><span class="tsd-signature-symbol">:</span> <a href="#role" class="tsd-signature-type tsd-kind-type-alias">Role</a><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#createmessageresult-stopreason">stopReason</a><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">;</span><br />  <span class="tsd-signature-symbol">\[</span><span class="tsd-kind-index-signature">key</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">]:</span> <span class="tsd-signature-type">unknown</span><span class="tsd-signature-symbol">;</span><br /><span class="tsd-signature-symbol">}</span></div><div class="tsd-comment tsd-typography"><p>The client's response to a sampling/create\_message request from the server. The client should inform the user before returning the sampled message, to allow them to inspect the response (human in the loop) and decide whether to allow the server to see it.</p> </div><section class="tsd-panel tsd-member tsd-is-inherited"><div data-typedoc-h="3" class="tsd-anchor-link" id="createmessageresult-_meta"><code class="tsd-tag">Optional</code><span>\_<wbr />meta</span><a href="#createmessageresult-_meta" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">\_meta</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-symbol">\{</span> <span class="tsd-signature-symbol">\[</span><span class="tsd-kind-index-signature">key</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">]:</span> <span class="tsd-signature-type">unknown</span> <span class="tsd-signature-symbol">}</span></div><div class="tsd-comment tsd-typography"><p>See <a href="/specification/2025-06-18/basic/index#meta">General fields: <code>\_meta</code></a> for notes on <code>\_meta</code> usage.</p> </div><div class="tsd-comment tsd-typography" /><aside class="tsd-sources"><p>Inherited from <a href="#result">Result</a>.<a href="#result-_meta">\_meta</a></p></aside></section><section class="tsd-panel tsd-member"><div data-typedoc-h="3" class="tsd-anchor-link" id="createmessageresult-model"><span>model</span><a href="#createmessageresult-model" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">model</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span></div><div class="tsd-comment tsd-typography"><p>The name of the model that generated the message.</p> </div><div class="tsd-comment tsd-typography" /></section><section class="tsd-panel tsd-member"><div data-typedoc-h="3" class="tsd-anchor-link" id="createmessageresult-stopreason"><code class="tsd-tag">Optional</code><span>stop<wbr />Reason</span><a href="#createmessageresult-stopreason" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">stopReason</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">string</span></div><div class="tsd-comment tsd-typography"><p>The reason why sampling stopped, if known.</p> </div><div class="tsd-comment tsd-typography" /></section>

## `tools/call`

### `CallToolRequest`

<div class="tsd-signature"><span class="tsd-signature-keyword">interface</span> <span class="tsd-kind-interface">CallToolRequest</span> <span class="tsd-signature-symbol">\{</span><br />  <a class="tsd-kind-property" href="#">method</a><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">"tools/call"</span><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#">params</a><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-symbol">\{</span> <span class="tsd-kind-property">arguments</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-symbol">\{</span> <span class="tsd-signature-symbol">\[</span><span class="tsd-kind-index-signature">key</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">]:</span> <span class="tsd-signature-type">unknown</span> <span class="tsd-signature-symbol">}</span><span class="tsd-signature-symbol">;</span> <span class="tsd-kind-property">name</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span> <span class="tsd-signature-symbol">}</span><span class="tsd-signature-symbol">;</span><br /><span class="tsd-signature-symbol">}</span></div><div class="tsd-comment tsd-typography"><p>Used by the client to invoke a tool provided by the server.</p> </div>

### `CallToolResult`

<div class="tsd-signature"><span class="tsd-signature-keyword">interface</span> <span class="tsd-kind-interface">CallToolResult</span> <span class="tsd-signature-symbol">\{</span><br />  <a class="tsd-kind-property" href="#calltoolresult-_meta">\_meta</a><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-symbol">\{</span> <span class="tsd-signature-symbol">\[</span><span class="tsd-kind-index-signature">key</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">]:</span> <span class="tsd-signature-type">unknown</span> <span class="tsd-signature-symbol">}</span><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#calltoolresult-content">content</a><span class="tsd-signature-symbol">:</span> <a href="#contentblock" class="tsd-signature-type tsd-kind-type-alias">ContentBlock</a><span class="tsd-signature-symbol">\[]</span><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#calltoolresult-iserror">isError</a><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">boolean</span><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#calltoolresult-structuredcontent">structuredContent</a><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-symbol">\{</span> <span class="tsd-signature-symbol">\[</span><span class="tsd-kind-index-signature">key</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">]:</span> <span class="tsd-signature-type">unknown</span> <span class="tsd-signature-symbol">}</span><span class="tsd-signature-symbol">;</span><br />  <span class="tsd-signature-symbol">\[</span><span class="tsd-kind-index-signature">key</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">]:</span> <span class="tsd-signature-type">unknown</span><span class="tsd-signature-symbol">;</span><br /><span class="tsd-signature-symbol">}</span></div><div class="tsd-comment tsd-typography"><p>The server's response to a tool call.</p> </div><section class="tsd-panel tsd-member tsd-is-inherited"><div data-typedoc-h="3" class="tsd-anchor-link" id="calltoolresult-_meta"><code class="tsd-tag">Optional</code><span>\_<wbr />meta</span><a href="#calltoolresult-_meta" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">\_meta</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-symbol">\{</span> <span class="tsd-signature-symbol">\[</span><span class="tsd-kind-index-signature">key</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">]:</span> <span class="tsd-signature-type">unknown</span> <span class="tsd-signature-symbol">}</span></div><div class="tsd-comment tsd-typography"><p>See <a href="/specification/2025-06-18/basic/index#meta">General fields: <code>\_meta</code></a> for notes on <code>\_meta</code> usage.</p> </div><div class="tsd-comment tsd-typography" /><aside class="tsd-sources"><p>Inherited from <a href="#result">Result</a>.<a href="#result-_meta">\_meta</a></p></aside></section><section class="tsd-panel tsd-member"><div data-typedoc-h="3" class="tsd-anchor-link" id="calltoolresult-content"><span>content</span><a href="#calltoolresult-content" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">content</span><span class="tsd-signature-symbol">:</span> <a href="#contentblock" class="tsd-signature-type tsd-kind-type-alias">ContentBlock</a><span class="tsd-signature-symbol">\[]</span></div><div class="tsd-comment tsd-typography"><p>A list of content objects that represent the unstructured result of the tool call.</p> </div><div class="tsd-comment tsd-typography" /></section><section class="tsd-panel tsd-member"><div data-typedoc-h="3" class="tsd-anchor-link" id="calltoolresult-iserror"><code class="tsd-tag">Optional</code><span>is<wbr />Error</span><a href="#calltoolresult-iserror" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">isError</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">boolean</span></div><div class="tsd-comment tsd-typography"><p>Whether the tool call ended in an error.</p> <p>If not set, this is assumed to be false (the call was successful).</p> <p>Any errors that originate from the tool SHOULD be reported inside the result
object, with <code>isError</code> set to true, <em>not</em> as an MCP protocol-level error
response. Otherwise, the LLM would not be able to see that an error occurred
and self-correct.</p> <p>However, any errors in <em>finding</em> the tool, an error indicating that the
server does not support tool calls, or any other exceptional conditions,
should be reported as an MCP error response.</p> </div><div class="tsd-comment tsd-typography" /></section><section class="tsd-panel tsd-member"><div data-typedoc-h="3" class="tsd-anchor-link" id="calltoolresult-structuredcontent"><code class="tsd-tag">Optional</code><span>structured<wbr />Content</span><a href="#calltoolresult-structuredcontent" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">structuredContent</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-symbol">\{</span> <span class="tsd-signature-symbol">\[</span><span class="tsd-kind-index-signature">key</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">]:</span> <span class="tsd-signature-type">unknown</span> <span class="tsd-signature-symbol">}</span></div><div class="tsd-comment tsd-typography"><p>An optional JSON object that represents the structured result of the tool call.</p> </div><div class="tsd-comment tsd-typography" /></section>

## `tools/list`

### `ListToolsRequest`

<div class="tsd-signature"><span class="tsd-signature-keyword">interface</span> <span class="tsd-kind-interface">ListToolsRequest</span> <span class="tsd-signature-symbol">\{</span><br />  <a class="tsd-kind-property" href="#">method</a><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">"tools/list"</span><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#listtoolsrequest-params">params</a><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-symbol">\{</span> <span class="tsd-kind-property">cursor</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">string</span> <span class="tsd-signature-symbol">}</span><span class="tsd-signature-symbol">;</span><br /><span class="tsd-signature-symbol">}</span></div><div class="tsd-comment tsd-typography"><p>Sent from the client to request a list of tools the server has.</p> </div><section class="tsd-panel tsd-member tsd-is-inherited"><div data-typedoc-h="3" class="tsd-anchor-link" id="listtoolsrequest-params"><code class="tsd-tag">Optional</code><span>params</span><a href="#listtoolsrequest-params" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">params</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-symbol">\{</span> <span class="tsd-kind-property">cursor</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">string</span> <span class="tsd-signature-symbol">}</span></div><div class="tsd-type-declaration"><div data-typedoc-h="4">Type declaration</div><ul class="tsd-parameters"><li class="tsd-parameter"><div data-typedoc-h="5"><code class="tsd-tag">Optional</code><span class="tsd-kind-property">cursor</span><span class="tsd-signature-symbol">?: </span><span class="tsd-signature-type">string</span></div><div class="tsd-comment tsd-typography"><p>An opaque token representing the current pagination position.
If provided, the server should return results starting after this cursor.</p> </div><div class="tsd-comment tsd-typography" /></li></ul></div><aside class="tsd-sources"><p>Inherited from PaginatedRequest.params</p></aside></section>

### `ListToolsResult`

<div class="tsd-signature"><span class="tsd-signature-keyword">interface</span> <span class="tsd-kind-interface">ListToolsResult</span> <span class="tsd-signature-symbol">\{</span><br />  <a class="tsd-kind-property" href="#listtoolsresult-_meta">\_meta</a><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-symbol">\{</span> <span class="tsd-signature-symbol">\[</span><span class="tsd-kind-index-signature">key</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">]:</span> <span class="tsd-signature-type">unknown</span> <span class="tsd-signature-symbol">}</span><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#listtoolsresult-nextcursor">nextCursor</a><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">;</span><br />  <a class="tsd-kind-property" href="#">tools</a><span class="tsd-signature-symbol">:</span> <a href="#tool" class="tsd-signature-type tsd-kind-interface">Tool</a><span class="tsd-signature-symbol">\[]</span><span class="tsd-signature-symbol">;</span><br />  <span class="tsd-signature-symbol">\[</span><span class="tsd-kind-index-signature">key</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">]:</span> <span class="tsd-signature-type">unknown</span><span class="tsd-signature-symbol">;</span><br /><span class="tsd-signature-symbol">}</span></div><div class="tsd-comment tsd-typography"><p>The server's response to a tools/list request from the client.</p> </div><section class="tsd-panel tsd-member tsd-is-inherited"><div data-typedoc-h="3" class="tsd-anchor-link" id="listtoolsresult-_meta"><code class="tsd-tag">Optional</code><span>\_<wbr />meta</span><a href="#listtoolsresult-_meta" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">\_meta</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-symbol">\{</span> <span class="tsd-signature-symbol">\[</span><span class="tsd-kind-index-signature">key</span><span class="tsd-signature-symbol">:</span> <span class="tsd-signature-type">string</span><span class="tsd-signature-symbol">]:</span> <span class="tsd-signature-type">unknown</span> <span class="tsd-signature-symbol">}</span></div><div class="tsd-comment tsd-typography"><p>See <a href="/specification/2025-06-18/basic/index#meta">General fields: <code>\_meta</code></a> for notes on <code>\_meta</code> usage.</p> </div><div class="tsd-comment tsd-typography" /><aside class="tsd-sources"><p>Inherited from PaginatedResult.\_meta</p></aside></section><section class="tsd-panel tsd-member tsd-is-inherited"><div data-typedoc-h="3" class="tsd-anchor-link" id="listtoolsresult-nextcursor"><code class="tsd-tag">Optional</code><span>next<wbr />Cursor</span><a href="#listtoolsresult-nextcursor" aria-label="Permalink" class="tsd-anchor-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><use href="assets/icons.svg#icon-anchor" /></svg></a></div><div class="tsd-signature"><span class="tsd-kind-property">nextCursor</span><span class="tsd-signature-symbol">?:</span> <span class="tsd-signature-type">string</span></div><div class="tsd-comment tsd-typography"><p>An opaque token representing the pagination position after the last returned result.
If present, there may be more results available.</p> </div><div class="tsd-comment tsd-typography" /><aside class="tsd-sources"><p>Inherited from PaginatedResult.nextCursor</p></aside></section>

```

mcp-streaming-http-go/docs/mcp/2025-06-18/server/prompts.md:
```

# Prompts

<div id="enable-section-numbers" />

<Info>**Protocol Revision**: 2025-06-18</Info>

The Model Context Protocol (MCP) provides a standardized way for servers to expose prompt
templates to clients. Prompts allow servers to provide structured messages and
instructions for interacting with language models. Clients can discover available
prompts, retrieve their contents, and provide arguments to customize them.

## User Interaction Model

Prompts are designed to be **user-controlled**, meaning they are exposed from servers to
clients with the intention of the user being able to explicitly select them for use.

Typically, prompts would be triggered through user-initiated commands in the user
interface, which allows users to naturally discover and invoke available prompts.

For example, as slash commands:

![Example of prompt exposed as slash command](https://mintlify.s3.us-west-1.amazonaws.com/mcp/specification/2025-06-18/server/slash-command.png)

However, implementors are free to expose prompts through any interface pattern that suits
their needs—the protocol itself does not mandate any specific user interaction
model.

## Capabilities

Servers that support prompts **MUST** declare the `prompts` capability during
[initialization](/specification/2025-06-18/basic/lifecycle#initialization):

```json
{
  "capabilities": {
    "prompts": {
      "listChanged": true
    }
  }
}
```

`listChanged` indicates whether the server will emit notifications when the list of
available prompts changes.

## Protocol Messages

### Listing Prompts

To retrieve available prompts, clients send a `prompts/list` request. This operation
supports [pagination](/specification/2025-06-18/server/utilities/pagination).

**Request:**

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "prompts/list",
  "params": {
    "cursor": "optional-cursor-value"
  }
}
```

**Response:**

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "result": {
    "prompts": [
      {
        "name": "code_review",
        "title": "Request Code Review",
        "description": "Asks the LLM to analyze code quality and suggest improvements",
        "arguments": [
          {
            "name": "code",
            "description": "The code to review",
            "required": true
          }
        ]
      }
    ],
    "nextCursor": "next-page-cursor"
  }
}
```

### Getting a Prompt

To retrieve a specific prompt, clients send a `prompts/get` request. Arguments may be
auto-completed through [the completion API](/specification/2025-06-18/server/utilities/completion).

**Request:**

```json
{
  "jsonrpc": "2.0",
  "id": 2,
  "method": "prompts/get",
  "params": {
    "name": "code_review",
    "arguments": {
      "code": "def hello():\n    print('world')"
    }
  }
}
```

**Response:**

```json
{
  "jsonrpc": "2.0",
  "id": 2,
  "result": {
    "description": "Code review prompt",
    "messages": [
      {
        "role": "user",
        "content": {
          "type": "text",
          "text": "Please review this Python code:\ndef hello():\n    print('world')"
        }
      }
    ]
  }
}
```

### List Changed Notification

When the list of available prompts changes, servers that declared the `listChanged`
capability **SHOULD** send a notification:

```json
{
  "jsonrpc": "2.0",
  "method": "notifications/prompts/list_changed"
}
```

## Message Flow

```mermaid
sequenceDiagram
    participant Client
    participant Server

    Note over Client,Server: Discovery
    Client->>Server: prompts/list
    Server-->>Client: List of prompts

    Note over Client,Server: Usage
    Client->>Server: prompts/get
    Server-->>Client: Prompt content

    opt listChanged
      Note over Client,Server: Changes
      Server--)Client: prompts/list_changed
      Client->>Server: prompts/list
      Server-->>Client: Updated prompts
    end
```

## Data Types

### Prompt

A prompt definition includes:

- `name`: Unique identifier for the prompt
- `title`: Optional human-readable name of the prompt for display purposes.
- `description`: Optional human-readable description
- `arguments`: Optional list of arguments for customization

### PromptMessage

Messages in a prompt can contain:

- `role`: Either "user" or "assistant" to indicate the speaker
- `content`: One of the following content types:

<Note>
  All content types in prompt messages support optional
  [annotations](/specification/2025-06-18/server/resources#annotations) for
  metadata about audience, priority, and modification times.
</Note>

#### Text Content

Text content represents plain text messages:

```json
{
  "type": "text",
  "text": "The text content of the message"
}
```

This is the most common content type used for natural language interactions.

#### Image Content

Image content allows including visual information in messages:

```json
{
  "type": "image",
  "data": "base64-encoded-image-data",
  "mimeType": "image/png"
}
```

The image data **MUST** be base64-encoded and include a valid MIME type. This enables
multi-modal interactions where visual context is important.

#### Audio Content

Audio content allows including audio information in messages:

```json
{
  "type": "audio",
  "data": "base64-encoded-audio-data",
  "mimeType": "audio/wav"
}
```

The audio data MUST be base64-encoded and include a valid MIME type. This enables
multi-modal interactions where audio context is important.

#### Embedded Resources

Embedded resources allow referencing server-side resources directly in messages:

```json
{
  "type": "resource",
  "resource": {
    "uri": "resource://example",
    "name": "example",
    "title": "My Example Resource",
    "mimeType": "text/plain",
    "text": "Resource content"
  }
}
```

Resources can contain either text or binary (blob) data and **MUST** include:

- A valid resource URI
- The appropriate MIME type
- Either text content or base64-encoded blob data

Embedded resources enable prompts to seamlessly incorporate server-managed content like
documentation, code samples, or other reference materials directly into the conversation
flow.

## Error Handling

Servers **SHOULD** return standard JSON-RPC errors for common failure cases:

- Invalid prompt name: `-32602` (Invalid params)
- Missing required arguments: `-32602` (Invalid params)
- Internal errors: `-32603` (Internal error)

## Implementation Considerations

1. Servers **SHOULD** validate prompt arguments before processing
2. Clients **SHOULD** handle pagination for large prompt lists
3. Both parties **SHOULD** respect capability negotiation

## Security

Implementations **MUST** carefully validate all prompt inputs and outputs to prevent
injection attacks or unauthorized access to resources.

```

mcp-streaming-http-go/docs/mcp/2025-06-18/server/resources.md:
```

# Resources

<div id="enable-section-numbers" />

<Info>**Protocol Revision**: 2025-06-18</Info>

The Model Context Protocol (MCP) provides a standardized way for servers to expose
resources to clients. Resources allow servers to share data that provides context to
language models, such as files, database schemas, or application-specific information.
Each resource is uniquely identified by a
[URI](https://datatracker.ietf.org/doc/html/rfc3986).

## User Interaction Model

Resources in MCP are designed to be **application-driven**, with host applications
determining how to incorporate context based on their needs.

For example, applications could:

- Expose resources through UI elements for explicit selection, in a tree or list view
- Allow the user to search through and filter available resources
- Implement automatic context inclusion, based on heuristics or the AI model's selection

![Example of resource context picker](https://mintlify.s3.us-west-1.amazonaws.com/mcp/specification/2025-06-18/server/resource-picker.png)

However, implementations are free to expose resources through any interface pattern that
suits their needs—the protocol itself does not mandate any specific user
interaction model.

## Capabilities

Servers that support resources **MUST** declare the `resources` capability:

```json
{
  "capabilities": {
    "resources": {
      "subscribe": true,
      "listChanged": true
    }
  }
}
```

The capability supports two optional features:

- `subscribe`: whether the client can subscribe to be notified of changes to individual
  resources.
- `listChanged`: whether the server will emit notifications when the list of available
  resources changes.

Both `subscribe` and `listChanged` are optional—servers can support neither,
either, or both:

```json
{
  "capabilities": {
    "resources": {} // Neither feature supported
  }
}
```

```json
{
  "capabilities": {
    "resources": {
      "subscribe": true // Only subscriptions supported
    }
  }
}
```

```json
{
  "capabilities": {
    "resources": {
      "listChanged": true // Only list change notifications supported
    }
  }
}
```

## Protocol Messages

### Listing Resources

To discover available resources, clients send a `resources/list` request. This operation
supports [pagination](/specification/2025-06-18/server/utilities/pagination).

**Request:**

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "resources/list",
  "params": {
    "cursor": "optional-cursor-value"
  }
}
```

**Response:**

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "result": {
    "resources": [
      {
        "uri": "file:///project/src/main.rs",
        "name": "main.rs",
        "title": "Rust Software Application Main File",
        "description": "Primary application entry point",
        "mimeType": "text/x-rust"
      }
    ],
    "nextCursor": "next-page-cursor"
  }
}
```

### Reading Resources

To retrieve resource contents, clients send a `resources/read` request:

**Request:**

```json
{
  "jsonrpc": "2.0",
  "id": 2,
  "method": "resources/read",
  "params": {
    "uri": "file:///project/src/main.rs"
  }
}
```

**Response:**

```json
{
  "jsonrpc": "2.0",
  "id": 2,
  "result": {
    "contents": [
      {
        "uri": "file:///project/src/main.rs",
        "name": "main.rs",
        "title": "Rust Software Application Main File",
        "mimeType": "text/x-rust",
        "text": "fn main() {\n    println!(\"Hello world!\");\n}"
      }
    ]
  }
}
```

### Resource Templates

Resource templates allow servers to expose parameterized resources using
[URI templates](https://datatracker.ietf.org/doc/html/rfc6570). Arguments may be
auto-completed through [the completion API](/specification/2025-06-18/server/utilities/completion).

**Request:**

```json
{
  "jsonrpc": "2.0",
  "id": 3,
  "method": "resources/templates/list"
}
```

**Response:**

```json
{
  "jsonrpc": "2.0",
  "id": 3,
  "result": {
    "resourceTemplates": [
      {
        "uriTemplate": "file:///{path}",
        "name": "Project Files",
        "title": "📁 Project Files",
        "description": "Access files in the project directory",
        "mimeType": "application/octet-stream"
      }
    ]
  }
}
```

### List Changed Notification

When the list of available resources changes, servers that declared the `listChanged`
capability **SHOULD** send a notification:

```json
{
  "jsonrpc": "2.0",
  "method": "notifications/resources/list_changed"
}
```

### Subscriptions

The protocol supports optional subscriptions to resource changes. Clients can subscribe
to specific resources and receive notifications when they change:

**Subscribe Request:**

```json
{
  "jsonrpc": "2.0",
  "id": 4,
  "method": "resources/subscribe",
  "params": {
    "uri": "file:///project/src/main.rs"
  }
}
```

**Update Notification:**

```json
{
  "jsonrpc": "2.0",
  "method": "notifications/resources/updated",
  "params": {
    "uri": "file:///project/src/main.rs",
    "title": "Rust Software Application Main File"
  }
}
```

## Message Flow

```mermaid
sequenceDiagram
    participant Client
    participant Server

    Note over Client,Server: Resource Discovery
    Client->>Server: resources/list
    Server-->>Client: List of resources

    Note over Client,Server: Resource Access
    Client->>Server: resources/read
    Server-->>Client: Resource contents

    Note over Client,Server: Subscriptions
    Client->>Server: resources/subscribe
    Server-->>Client: Subscription confirmed

    Note over Client,Server: Updates
    Server--)Client: notifications/resources/updated
    Client->>Server: resources/read
    Server-->>Client: Updated contents
```

## Data Types

### Resource

A resource definition includes:

- `uri`: Unique identifier for the resource
- `name`: The name of the resource.
- `title`: Optional human-readable name of the resource for display purposes.
- `description`: Optional description
- `mimeType`: Optional MIME type
- `size`: Optional size in bytes

### Resource Contents

Resources can contain either text or binary data:

#### Text Content

```json
{
  "uri": "file:///example.txt",
  "name": "example.txt",
  "title": "Example Text File",
  "mimeType": "text/plain",
  "text": "Resource content"
}
```

#### Binary Content

```json
{
  "uri": "file:///example.png",
  "name": "example.png",
  "title": "Example Image",
  "mimeType": "image/png",
  "blob": "base64-encoded-data"
}
```

### Annotations

Resources, resource templates and content blocks support optional annotations that provide hints to clients about how to use or display the resource:

- **`audience`**: An array indicating the intended audience(s) for this resource. Valid values are `"user"` and `"assistant"`. For example, `["user", "assistant"]` indicates content useful for both.
- **`priority`**: A number from 0.0 to 1.0 indicating the importance of this resource. A value of 1 means "most important" (effectively required), while 0 means "least important" (entirely optional).
- **`lastModified`**: An ISO 8601 formatted timestamp indicating when the resource was last modified (e.g., `"2025-01-12T15:00:58Z"`).

Example resource with annotations:

```json
{
  "uri": "file:///project/README.md",
  "name": "README.md",
  "title": "Project Documentation",
  "mimeType": "text/markdown",
  "annotations": {
    "audience": ["user"],
    "priority": 0.8,
    "lastModified": "2025-01-12T15:00:58Z"
  }
}
```

Clients can use these annotations to:

- Filter resources based on their intended audience
- Prioritize which resources to include in context
- Display modification times or sort by recency

## Common URI Schemes

The protocol defines several standard URI schemes. This list not
exhaustive—implementations are always free to use additional, custom URI schemes.

### https\://

Used to represent a resource available on the web.

Servers **SHOULD** use this scheme only when the client is able to fetch and load the
resource directly from the web on its own—that is, it doesn’t need to read the resource
via the MCP server.

For other use cases, servers **SHOULD** prefer to use another URI scheme, or define a
custom one, even if the server will itself be downloading resource contents over the
internet.

### file://

Used to identify resources that behave like a filesystem. However, the resources do not
need to map to an actual physical filesystem.

MCP servers **MAY** identify file:// resources with an
[XDG MIME type](https://specifications.freedesktop.org/shared-mime-info-spec/0.14/ar01s02.html#id-1.3.14),
like `inode/directory`, to represent non-regular files (such as directories) that don’t
otherwise have a standard MIME type.

### git://

Git version control integration.

### Custom URI Schemes

Custom URI schemes **MUST** be in accordance with [RFC3986](https://datatracker.ietf.org/doc/html/rfc3986),
taking the above guidance in to account.

## Error Handling

Servers **SHOULD** return standard JSON-RPC errors for common failure cases:

- Resource not found: `-32002`
- Internal errors: `-32603`

Example error:

```json
{
  "jsonrpc": "2.0",
  "id": 5,
  "error": {
    "code": -32002,
    "message": "Resource not found",
    "data": {
      "uri": "file:///nonexistent.txt"
    }
  }
}
```

## Security Considerations

1. Servers **MUST** validate all resource URIs
2. Access controls **SHOULD** be implemented for sensitive resources
3. Binary data **MUST** be properly encoded
4. Resource permissions **SHOULD** be checked before operations

```

mcp-streaming-http-go/docs/mcp/2025-06-18/server/tools.md:
```

# Tools

<div id="enable-section-numbers" />

<Info>**Protocol Revision**: 2025-06-18</Info>

The Model Context Protocol (MCP) allows servers to expose tools that can be invoked by
language models. Tools enable models to interact with external systems, such as querying
databases, calling APIs, or performing computations. Each tool is uniquely identified by
a name and includes metadata describing its schema.

## User Interaction Model

Tools in MCP are designed to be **model-controlled**, meaning that the language model can
discover and invoke tools automatically based on its contextual understanding and the
user's prompts.

However, implementations are free to expose tools through any interface pattern that
suits their needs—the protocol itself does not mandate any specific user
interaction model.

<Warning>
  For trust & safety and security, there **SHOULD** always
  be a human in the loop with the ability to deny tool invocations.

Applications **SHOULD**:

- Provide UI that makes clear which tools are being exposed to the AI model
- Insert clear visual indicators when tools are invoked
- Present confirmation prompts to the user for operations, to ensure a human is in the
  loop
  </Warning>

## Capabilities

Servers that support tools **MUST** declare the `tools` capability:

```json
{
  "capabilities": {
    "tools": {
      "listChanged": true
    }
  }
}
```

`listChanged` indicates whether the server will emit notifications when the list of
available tools changes.

## Protocol Messages

### Listing Tools

To discover available tools, clients send a `tools/list` request. This operation supports
[pagination](/specification/2025-06-18/server/utilities/pagination).

**Request:**

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "tools/list",
  "params": {
    "cursor": "optional-cursor-value"
  }
}
```

**Response:**

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "result": {
    "tools": [
      {
        "name": "get_weather",
        "title": "Weather Information Provider",
        "description": "Get current weather information for a location",
        "inputSchema": {
          "type": "object",
          "properties": {
            "location": {
              "type": "string",
              "description": "City name or zip code"
            }
          },
          "required": ["location"]
        }
      }
    ],
    "nextCursor": "next-page-cursor"
  }
}
```

### Calling Tools

To invoke a tool, clients send a `tools/call` request:

**Request:**

```json
{
  "jsonrpc": "2.0",
  "id": 2,
  "method": "tools/call",
  "params": {
    "name": "get_weather",
    "arguments": {
      "location": "New York"
    }
  }
}
```

**Response:**

```json
{
  "jsonrpc": "2.0",
  "id": 2,
  "result": {
    "content": [
      {
        "type": "text",
        "text": "Current weather in New York:\nTemperature: 72°F\nConditions: Partly cloudy"
      }
    ],
    "isError": false
  }
}
```

### List Changed Notification

When the list of available tools changes, servers that declared the `listChanged`
capability **SHOULD** send a notification:

```json
{
  "jsonrpc": "2.0",
  "method": "notifications/tools/list_changed"
}
```

## Message Flow

```mermaid
sequenceDiagram
    participant LLM
    participant Client
    participant Server

    Note over Client,Server: Discovery
    Client->>Server: tools/list
    Server-->>Client: List of tools

    Note over Client,LLM: Tool Selection
    LLM->>Client: Select tool to use

    Note over Client,Server: Invocation
    Client->>Server: tools/call
    Server-->>Client: Tool result
    Client->>LLM: Process result

    Note over Client,Server: Updates
    Server--)Client: tools/list_changed
    Client->>Server: tools/list
    Server-->>Client: Updated tools
```

## Data Types

### Tool

A tool definition includes:

- `name`: Unique identifier for the tool
- `title`: Optional human-readable name of the tool for display purposes.
- `description`: Human-readable description of functionality
- `inputSchema`: JSON Schema defining expected parameters
- `outputSchema`: Optional JSON Schema defining expected output structure
- `annotations`: optional properties describing tool behavior

<Warning>
  For trust & safety and security, clients **MUST** consider
  tool annotations to be untrusted unless they come from trusted servers.
</Warning>

### Tool Result

Tool results may contain [**structured**](#structured-content) or **unstructured** content.

**Unstructured** content is returned in the `content` field of a result, and can contain multiple content items of different types:

<Note>
  All content types (text, image, audio, resource links, and embedded resources)
  support optional
  [annotations](/specification/2025-06-18/server/resources#annotations) that
  provide metadata about audience, priority, and modification times. This is the
  same annotation format used by resources and prompts.
</Note>

#### Text Content

```json
{
  "type": "text",
  "text": "Tool result text"
}
```

#### Image Content

```json
{
  "type": "image",
  "data": "base64-encoded-data",
  "mimeType": "image/png"
  "annotations": {
    "audience": ["user"],
    "priority": 0.9
  }

}
```

This example demonstrates the use of an optional Annotation.

#### Audio Content

```json
{
  "type": "audio",
  "data": "base64-encoded-audio-data",
  "mimeType": "audio/wav"
}
```

#### Resource Links

A tool **MAY** return links to [Resources](/specification/2025-06-18/server/resources), to provide additional context
or data. In this case, the tool will return a URI that can be subscribed to or fetched by the client:

```json
{
  "type": "resource_link",
  "uri": "file:///project/src/main.rs",
  "name": "main.rs",
  "description": "Primary application entry point",
  "mimeType": "text/x-rust",
  "annotations": {
    "audience": ["assistant"],
    "priority": 0.9
  }
}
```

Resource links support the same [Resource annotations](/specification/2025-06-18/server/resources#annotations) as regular resources to help clients understand how to use them.

<Info>
  Resource links returned by tools are not guaranteed to appear in the results
  of a `resources/list` request.
</Info>

#### Embedded Resources

[Resources](/specification/2025-06-18/server/resources) **MAY** be embedded to provide additional context
or data using a suitable [URI scheme](./resources#common-uri-schemes). Servers that use embedded resources **SHOULD** implement the `resources` capability:

```json
{
  "type": "resource",
  "resource": {
    "uri": "file:///project/src/main.rs",
    "title": "Project Rust Main File",
    "mimeType": "text/x-rust",
    "text": "fn main() {\n    println!(\"Hello world!\");\n}",
    "annotations": {
      "audience": ["user", "assistant"],
      "priority": 0.7,
      "lastModified": "2025-05-03T14:30:00Z"
    }
  }
}
```

Embedded resources support the same [Resource annotations](/specification/2025-06-18/server/resources#annotations) as regular resources to help clients understand how to use them.

#### Structured Content

**Structured** content is returned as a JSON object in the `structuredContent` field of a result.

For backwards compatibility, a tool that returns structured content SHOULD also return the serialized JSON in a TextContent block.

#### Output Schema

Tools may also provide an output schema for validation of structured results.
If an output schema is provided:

- Servers **MUST** provide structured results that conform to this schema.
- Clients **SHOULD** validate structured results against this schema.

Example tool with output schema:

```json
{
  "name": "get_weather_data",
  "title": "Weather Data Retriever",
  "description": "Get current weather data for a location",
  "inputSchema": {
    "type": "object",
    "properties": {
      "location": {
        "type": "string",
        "description": "City name or zip code"
      }
    },
    "required": ["location"]
  },
  "outputSchema": {
    "type": "object",
    "properties": {
      "temperature": {
        "type": "number",
        "description": "Temperature in celsius"
      },
      "conditions": {
        "type": "string",
        "description": "Weather conditions description"
      },
      "humidity": {
        "type": "number",
        "description": "Humidity percentage"
      }
    },
    "required": ["temperature", "conditions", "humidity"]
  }
}
```

Example valid response for this tool:

```json
{
  "jsonrpc": "2.0",
  "id": 5,
  "result": {
    "content": [
      {
        "type": "text",
        "text": "{\"temperature\": 22.5, \"conditions\": \"Partly cloudy\", \"humidity\": 65}"
      }
    ],
    "structuredContent": {
      "temperature": 22.5,
      "conditions": "Partly cloudy",
      "humidity": 65
    }
  }
}
```

Providing an output schema helps clients and LLMs understand and properly handle structured tool outputs by:

- Enabling strict schema validation of responses
- Providing type information for better integration with programming languages
- Guiding clients and LLMs to properly parse and utilize the returned data
- Supporting better documentation and developer experience

## Error Handling

Tools use two error reporting mechanisms:

1. **Protocol Errors**: Standard JSON-RPC errors for issues like:

   - Unknown tools
   - Invalid arguments
   - Server errors

2. **Tool Execution Errors**: Reported in tool results with `isError: true`:
   - API failures
   - Invalid input data
   - Business logic errors

Example protocol error:

```json
{
  "jsonrpc": "2.0",
  "id": 3,
  "error": {
    "code": -32602,
    "message": "Unknown tool: invalid_tool_name"
  }
}
```

Example tool execution error:

```json
{
  "jsonrpc": "2.0",
  "id": 4,
  "result": {
    "content": [
      {
        "type": "text",
        "text": "Failed to fetch weather data: API rate limit exceeded"
      }
    ],
    "isError": true
  }
}
```

## Security Considerations

1. Servers **MUST**:

   - Validate all tool inputs
   - Implement proper access controls
   - Rate limit tool invocations
   - Sanitize tool outputs

2. Clients **SHOULD**:
   - Prompt for user confirmation on sensitive operations
   - Show tool inputs to the user before calling the server, to avoid malicious or
     accidental data exfiltration
   - Validate tool results before passing to LLM
   - Implement timeouts for tool calls
   - Log tool usage for audit purposes

```

mcp-streaming-http-go/docs/mcp/2025-06-18/server/utilities/completion.md:
```

# Completion

<div id="enable-section-numbers" />

<Info>**Protocol Revision**: 2025-06-18</Info>

The Model Context Protocol (MCP) provides a standardized way for servers to offer
argument autocompletion suggestions for prompts and resource URIs. This enables rich,
IDE-like experiences where users receive contextual suggestions while entering argument
values.

## User Interaction Model

Completion in MCP is designed to support interactive user experiences similar to IDE code
completion.

For example, applications may show completion suggestions in a dropdown or popup menu as
users type, with the ability to filter and select from available options.

However, implementations are free to expose completion through any interface pattern that
suits their needs—the protocol itself does not mandate any specific user
interaction model.

## Capabilities

Servers that support completions **MUST** declare the `completions` capability:

```json
{
  "capabilities": {
    "completions": {}
  }
}
```

## Protocol Messages

### Requesting Completions

To get completion suggestions, clients send a `completion/complete` request specifying
what is being completed through a reference type:

**Request:**

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "completion/complete",
  "params": {
    "ref": {
      "type": "ref/prompt",
      "name": "code_review"
    },
    "argument": {
      "name": "language",
      "value": "py"
    }
  }
}
```

**Response:**

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "result": {
    "completion": {
      "values": ["python", "pytorch", "pyside"],
      "total": 10,
      "hasMore": true
    }
  }
}
```

For prompts or URI templates with multiple arguments, clients should include previous completions in the `context.arguments` object to provide context for subsequent requests.

**Request:**

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "completion/complete",
  "params": {
    "ref": {
      "type": "ref/prompt",
      "name": "code_review"
    },
    "argument": {
      "name": "framework",
      "value": "fla"
    },
    "context": {
      "arguments": {
        "language": "python"
      }
    }
  }
}
```

**Response:**

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "result": {
    "completion": {
      "values": ["flask"],
      "total": 1,
      "hasMore": false
    }
  }
}
```

### Reference Types

The protocol supports two types of completion references:

| Type           | Description                 | Example                                             |
| -------------- | --------------------------- | --------------------------------------------------- |
| `ref/prompt`   | References a prompt by name | `{"type": "ref/prompt", "name": "code_review"}`     |
| `ref/resource` | References a resource URI   | `{"type": "ref/resource", "uri": "file:///{path}"}` |

### Completion Results

Servers return an array of completion values ranked by relevance, with:

- Maximum 100 items per response
- Optional total number of available matches
- Boolean indicating if additional results exist

## Message Flow

```mermaid
sequenceDiagram
    participant Client
    participant Server

    Note over Client: User types argument
    Client->>Server: completion/complete
    Server-->>Client: Completion suggestions

    Note over Client: User continues typing
    Client->>Server: completion/complete
    Server-->>Client: Refined suggestions
```

## Data Types

### CompleteRequest

- `ref`: A `PromptReference` or `ResourceReference`
- `argument`: Object containing:
  - `name`: Argument name
  - `value`: Current value
- `context`: Object containing:
  - `arguments`: A mapping of already-resolved argument names to their values.

### CompleteResult

- `completion`: Object containing:
  - `values`: Array of suggestions (max 100)
  - `total`: Optional total matches
  - `hasMore`: Additional results flag

## Error Handling

Servers **SHOULD** return standard JSON-RPC errors for common failure cases:

- Method not found: `-32601` (Capability not supported)
- Invalid prompt name: `-32602` (Invalid params)
- Missing required arguments: `-32602` (Invalid params)
- Internal errors: `-32603` (Internal error)

## Implementation Considerations

1. Servers **SHOULD**:

   - Return suggestions sorted by relevance
   - Implement fuzzy matching where appropriate
   - Rate limit completion requests
   - Validate all inputs

2. Clients **SHOULD**:
   - Debounce rapid completion requests
   - Cache completion results where appropriate
   - Handle missing or partial results gracefully

## Security

Implementations **MUST**:

- Validate all completion inputs
- Implement appropriate rate limiting
- Control access to sensitive suggestions
- Prevent completion-based information disclosure

```

mcp-streaming-http-go/docs/mcp/2025-06-18/server/utilities/logging.md:
```

# Logging

<div id="enable-section-numbers" />

<Info>**Protocol Revision**: 2025-06-18</Info>

The Model Context Protocol (MCP) provides a standardized way for servers to send
structured log messages to clients. Clients can control logging verbosity by setting
minimum log levels, with servers sending notifications containing severity levels,
optional logger names, and arbitrary JSON-serializable data.

## User Interaction Model

Implementations are free to expose logging through any interface pattern that suits their
needs—the protocol itself does not mandate any specific user interaction model.

## Capabilities

Servers that emit log message notifications **MUST** declare the `logging` capability:

```json
{
  "capabilities": {
    "logging": {}
  }
}
```

## Log Levels

The protocol follows the standard syslog severity levels specified in
[RFC 5424](https://datatracker.ietf.org/doc/html/rfc5424#section-6.2.1):

| Level     | Description                      | Example Use Case           |
| --------- | -------------------------------- | -------------------------- |
| debug     | Detailed debugging information   | Function entry/exit points |
| info      | General informational messages   | Operation progress updates |
| notice    | Normal but significant events    | Configuration changes      |
| warning   | Warning conditions               | Deprecated feature usage   |
| error     | Error conditions                 | Operation failures         |
| critical  | Critical conditions              | System component failures  |
| alert     | Action must be taken immediately | Data corruption detected   |
| emergency | System is unusable               | Complete system failure    |

## Protocol Messages

### Setting Log Level

To configure the minimum log level, clients **MAY** send a `logging/setLevel` request:

**Request:**

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "logging/setLevel",
  "params": {
    "level": "info"
  }
}
```

### Log Message Notifications

Servers send log messages using `notifications/message` notifications:

```json
{
  "jsonrpc": "2.0",
  "method": "notifications/message",
  "params": {
    "level": "error",
    "logger": "database",
    "data": {
      "error": "Connection failed",
      "details": {
        "host": "localhost",
        "port": 5432
      }
    }
  }
}
```

## Message Flow

```mermaid
sequenceDiagram
    participant Client
    participant Server

    Note over Client,Server: Configure Logging
    Client->>Server: logging/setLevel (info)
    Server-->>Client: Empty Result

    Note over Client,Server: Server Activity
    Server--)Client: notifications/message (info)
    Server--)Client: notifications/message (warning)
    Server--)Client: notifications/message (error)

    Note over Client,Server: Level Change
    Client->>Server: logging/setLevel (error)
    Server-->>Client: Empty Result
    Note over Server: Only sends error level<br/>and above
```

## Error Handling

Servers **SHOULD** return standard JSON-RPC errors for common failure cases:

- Invalid log level: `-32602` (Invalid params)
- Configuration errors: `-32603` (Internal error)

## Implementation Considerations

1. Servers **SHOULD**:

   - Rate limit log messages
   - Include relevant context in data field
   - Use consistent logger names
   - Remove sensitive information

2. Clients **MAY**:
   - Present log messages in the UI
   - Implement log filtering/search
   - Display severity visually
   - Persist log messages

## Security

1. Log messages **MUST NOT** contain:

   - Credentials or secrets
   - Personal identifying information
   - Internal system details that could aid attacks

2. Implementations **SHOULD**:
   - Rate limit messages
   - Validate all data fields
   - Control log access
   - Monitor for sensitive content

```

mcp-streaming-http-go/docs/mcp/2025-06-18/server/utilities/pagination.md:
```

# Pagination

<div id="enable-section-numbers" />

<Info>**Protocol Revision**: 2025-06-18</Info>

The Model Context Protocol (MCP) supports paginating list operations that may return
large result sets. Pagination allows servers to yield results in smaller chunks rather
than all at once.

Pagination is especially important when connecting to external services over the
internet, but also useful for local integrations to avoid performance issues with large
data sets.

## Pagination Model

Pagination in MCP uses an opaque cursor-based approach, instead of numbered pages.

- The **cursor** is an opaque string token, representing a position in the result set
- **Page size** is determined by the server, and clients **MUST NOT** assume a fixed page
  size

## Response Format

Pagination starts when the server sends a **response** that includes:

- The current page of results
- An optional `nextCursor` field if more results exist

```json
{
  "jsonrpc": "2.0",
  "id": "123",
  "result": {
    "resources": [...],
    "nextCursor": "eyJwYWdlIjogM30="
  }
}
```

## Request Format

After receiving a cursor, the client can _continue_ paginating by issuing a request
including that cursor:

```json
{
  "jsonrpc": "2.0",
  "method": "resources/list",
  "params": {
    "cursor": "eyJwYWdlIjogMn0="
  }
}
```

## Pagination Flow

```mermaid
sequenceDiagram
    participant Client
    participant Server

    Client->>Server: List Request (no cursor)
    loop Pagination Loop
      Server-->>Client: Page of results + nextCursor
      Client->>Server: List Request (with cursor)
    end
```

## Operations Supporting Pagination

The following MCP operations support pagination:

- `resources/list` - List available resources
- `resources/templates/list` - List resource templates
- `prompts/list` - List available prompts
- `tools/list` - List available tools

## Implementation Guidelines

1. Servers **SHOULD**:

   - Provide stable cursors
   - Handle invalid cursors gracefully

2. Clients **SHOULD**:

   - Treat a missing `nextCursor` as the end of results
   - Support both paginated and non-paginated flows

3. Clients **MUST** treat cursors as opaque tokens:
   - Don't make assumptions about cursor format
   - Don't attempt to parse or modify cursors
   - Don't persist cursors across sessions

## Error Handling

Invalid cursors **SHOULD** result in an error with code -32602 (Invalid params).

```

mcp-streaming-http-go/docs/mcp/2025-06-18/server.md:
```

# Overview

<Info>**Protocol Revision**: 2025-06-18</Info>

Servers provide the fundamental building blocks for adding context to language models via
MCP. These primitives enable rich interactions between clients, servers, and language
models:

- **Prompts**: Pre-defined templates or instructions that guide language model
  interactions
- **Resources**: Structured data or content that provides additional context to the model
- **Tools**: Executable functions that allow models to perform actions or retrieve
  information

Each primitive can be summarized in the following control hierarchy:

| Primitive | Control                | Description                                        | Example                         |
| --------- | ---------------------- | -------------------------------------------------- | ------------------------------- |
| Prompts   | User-controlled        | Interactive templates invoked by user choice       | Slash commands, menu options    |
| Resources | Application-controlled | Contextual data attached and managed by the client | File contents, git history      |
| Tools     | Model-controlled       | Functions exposed to the LLM to take actions       | API POST requests, file writing |

Explore these key primitives in more detail below:

<CardGroup cols={3}>
  <Card title="Prompts" icon="message" href="/specification/2025-06-18/server/prompts" />

  <Card title="Resources" icon="file-lines" href="/specification/2025-06-18/server/resources" />

  <Card title="Tools" icon="wrench" href="/specification/2025-06-18/server/tools" />
</CardGroup>

```

mcp-streaming-http-go/docs/mcp/2025-06-18.md:
```

# Specification

<div id="enable-section-numbers" />

[Model Context Protocol](https://modelcontextprotocol.io) (MCP) is an open protocol that
enables seamless integration between LLM applications and external data sources and
tools. Whether you're building an AI-powered IDE, enhancing a chat interface, or creating
custom AI workflows, MCP provides a standardized way to connect LLMs with the context
they need.

This specification defines the authoritative protocol requirements, based on the
TypeScript schema in
[schema.ts](https://github.com/modelcontextprotocol/specification/blob/main/schema/2025-06-18/schema.ts).

For implementation guides and examples, visit
[modelcontextprotocol.io](https://modelcontextprotocol.io).

The key words "MUST", "MUST NOT", "REQUIRED", "SHALL", "SHALL NOT", "SHOULD", "SHOULD
NOT", "RECOMMENDED", "NOT RECOMMENDED", "MAY", and "OPTIONAL" in this document are to be
interpreted as described in [BCP 14](https://datatracker.ietf.org/doc/html/bcp14)
\[[RFC2119](https://datatracker.ietf.org/doc/html/rfc2119)]
\[[RFC8174](https://datatracker.ietf.org/doc/html/rfc8174)] when, and only when, they
appear in all capitals, as shown here.

## Overview

MCP provides a standardized way for applications to:

- Share contextual information with language models
- Expose tools and capabilities to AI systems
- Build composable integrations and workflows

The protocol uses [JSON-RPC](https://www.jsonrpc.org/) 2.0 messages to establish
communication between:

- **Hosts**: LLM applications that initiate connections
- **Clients**: Connectors within the host application
- **Servers**: Services that provide context and capabilities

MCP takes some inspiration from the
[Language Server Protocol](https://microsoft.github.io/language-server-protocol/), which
standardizes how to add support for programming languages across a whole ecosystem of
development tools. In a similar way, MCP standardizes how to integrate additional context
and tools into the ecosystem of AI applications.

## Key Details

### Base Protocol

- [JSON-RPC](https://www.jsonrpc.org/) message format
- Stateful connections
- Server and client capability negotiation

### Features

Servers offer any of the following features to clients:

- **Resources**: Context and data, for the user or the AI model to use
- **Prompts**: Templated messages and workflows for users
- **Tools**: Functions for the AI model to execute

Clients may offer the following features to servers:

- **Sampling**: Server-initiated agentic behaviors and recursive LLM interactions
- **Roots**: Server-initiated inquiries into uri or filesystem boundaries to operate in
- **Elicitation**: Server-initiated requests for additional information from users

### Additional Utilities

- Configuration
- Progress tracking
- Cancellation
- Error reporting
- Logging

## Security and Trust & Safety

The Model Context Protocol enables powerful capabilities through arbitrary data access
and code execution paths. With this power comes important security and trust
considerations that all implementors must carefully address.

### Key Principles

1. **User Consent and Control**

   - Users must explicitly consent to and understand all data access and operations
   - Users must retain control over what data is shared and what actions are taken
   - Implementors should provide clear UIs for reviewing and authorizing activities

2. **Data Privacy**

   - Hosts must obtain explicit user consent before exposing user data to servers
   - Hosts must not transmit resource data elsewhere without user consent
   - User data should be protected with appropriate access controls

3. **Tool Safety**

   - Tools represent arbitrary code execution and must be treated with appropriate
     caution.
     - In particular, descriptions of tool behavior such as annotations should be
       considered untrusted, unless obtained from a trusted server.
   - Hosts must obtain explicit user consent before invoking any tool
   - Users should understand what each tool does before authorizing its use

4. **LLM Sampling Controls**
   - Users must explicitly approve any LLM sampling requests
   - Users should control:
     - Whether sampling occurs at all
     - The actual prompt that will be sent
     - What results the server can see
   - The protocol intentionally limits server visibility into prompts

### Implementation Guidelines

While MCP itself cannot enforce these security principles at the protocol level,
implementors **SHOULD**:

1. Build robust consent and authorization flows into their applications
2. Provide clear documentation of security implications
3. Implement appropriate access controls and data protections
4. Follow security best practices in their integrations
5. Consider privacy implications in their feature designs
