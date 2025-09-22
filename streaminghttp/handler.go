package streaminghttp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/elnormous/contenttype"
	"github.com/ggoodman/mcp-server-go/auth"
	"github.com/ggoodman/mcp-server-go/internal/jsonrpc"
	"github.com/ggoodman/mcp-server-go/internal/sessioncore"
	"github.com/ggoodman/mcp-server-go/internal/wellknown"
	"github.com/ggoodman/mcp-server-go/mcp"
	"github.com/ggoodman/mcp-server-go/mcpservice"
	"github.com/ggoodman/mcp-server-go/sessions"
)

var (
	_ http.Handler = (*StreamingHTTPHandler)(nil)
)

var (
	ErrSessionHeaderMissing = errors.New("missing mcp-session-id header")
	ErrSessionHeaderInvalid = errors.New("invalid mcp-session-id header")
	ErrInvalidSession       = errors.New("invalid mcp session")
)

var (
	jsonMediaType         = contenttype.NewMediaType("application/json")
	eventStreamMediaType  = contenttype.NewMediaType("text/event-stream")
	eventStreamMediaTypes = []contenttype.MediaType{eventStreamMediaType}
)

const (
	// Use canonical header names for clarity; Go matches headers case-insensitively.
	lastEventIDHeader        = "Last-Event-ID"
	mcpSessionIDHeader       = "Mcp-Session-Id"
	mcpProtocolVersionHeader = "Mcp-Protocol-Version"
	authorizationHeader      = "Authorization"
	wwwAuthenticateHeader    = "WWW-Authenticate"
)

// writeJSONError emits a minimal JSON body for HTTP-layer rejections before a JSON-RPC
// message exchange is possible. We do NOT claim JSON-RPC framing here; this is
// transport-level. Shape: {"error":{"code":<httpStatus>,"message":"<reason>"}}
// Safe to call after some headers set but before status written.
func writeJSONError(w http.ResponseWriter, status int, msg string) {
	// Only set content-type if not already committed to SSE.
	if ct := w.Header().Get("Content-Type"); ct == "" || ct == jsonMediaType.String() {
		w.Header().Set("Content-Type", jsonMediaType.String())
	}
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": status, "message": msg}})
}

// Option configures the StreamingHTTPHandler.
type Option func(*newConfig)

// (no legacy config structs)
// (no legacy manual OIDC config struct)

// ManualOIDC provides manual OAuth/OIDC details and PRM fields when bypassing
// OIDC discovery.
type ManualOIDC struct {
	// Issuer is the OAuth2 authorization server issuer URL.
	Issuer string
	// JwksURI is the URL of the JSON Web Key Set document.
	JwksURI string

	// Optional fields that would normally be discovered from authorization server metadata
	ScopesSupported                            []string
	TokenEndpointAuthMethodsSupported          []string
	TokenEndpointAuthSigningAlgValuesSupported []string
	ServiceDocumentation                       string
	OpPolicyURI                                string
	OpTosURI                                   string
}

type newConfig struct {
	serverName string
	logger     *slog.Logger
	// auth mode: exactly one must be set
	discoveryURL string
	manualOIDC   *ManualOIDC
}

// WithServerName sets a human-readable server name surfaced in PRM.
func WithServerName(name string) Option {
	return func(c *newConfig) { c.serverName = name }
}

// WithLogger sets the slog handler used by the server. If not provided, logs are discarded.
func WithLogger(h *slog.Logger) Option {
	return func(c *newConfig) { c.logger = h }
}

// WithAuthorizationServerDiscovery enables auto-configuration via OAuth 2.0
// Authorization Server Metadata (RFC 8414) at the provided issuer URL. OIDC
// issuers are supported since their discovery document is a superset.
func WithAuthorizationServerDiscovery(authorizationServerURL string) Option {
	return func(c *newConfig) { c.discoveryURL = authorizationServerURL }
}

// WithManualOIDC sets manual OIDC/PRM configuration without discovery.
func WithManualOIDC(cfg ManualOIDC) Option {
	return func(c *newConfig) { c.manualOIDC = &cfg }
}

// StreamingHTTPHandler implements the stream HTTP transport protocol of
// the Model Context Protocol.
type StreamingHTTPHandler struct {
	mux            *http.ServeMux
	oidcProvider   *oidc.Provider
	log            *slog.Logger
	prmDocument    wellknown.ProtectedResourceMetadata
	prmDocumentURL *url.URL
	serverURL      *url.URL
	// Mirror of RFC 8414 Authorization Server Metadata for discovery convenience.
	// In discovery mode this mirrors the real AS document; in manual mode it's
	// synthesized from ManualOIDC fields.
	authServerMetadata    wellknown.AuthServerMetadata
	authServerMetadataURL *url.URL

	auth        auth.Authenticator
	mcp         mcpservice.ServerCapabilities
	sessions    *sessioncore.Manager // stateful manager only
	sessionHost sessions.SessionHost

	// Per-session subscription bridges for resources/updated notifications.
	// Map: sessionID -> (uri -> cancel)
	subMu      sync.Mutex
	subCancels map[string]map[string]context.CancelFunc

	// Per-session parent contexts for long-lived activities (e.g., resource
	// subscription forwarders). Each session gets a parent derived from the
	// request context via context.WithoutCancel to preserve values while
	// decoupling from request cancellation. The cancel function is invoked
	// when the session ends or is deemed invalid.
	sessMu      sync.Mutex
	sessParents map[string]context.Context
	sessCancel  map[string]context.CancelFunc

	// Per-session outbound dispatchers and their cancel/unsub handlers for
	// long-lived subscriptions (e.g., notifications/cancelled).
	// (outbound dispatcher removed in stateful refactor)

	// in-flight server-handled requests per session (for inbound cancellation)
	inflightMu sync.Mutex
	inflight   map[string]map[string]context.CancelFunc // sessionID -> requestID -> cancel
}

// lockedWriteFlusher wraps an io.Writer + http.Flusher with a mutex and an optional context.
// It serializes concurrent writes/flushes and avoids writing after ctx is canceled.
type lockedWriteFlusher struct {
	io.Writer
	http.Flusher
	mu  sync.Mutex
	ctx context.Context
}

func (l *lockedWriteFlusher) Write(p []byte) (int, error) {
	if l.ctx != nil && l.ctx.Err() != nil {
		return 0, l.ctx.Err()
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	// Re-check after acquiring the lock to minimize races with cancellation
	if l.ctx != nil && l.ctx.Err() != nil {
		return 0, l.ctx.Err()
	}
	return l.Writer.Write(p)
}

func (l *lockedWriteFlusher) Flush() {
	if l.ctx != nil && l.ctx.Err() != nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.ctx != nil && l.ctx.Err() != nil {
		return
	}
	l.Flusher.Flush()
}

// New constructs a StreamingHTTPHandler using required formal parameters and optional settings.
//
// Required:
//   - publicEndpoint: externally visible URL of the MCP endpoint (scheme, host, path)
//   - host: sessions.SessionHost implementation (horizontal-scale ready)
//   - server: mcpserver.ServerCapabilities implementation
//   - authenticator: auth.Authenticator implementation
//
// Exactly one auth mode option must be provided:
//   - WithAuthorizationServerDiscovery
//   - WithManualOIDC
func New(
	ctx context.Context,
	publicEndpoint string,
	host sessions.SessionHost,
	server mcpservice.ServerCapabilities,
	authenticator auth.Authenticator,
	opts ...Option,
) (*StreamingHTTPHandler, error) {
	if authenticator == nil {
		return nil, fmt.Errorf("authenticator is required")
	}
	if server == nil {
		return nil, fmt.Errorf("server is required")
	}
	if host == nil {
		return nil, fmt.Errorf("SessionHost is required")
	}

	mcpURL, err := url.Parse(publicEndpoint)
	if err != nil {
		return nil, fmt.Errorf("invalid server URL %q: %w", publicEndpoint, err)
	}
	if mcpURL.Scheme != "https" && mcpURL.Scheme != "http" {
		return nil, fmt.Errorf("server URL must use HTTP or HTTPS scheme, got %q", mcpURL.Scheme)
	}

	cfg := &newConfig{}
	for _, opt := range opts {
		opt(cfg)
	}

	if (cfg.discoveryURL == "" && cfg.manualOIDC == nil) || (cfg.discoveryURL != "" && cfg.manualOIDC != nil) {
		return nil, fmt.Errorf("exactly one of WithAuthorizationServerDiscovery or WithManualOIDC must be provided")
	}

	log := slog.Default()
	if cfg.logger != nil {
		log = cfg.logger
	}

	sessions := &sessioncore.Manager{Host: host}

	h := &StreamingHTTPHandler{
		log:         log,
		serverURL:   mcpURL,
		auth:        authenticator,
		mcp:         server,
		sessions:    sessions,
		sessionHost: host,
		subCancels:  make(map[string]map[string]context.CancelFunc),
		sessParents: make(map[string]context.Context),
		sessCancel:  make(map[string]context.CancelFunc),
	}
	h.inflight = make(map[string]map[string]context.CancelFunc)

	// Build PRM based on auth mode
	switch {
	case cfg.manualOIDC != nil:
		manual := cfg.manualOIDC
		if manual.Issuer == "" {
			return nil, fmt.Errorf("issuer is required for manual OIDC")
		}
		if manual.JwksURI == "" {
			return nil, fmt.Errorf("JwksURI is required for manual OIDC")
		}
		h.prmDocument = wellknown.ProtectedResourceMetadata{
			Resource:             mcpURL.String(),
			AuthorizationServers: []string{manual.Issuer},
			JwksURI:              manual.JwksURI,
			ScopesSupported:      manual.ScopesSupported,
			// Per RFC 9728, bearer methods describe how the token is sent to the resource.
			// We support the Authorization header mechanism.
			BearerMethodsSupported: []string{"authorization_header"},
			// ResourceSigningAlgValuesSupported applies if the RESOURCE signs challenges; leave empty unless explicitly supported.
			// ResourceSigningAlgValuesSupported:  manual.ResourceSigningAlgValuesSupported, // not set by default
			ResourceName:                          cfg.serverName,
			ResourceDocumentation:                 manual.ServiceDocumentation,
			ResourcePolicyURI:                     manual.OpPolicyURI,
			ResourceTosURI:                        manual.OpTosURI,
			TLSClientCertificateBoundAccessTokens: false,
			AuthorizationDetailsTypesSupported:    []string{"urn:ietf:params:oauth:authorization-details"},
		}
		// Synthesize a minimal, standards-conformant Authorization Server Metadata doc.
		h.authServerMetadata = wellknown.AuthServerMetadata{
			Issuer:                            manual.Issuer,
			ResponseTypesSupported:            []string{"code"},
			JwksURI:                           manual.JwksURI,
			ScopesSupported:                   manual.ScopesSupported,
			TokenEndpointAuthMethodsSupported: manual.TokenEndpointAuthMethodsSupported,
			TokenEndpointAuthSigningAlgValuesSupported: manual.TokenEndpointAuthSigningAlgValuesSupported,
			ServiceDocumentation:                       manual.ServiceDocumentation,
			OpPolicyURI:                                manual.OpPolicyURI,
			OpTosURI:                                   manual.OpTosURI,
		}
		h.oidcProvider = nil

	case cfg.discoveryURL != "":
		provider, err := oidc.NewProvider(ctx, cfg.discoveryURL)
		if err != nil {
			return nil, fmt.Errorf("failed to create OIDC provider: %w", err)
		}
		var asm *wellknown.AuthServerMetadata
		if err := provider.Claims(&asm); err != nil {
			return nil, fmt.Errorf("unexpected or invalid authorization server metadata: %w", err)
		}
		if asm.JwksURI == "" {
			return nil, fmt.Errorf("the supplied authorization server does not declare support for a JWKS URI in its metadata")
		}
		h.prmDocument = wellknown.ProtectedResourceMetadata{
			Resource:                              mcpURL.String(),
			AuthorizationServers:                  []string{asm.Issuer},
			JwksURI:                               asm.JwksURI,
			ScopesSupported:                       asm.ScopesSupported,
			BearerMethodsSupported:                []string{"authorization_header"},
			ResourceName:                          cfg.serverName,
			ResourceDocumentation:                 asm.ServiceDocumentation,
			ResourcePolicyURI:                     asm.OpPolicyURI,
			ResourceTosURI:                        asm.OpTosURI,
			TLSClientCertificateBoundAccessTokens: false,
			AuthorizationDetailsTypesSupported:    []string{"urn:ietf:params:oauth:authorization-details"},
		}
		// Mirror the discovered AS metadata directly.
		h.authServerMetadata = *asm
		h.oidcProvider = provider
	}

	// Construct PRM document URL
	h.prmDocumentURL = &url.URL{
		Scheme: mcpURL.Scheme,
		Host:   mcpURL.Host,
		Path:   fmt.Sprintf("/.well-known/oauth-protected-resource%s", mcpURL.Path),
	}

	// Construct Authorization Server Metadata mirror URL
	h.authServerMetadataURL = &url.URL{
		Scheme: mcpURL.Scheme,
		Host:   mcpURL.Host,
		Path:   "/.well-known/oauth-authorization-server",
	}

	mux := http.NewServeMux()
	mux.HandleFunc(fmt.Sprintf("POST %s", pathOnly(mcpURL)), h.handlePostMCP)
	mux.HandleFunc(fmt.Sprintf("GET %s", pathOnly(mcpURL)), h.handleGetMCP)
	mux.HandleFunc(fmt.Sprintf("DELETE %s", pathOnly(mcpURL)), h.handleDeleteMCP)

	prmPath := pathOnly(h.prmDocumentURL)
	mux.HandleFunc(fmt.Sprintf("GET %s", prmPath), h.handleGetProtectedResourceMetadata)
	mux.HandleFunc(fmt.Sprintf("OPTIONS %s", prmPath), h.handleOptionsProtectedResourceMetadata)
	if !strings.HasSuffix(prmPath, "/") {
		mux.HandleFunc(fmt.Sprintf("GET %s/", prmPath), h.handleGetProtectedResourceMetadata)
		mux.HandleFunc(fmt.Sprintf("OPTIONS %s/", prmPath), h.handleOptionsProtectedResourceMetadata)
	}

	// Expose a mirror of Authorization Server Metadata for discovery convenience.
	asPath := pathOnly(h.authServerMetadataURL)
	mux.HandleFunc(fmt.Sprintf("GET %s", asPath), h.handleGetAuthorizationServerMetadata)
	mux.HandleFunc(fmt.Sprintf("OPTIONS %s", asPath), h.handleOptionsAuthorizationServerMetadata)
	if !strings.HasSuffix(asPath, "/") {
		mux.HandleFunc(fmt.Sprintf("GET %s/", asPath), h.handleGetAuthorizationServerMetadata)
		mux.HandleFunc(fmt.Sprintf("OPTIONS %s/", asPath), h.handleOptionsAuthorizationServerMetadata)
	}
	h.mux = mux

	return h, nil
}

// hostPath builds the key used for ServeMux's host-aware patterns: "host[:port]" + path.
// For example, for URL "https://example.com:8080/foo/bar", it returns "example.com:8080/foo/bar".
// If the path is empty or "/", treat it as root "/".
// pathOnly returns just the URL path or "/" if empty.
func pathOnly(u *url.URL) string {
	if u == nil {
		return "/"
	}
	if u.Path == "" {
		return "/"
	}
	return u.Path
}

func (h *StreamingHTTPHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mux.ServeHTTP(w, r)
}

// handleDeleteMCP handles the DELETE /mcp endpoint, which terminates an existing
// session. Authentication is required. On success, both persistent host-side
// resources and any process-local ephemeral resources associated with the
// session are cleaned up.
func (h *StreamingHTTPHandler) handleDeleteMCP(w http.ResponseWriter, r *http.Request) {
	slog.Debug("handleDeleteMCP", slog.String("method", r.Method), slog.String("url", r.URL.String()))

	ctx := r.Context()

	userInfo := h.checkAuthentication(ctx, r, w)
	if userInfo == nil {
		return
	}

	sessID := r.Header.Get(mcpSessionIDHeader)
	if sessID == "" {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	// Verify the session belongs to the authenticated user before deletion and
	// capture its protocol version for response headers.
	session, err := h.sessions.LoadSession(ctx, sessID, userInfo.UserID(), "")
	if err != nil {
		// Regardless of validity, ensure any local ephemerals are torn down.
		h.teardownSession(sessID)
		// Do not reveal existence; return 404 for invalid or non-owned sessions.
		w.WriteHeader(http.StatusNotFound)
		return
	}
	respProtoVersion := session.ProtocolVersion()

	// Protocol version handling: if a session-bound protocol version exists, enforce
	// that any provided header matches it. If the header is missing, allow the request
	// (especially important when the session token doesn't encode a version, e.g., non-JWS).
	if pv := r.Header.Get(mcpProtocolVersionHeader); respProtoVersion != "" && pv != "" && pv != respProtoVersion {
		w.WriteHeader(http.StatusPreconditionFailed)
		return
	}

	// Revoke and cleanup persistent resources via the session manager.
	if err := h.sessions.DeleteSession(ctx, sessID); err != nil {
		// Best-effort local teardown even on error.
		h.teardownSession(sessID)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	// Also tear down any local per-session forwarders/parents.
	h.teardownSession(sessID)

	// If we captured a protocol version, advertise it
	if respProtoVersion != "" {
		w.Header().Set(mcpProtocolVersionHeader, respProtoVersion)
	}

	// Success: no content.
	w.WriteHeader(http.StatusNoContent)
}

// handlePostMCP handles the POST /mcp endpoint, which is used by the client to send
// MCP messages to the server and to establish a session.
func (h *StreamingHTTPHandler) handlePostMCP(w http.ResponseWriter, r *http.Request) {
	// h.log.Debug("handlePostMCP", slog.String("method", r.Method), slog.String("url", r.URL.String()))
	// NOTE: We defer Accept negotiation (for text/event-stream) until we know
	// we will actually produce an SSE response (i.e. a request with an ID).
	// Earlier code enforced Accept universally which penalized simple
	// notifications / initialize handshakes returning JSON.
	ctype, err := contenttype.GetMediaType(r)
	if err != nil || !ctype.Matches(jsonMediaType) {
		writeJSONError(w, http.StatusUnsupportedMediaType, "content-type must be application/json")
		return
	}

	f, ok := w.(http.Flusher)
	if !ok {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	ctx := r.Context()
	wf := &lockedWriteFlusher{Writer: w, Flusher: f, ctx: ctx}

	userInfo := h.checkAuthentication(ctx, r, w)
	if userInfo == nil {
		return
	}

	// Prevent JSON-RPC batch usage (spec forbids batching on streaming HTTP transport)
	var raw json.RawMessage
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(&raw); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if len(raw) > 0 && raw[0] == '[' { // rudimentary array detection
		writeJSONError(w, http.StatusBadRequest, "JSON-RPC batch arrays are forbidden on streaming HTTP transport")
		return
	}

	var msg jsonrpc.AnyMessage
	if err := json.Unmarshal(raw, &msg); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid JSON-RPC message: "+err.Error())
		return
	}

	sessID := r.Header.Get(mcpSessionIDHeader)
	if sessID == "" {
		// New session expected; must be initialize request.
		if err := h.handleSessionInitialization(ctx, wf, w, userInfo, msg); err != nil {
			return
		}
		return
	}

	session, err := h.sessions.LoadSession(ctx, sessID, userInfo.UserID(), "")
	if err != nil {
		// Session not found or mismatched user: treat as 404 per spec expectation
		w.WriteHeader(http.StatusNotFound)
		h.teardownSession(sessID)
		return
	}

	// Protocol version header enforcement for non-initialize requests.
	// Future negotiation placeholder: when multi-version support lands, we'll parse the header, compare against supported set,
	// and potentially perform downgrade/upgrade handshakes here.
	if req := msg.AsRequest(); req != nil && req.Method == "initialize" {
		// Second initialize against existing session disallowed.
		writeJSONError(w, http.StatusConflict, "session already initialized")
		return
	}
	clientPV := r.Header.Get(mcpProtocolVersionHeader)
	sessionPV := session.ProtocolVersion()
	if clientPV != "" && sessionPV != "" && clientPV != sessionPV {
		if h.log != nil {
			h.log.Debug("protocol version mismatch", slog.String("session_id", sessID), slog.String("session_version", sessionPV), slog.String("client_version", clientPV))
		}
		writeJSONError(w, http.StatusBadRequest, "protocol version mismatch")
		return
	}
	// If clientPV empty, we fall back to the session-bound version (spec allows
	// server to infer). No 400 in this case.

	// We have a valid session now. Let's see what kind of message it is.
	if req := msg.AsRequest(); req != nil {
		if req.ID.IsNil() {
			if err := h.handleNotification(ctx, session, userInfo, req); err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}

			// The notification was handled successfully.
			if spv := session.ProtocolVersion(); spv != "" {
				w.Header().Set(mcpProtocolVersionHeader, spv)
			}
			w.WriteHeader(http.StatusAccepted)
			return
		}

		// For request-with-ID we will stream a response (SSE). Now negotiate Accept.
		acc := r.Header.Get("Accept")
		if acc != "" { // only enforce if explicitly provided
			if _, _, err := contenttype.GetAcceptableMediaType(r, eventStreamMediaTypes); err != nil {
				if h.log != nil {
					h.log.Debug("reject request: unacceptable explicit Accept for SSE", slog.String("session_id", sessID), slog.String("accept", acc))
				}
				w.WriteHeader(http.StatusUnsupportedMediaType)
				return
			}
		}
		// (If Accept missing, treat as implicit */* and allow.)
		// Set the content type for the SSE response and advertise protocol version
		if spv := session.ProtocolVersion(); spv != "" {
			w.Header().Set(mcpProtocolVersionHeader, spv)
		}
		w.Header().Set("Content-Type", eventStreamMediaType.String())
		w.WriteHeader(http.StatusOK)

		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()

		// We want to wrap the session with a little wrapper type so that we can
		// try to write that session's messages back to the client as part of
		// handling this request. The wrapper will fall back to writing to
		// the session backend if writing to the client fails.
		fallback := func(ctx context.Context, b []byte) error {
			_, err := h.sessionHost.PublishSession(ctx, sessID, b)
			return err
		}
		sess := newSessionWithWriter(ctx, session, wf, fallback)

		// Track inflight cancellable request by session+requestID
		rid := req.ID.String()
		sid := session.SessionID()
		h.inflightMu.Lock()
		if _, ok := h.inflight[sid]; !ok {
			h.inflight[sid] = make(map[string]context.CancelFunc)
		}
		h.inflight[sid][rid] = cancel
		h.inflightMu.Unlock()

		// Inject a ProgressReporter bound to this request ID that writes notifications/progress
		// back to the client via the session writer.
		if mw, ok := any(sess).(messageWriter); ok {
			ctx = mcpservice.WithProgressReporter(ctx, streamingProgressReporter{mw: mw, requestID: rid})
		}

		// This is a request with an ID, so we need to handle it as a request.
		res, err := h.handleRequest(ctx, sess, userInfo, req)
		// Remove inflight entry
		h.inflightMu.Lock()
		if m, ok := h.inflight[sid]; ok {
			delete(m, rid)
			if len(m) == 0 {
				delete(h.inflight, sid)
			}
		}
		h.inflightMu.Unlock()
		if err != nil {
			res = &jsonrpc.Response{
				JSONRPCVersion: jsonrpc.ProtocolVersion,
				Error: &jsonrpc.Error{
					Code:    jsonrpc.ErrorCodeInternalError,
					Message: "internal server error",
				},
				ID: req.ID,
			}
		}

		// TODO: The silliness of needing to write to the _SAME_ sse stream means that we
		// we can't write to the session handle's broker. So here we're stuck with an empty
		// message ID that can't be replayed or recovered :(.
		b, mErr := json.Marshal(res)
		if mErr != nil {
			return
		}
		if err := writeSSEEvent(wf, "", b); err != nil {
			// At this point headers are already written, so we can't change the status code
			// TODO: Log the error
			return
		}

		return
	}

	if res := msg.AsResponse(); res != nil {
		// TODO: Dispatch the response to the communication mesh.

		if err := h.handleResponse(ctx, session, userInfo, res); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		if spv := session.ProtocolVersion(); spv != "" {
			w.Header().Set(mcpProtocolVersionHeader, spv)
		}
		w.WriteHeader(http.StatusAccepted)
		return
	}
}

// handleGetMCP handles the GET /mcp endpoint, which is used to consume messages
// from an established session.
func (h *StreamingHTTPHandler) handleGetMCP(w http.ResponseWriter, r *http.Request) {
	slog.Debug("handleGetMCP", slog.String("method", r.Method), slog.String("url", r.URL.String()))
	_, _, err := contenttype.GetAcceptableMediaType(r, eventStreamMediaTypes)
	if err != nil {
		w.WriteHeader(http.StatusUnsupportedMediaType)
		return
	}

	f, ok := w.(http.Flusher)
	if !ok {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	// Tie SSE stream to the request context; client disconnect cancels ctx.
	ctx := r.Context()
	// Ensure SSE writes are serialized and skip writing after cancellation.
	wf := &lockedWriteFlusher{Writer: w, Flusher: f, ctx: ctx}

	userInfo := h.checkAuthentication(ctx, r, w)
	if userInfo == nil {
		return
	}

	sessionHeader := r.Header.Get(mcpSessionIDHeader)
	if sessionHeader == "" {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	session, err := h.sessions.LoadSession(ctx, sessionHeader, userInfo.UserID(), "")
	if err != nil {
		// Treat any load failure as not found; best-effort cleanup of local resources.
		w.WriteHeader(http.StatusNotFound)
		h.teardownSession(sessionHeader)
		return
	}

	sessionID := session.SessionID()

	// Protocol version handling for stream consumption: if header provided and
	// session has a bound version, require match; otherwise allow.
	if pv := r.Header.Get(mcpProtocolVersionHeader); pv != "" {
		if spv := session.ProtocolVersion(); spv != "" && pv != spv {
			w.WriteHeader(http.StatusPreconditionFailed)
			return
		}
	}

	lastEventID := r.Header.Get(lastEventIDHeader)

	// Option B wiring for resources list_changed notifications:
	// 1) Subscribe to internal fan-out topic and forward as JSON-RPC to the client.
	// 2) Register a local publisher that publishes to the internal topic when the
	//    process-local resources notifier ticks; registration is tied to this GET lifetime.
	const resourcesListChangedTopic = string(mcp.ResourcesListChangedNotificationMethod)

	// Subscribe before we register the publisher to avoid a race dropping first events.
	unsub, subErr := h.sessionHost.SubscribeEvents(ctx, sessionID, resourcesListChangedTopic, func(evtCtx context.Context, payload []byte) error {
		// payload is unused for list_changed; deliver JSON-RPC notification into the session.
		n := &jsonrpc.Request{
			JSONRPCVersion: jsonrpc.ProtocolVersion,
			Method:         string(mcp.ResourcesListChangedNotificationMethod),
		}
		b, err := json.Marshal(n)
		if err != nil {
			return err
		}
		_, err = h.sessionHost.PublishSession(evtCtx, sessionID, b)
		return err
	})
	if subErr != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	defer unsub()

	// Bridge resources/updated notifications via the internal bus as well.
	const resourcesUpdatedTopic = string(mcp.ResourcesUpdatedNotificationMethod)
	unsubUpdated, subUpdatedErr := h.sessionHost.SubscribeEvents(ctx, sessionID, resourcesUpdatedTopic, func(evtCtx context.Context, payload []byte) error {
		// payload is JSON: {"uri": string}
		n := &jsonrpc.Request{
			JSONRPCVersion: jsonrpc.ProtocolVersion,
			Method:         string(mcp.ResourcesUpdatedNotificationMethod),
			Params:         payload,
		}
		b, err := json.Marshal(n)
		if err != nil {
			return err
		}
		_, err = h.sessionHost.PublishSession(evtCtx, sessionID, b)
		return err
	})
	if subUpdatedErr != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	defer unsubUpdated()

	// Also subscribe for tools list_changed to forward to the client
	const toolsListChangedTopic = string(mcp.ToolsListChangedNotificationMethod)
	unsubTools, subToolsErr := h.sessionHost.SubscribeEvents(ctx, sessionID, toolsListChangedTopic, func(evtCtx context.Context, payload []byte) error {
		n := &jsonrpc.Request{
			JSONRPCVersion: jsonrpc.ProtocolVersion,
			Method:         string(mcp.ToolsListChangedNotificationMethod),
			Params:         nil,
		}
		b, err := json.Marshal(n)
		if err != nil {
			return err
		}
		_, err = h.sessionHost.PublishSession(evtCtx, sessionID, b)
		return err
	})
	if subToolsErr != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	defer unsubTools()

	// Also subscribe for prompts list_changed to forward to the client
	const promptsListChangedTopic = string(mcp.PromptsListChangedNotificationMethod)
	unsubPrompts, subPromptsErr := h.sessionHost.SubscribeEvents(ctx, sessionID, promptsListChangedTopic, func(evtCtx context.Context, payload []byte) error {
		n := &jsonrpc.Request{
			JSONRPCVersion: jsonrpc.ProtocolVersion,
			Method:         string(mcp.PromptsListChangedNotificationMethod),
			Params:         nil,
		}
		b, err := json.Marshal(n)
		if err != nil {
			return err
		}
		_, err = h.sessionHost.PublishSession(evtCtx, sessionID, b)
		return err
	})
	if subPromptsErr != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	defer unsubPrompts()

	// Signal to server-internal listeners (e.g., tests) that the SSE stream
	// has finished wiring its internal subscriptions and is ready to receive
	// server-internal events. This event is not forwarded to clients.
	_ = h.sessionHost.PublishEvent(ctx, sessionID, "streaminghttp/ready", nil)

	// Discover resources capability and (optionally) listChanged capability
	if resCap, ok, err := h.mcp.GetResourcesCapability(ctx, session); err == nil && ok {
		if lc, hasLC, lErr := resCap.GetListChangedCapability(ctx, session); lErr == nil && hasLC {
			// Register a local publisher tied to this GET context lifetime
			_, _ = lc.Register(ctx, session, func(cbCtx context.Context, sess sessions.Session, _ string) {
				// best-effort publish to internal topic; no payload required
				_ = h.sessionHost.PublishEvent(cbCtx, sessionID, resourcesListChangedTopic, nil)
			})
		}
	}

	// Bridge tools/list_changed notifications via the internal bus as well.
	if toolsCap, ok, err := h.mcp.GetToolsCapability(ctx, session); err == nil && ok {
		if lc, hasLC, lErr := toolsCap.GetListChangedCapability(ctx, session); lErr == nil && hasLC {
			_, _ = lc.Register(ctx, session, func(cbCtx context.Context, sess sessions.Session) {
				_ = h.sessionHost.PublishEvent(cbCtx, sessionID, toolsListChangedTopic, nil)
			})
		}
	}

	// Bridge prompts/list_changed notifications via the internal bus as well.
	if promptsCap, ok, err := h.mcp.GetPromptsCapability(ctx, session); err == nil && ok {
		if lc, hasLC, lErr := promptsCap.GetListChangedCapability(ctx, session); lErr == nil && hasLC {
			_, _ = lc.Register(ctx, session, func(cbCtx context.Context, sess sessions.Session) {
				_ = h.sessionHost.PublishEvent(cbCtx, sessionID, promptsListChangedTopic, nil)
			})
		}
	}

	// At this point we have a valid session and subscriptions are established.
	// Proactively set SSE headers and advertise protocol version so the client observes the stream
	// immediately, even if the first event arrives later.
	if spv := session.ProtocolVersion(); spv != "" {
		w.Header().Set(mcpProtocolVersionHeader, spv)
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	wf.Flush()

	// TODO(feat/stateful-sessions): Rewire outbound dispatcher for stateful sessions (legacy ensureDispatcher removed).

	// Requests to the GET /mcp endpoint MUST be in the context of an already
	// established session. Session establishment can only happen in POST /mcp.
	// As a result, we can respond with an error in the case of a missing session.
	if err := h.sessionHost.SubscribeSession(ctx, sessionID, lastEventID, func(ctx context.Context, msgID string, bytes []byte) error {
		if err := writeSSEEvent(wf, msgID, bytes); err != nil {
			return fmt.Errorf("failed to write session message to http response: %w", err)
		}
		return nil
	}); err != nil {
		// Headers have already been sent; simply return.
		return
	}

	// Stream ended (client closed, context canceled, or no further messages).
}

// handleGetProtectedResourceMetadata serves the OAuth2 Protected Resource Metadata
// document crafted when the StreamingHTTPHandler initialized.
func (h *StreamingHTTPHandler) handleGetProtectedResourceMetadata(w http.ResponseWriter, r *http.Request) {
	slog.Debug("handleGetProtectedResourceMetadata", slog.String("method", r.Method), slog.String("url", r.URL.String()))
	// CORS: allow cross-origin browser fetches of the well-known metadata
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Vary", "Origin")
	w.Header().Set("Content-Type", "application/json")

	if err := json.NewEncoder(w).Encode(h.prmDocument); err != nil {
		http.Error(w, fmt.Sprintf("failed to encode protected resource metadata: %v", err), http.StatusInternalServerError)
		return
	}
}

// handleOptionsProtectedResourceMetadata responds to CORS preflight requests
// for the protected resource metadata endpoint.
func (h *StreamingHTTPHandler) handleOptionsProtectedResourceMetadata(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Accept, Authorization")
	w.Header().Set("Access-Control-Max-Age", "600")
	w.WriteHeader(http.StatusNoContent)
}

// handleGetAuthorizationServerMetadata serves a mirror or synthesized
// Authorization Server Metadata (RFC 8414). This endpoint is provided as a
// convenience to clients and tooling for discovery purposes. It does not
// imply this process acts as an authorization server.
func (h *StreamingHTTPHandler) handleGetAuthorizationServerMetadata(w http.ResponseWriter, r *http.Request) {
	slog.Debug("handleGetAuthorizationServerMetadata", slog.String("method", r.Method), slog.String("url", r.URL.String()))
	// CORS: allow cross-origin browser fetches of the well-known metadata
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Vary", "Origin")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(h.authServerMetadata); err != nil {
		http.Error(w, fmt.Sprintf("failed to encode authorization server metadata: %v", err), http.StatusInternalServerError)
		return
	}
}

// handleOptionsAuthorizationServerMetadata responds to CORS preflight requests
// for the authorization server metadata endpoint.
func (h *StreamingHTTPHandler) handleOptionsAuthorizationServerMetadata(w http.ResponseWriter, r *http.Request) {
	// Allow any origin; this is a public metadata document.
	w.Header().Set("Access-Control-Allow-Origin", "*")
	// Allow only GET (and OPTIONS). Browsers will send Access-Control-Request-Method: GET
	w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
	// Allow typical headers used by fetch; keep permissive as it's a read-only endpoint.
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Accept, Authorization")
	// Cache preflight for a reasonable period.
	w.Header().Set("Access-Control-Max-Age", "600")
	w.WriteHeader(http.StatusNoContent)
}

func (h *StreamingHTTPHandler) handleNotification(ctx context.Context, session sessions.Session, userInfo auth.UserInfo, req *jsonrpc.Request) error {
	h.log.Debug("handleNotification", slog.String("method", req.Method), slog.String("user", userInfo.UserID()), slog.String("session", session.SessionID()))
	// Intercept notifications/cancelled to cancel in-flight server-handled requests for this session.
	if req.Method == string(mcp.CancelledNotificationMethod) && len(req.Params) > 0 {
		var p struct {
			RequestID string `json:"requestId"`
		}
		if err := json.Unmarshal(req.Params, &p); err == nil && p.RequestID != "" {
			sid := session.SessionID()
			h.inflightMu.Lock()
			if m, ok := h.inflight[sid]; ok {
				if cancel, ok := m[p.RequestID]; ok && cancel != nil {
					cancel()
					delete(m, p.RequestID)
					if len(m) == 0 {
						delete(h.inflight, sid)
					}
				}
			}
			h.inflightMu.Unlock()
		}
	}
	// Publish server-internal event for fan-out to interested listeners.
	// Topic is the JSON-RPC method; payload is the raw params.
	if err := h.sessionHost.PublishEvent(ctx, session.SessionID(), req.Method, req.Params); err != nil {
		return fmt.Errorf("publish internal event: %w", err)
	}
	return nil
}

// handleSessionInitialization handles the initialization of a new MCP session
// when no mcp-session-id header is present in the request.
func (h *StreamingHTTPHandler) handleSessionInitialization(ctx context.Context, wf *lockedWriteFlusher, w http.ResponseWriter, userInfo auth.UserInfo, msg jsonrpc.AnyMessage) error {
	req := msg.AsRequest()
	if req == nil {
		w.WriteHeader(http.StatusBadRequest)
		return fmt.Errorf("expected request message for session initialization")
	}

	if req.Method != "initialize" {
		w.WriteHeader(http.StatusNotFound)
		return fmt.Errorf("expected initialize method, got %s", req.Method)
	}

	var initializeReq mcp.InitializeRequest
	if err := json.Unmarshal(req.Params, &initializeReq); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return fmt.Errorf("failed to unmarshal initialize request: %w", err)
	}

	// Resolve protocol version preference BEFORE minting the session id so the
	// negotiated version can be baked into the token.
	negotiatedVersion := initializeReq.ProtocolVersion
	if v, ok, err := h.mcp.GetPreferredProtocolVersion(ctx); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return fmt.Errorf("failed to get preferred protocol version: %w", err)
	} else if ok && v != "" {
		negotiatedVersion = v
	}

	// Build capability set for stateful metadata
	capSet := sessions.CapabilitySet{}
	if initializeReq.Capabilities.Sampling != nil {
		capSet.Sampling = true
	}
	if initializeReq.Capabilities.Roots != nil {
		capSet.Roots = true
		capSet.RootsListChanged = initializeReq.Capabilities.Roots.ListChanged
	}
	if initializeReq.Capabilities.Elicitation != nil {
		capSet.Elicitation = true
	}

	clientName := initializeReq.ClientInfo.Name
	clientVersion := initializeReq.ClientInfo.Version
	session, err := h.sessions.CreateSession(ctx, userInfo.UserID(), negotiatedVersion, capSet, sessions.MetadataClientInfo{Name: clientName, Version: clientVersion})
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return fmt.Errorf("failed to create session: %w", err)
	}

	serverInfo, err := h.mcp.GetServerInfo(ctx, session)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return fmt.Errorf("failed to get server info: %w", err)
	}

	initializeRes := &mcp.InitializeResult{
		ProtocolVersion: negotiatedVersion,
		ServerInfo:      serverInfo,
	}

	// Optional instructions
	if instr, ok, err := h.mcp.GetInstructions(ctx, session); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return fmt.Errorf("failed to get instructions: %w", err)
	} else if ok {
		initializeRes.Instructions = instr
	}

	resCap, hasResourcesCap, err := h.mcp.GetResourcesCapability(ctx, session)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return fmt.Errorf("failed to get resources capability: %w", err)
	}
	if hasResourcesCap {
		_, hasChangeSubCap, err := resCap.GetSubscriptionCapability(ctx, session)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return fmt.Errorf("failed to get resources subscription capability: %w", err)
		}

		_, hasListChangedCap, err := resCap.GetListChangedCapability(ctx, session)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return fmt.Errorf("failed to get resources listChanged capability: %w", err)
		}

		initializeRes.Capabilities.Resources = &struct {
			ListChanged bool "json:\"listChanged\""
			Subscribe   bool "json:\"subscribe\""
		}{
			ListChanged: hasListChangedCap,
			Subscribe:   hasChangeSubCap,
		}
	}

	// Discover tools capability and (optionally) listChanged capability
	if toolsCap, hasToolsCap, err := h.mcp.GetToolsCapability(ctx, session); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return fmt.Errorf("failed to get tools capability: %w", err)
	} else if hasToolsCap {
		_, hasToolsListChanged, err := toolsCap.GetListChangedCapability(ctx, session)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return fmt.Errorf("failed to get tools listChanged capability: %w", err)
		}

		initializeRes.Capabilities.Tools = &struct {
			ListChanged bool "json:\"listChanged\""
		}{
			ListChanged: hasToolsListChanged,
		}
	}

	// Discover prompts capability and (optionally) listChanged capability
	if promptsCap, hasPromptsCap, err := h.mcp.GetPromptsCapability(ctx, session); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return fmt.Errorf("failed to get prompts capability: %w", err)
	} else if hasPromptsCap {
		_, hasPromptsListChanged, err := promptsCap.GetListChangedCapability(ctx, session)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return fmt.Errorf("failed to get prompts listChanged capability: %w", err)
		}

		initializeRes.Capabilities.Prompts = &struct {
			ListChanged bool "json:\"listChanged\""
		}{
			ListChanged: hasPromptsListChanged,
		}
	}

	// Discover logging capability
	if _, ok, err := h.mcp.GetLoggingCapability(ctx, session); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return fmt.Errorf("failed to get logging capability: %w", err)
	} else if ok {
		initializeRes.Capabilities.Logging = &struct{}{}
	}

	// Discover completions capability
	if _, ok, err := h.mcp.GetCompletionsCapability(ctx, session); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return fmt.Errorf("failed to get completions capability: %w", err)
	} else if ok {
		initializeRes.Capabilities.Completions = &struct{}{}
	}

	res, err := jsonrpc.NewResultResponse(req.ID, initializeRes)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return fmt.Errorf("failed to create initialize response: %w", err)
	}

	w.Header().Set(mcpSessionIDHeader, session.SessionID())
	// Advertise the negotiated protocol version on the response
	if negotiatedVersion != "" {
		w.Header().Set(mcpProtocolVersionHeader, negotiatedVersion)
	}
	w.Header().Set("Content-Type", jsonMediaType.String())
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(res); err != nil {
		return fmt.Errorf("failed to encode initialize response: %w", err)
	}

	return nil
}

// mapHooksErrorToJSONRPCError maps specific hooks errors to appropriate JSON-RPC error responses
func mapHooksErrorToJSONRPCError(requestID *jsonrpc.RequestID, err error) *jsonrpc.Response {
	// Future: inspect error types to map to specific JSON-RPC codes.
	return jsonrpc.NewErrorResponse(requestID, jsonrpc.ErrorCodeInternalError, "internal error", nil)
}

func (h *StreamingHTTPHandler) handleRequest(ctx context.Context, session sessions.Session, userInfo auth.UserInfo, req *jsonrpc.Request) (*jsonrpc.Response, error) {
	h.log.Debug("handleRequest", slog.String("method", req.Method), slog.String("user", userInfo.UserID()), slog.String("session", session.SessionID()))

	switch req.Method {
	case string(mcp.PingMethod):
		return jsonrpc.NewResultResponse(req.ID, struct{}{})
	case string(mcp.ToolsListMethod):
		toolsCap, ok, err := h.mcp.GetToolsCapability(ctx, session)
		if err != nil {
			return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeInternalError, "internal error", nil), nil
		}
		if !ok || toolsCap == nil {
			return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeMethodNotFound, "tools capability not supported", nil), nil
		}

		var listToolsReq mcp.ListToolsRequest
		if err := json.Unmarshal(req.Params, &listToolsReq); err != nil {
			return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeInvalidParams, "invalid parameters", nil), nil
		}

		page, err := toolsCap.ListTools(ctx, session, func() *string {
			if listToolsReq.Cursor == "" {
				return nil
			}
			s := listToolsReq.Cursor
			return &s
		}())
		if err != nil {
			return mapHooksErrorToJSONRPCError(req.ID, err), nil
		}

		return jsonrpc.NewResultResponse(req.ID, &mcp.ListToolsResult{
			Tools: page.Items,
			PaginatedResult: mcp.PaginatedResult{
				NextCursor: func() string {
					if page.NextCursor == nil {
						return ""
					}
					return *page.NextCursor
				}(),
			},
		})
	case string(mcp.ToolsCallMethod):
		toolsCap, ok, err := h.mcp.GetToolsCapability(ctx, session)
		if err != nil {
			return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeInternalError, "internal error", nil), nil
		}
		if !ok || toolsCap == nil {
			return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeMethodNotFound, "tools capability not supported", nil), nil
		}

		var callToolReq mcp.CallToolRequestReceived
		if err := json.Unmarshal(req.Params, &callToolReq); err != nil {
			return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeInvalidParams, "invalid parameters", nil), nil
		}

		result, err := toolsCap.CallTool(ctx, session, &callToolReq)
		if err != nil {
			return mapHooksErrorToJSONRPCError(req.ID, err), nil
		}

		return jsonrpc.NewResultResponse(req.ID, result)
	case string(mcp.ResourcesListMethod):
		resourcesCap, ok, err := h.mcp.GetResourcesCapability(ctx, session)
		if err != nil {
			return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeInternalError, "internal error", nil), nil
		}
		if !ok {
			return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeMethodNotFound, "resources capability not supported", nil), nil
		}

		var listResourcesReq mcp.ListResourcesRequest
		if err := json.Unmarshal(req.Params, &listResourcesReq); err != nil {
			return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeInvalidParams, "invalid parameters", nil), nil
		}

		page, err := resourcesCap.ListResources(ctx, session, func() *string {
			if listResourcesReq.Cursor == "" {
				return nil
			}
			s := listResourcesReq.Cursor
			return &s
		}())
		if err != nil {
			return mapHooksErrorToJSONRPCError(req.ID, err), nil
		}

		return jsonrpc.NewResultResponse(req.ID, &mcp.ListResourcesResult{
			Resources: page.Items,
			PaginatedResult: mcp.PaginatedResult{
				NextCursor: func() string {
					if page.NextCursor == nil {
						return ""
					}
					return *page.NextCursor
				}(),
			},
		})
	case string(mcp.ResourcesReadMethod):
		resourcesCap, ok, err := h.mcp.GetResourcesCapability(ctx, session)
		if err != nil {
			return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeInternalError, "internal error", nil), nil
		}
		if !ok {
			return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeMethodNotFound, "resources capability not supported", nil), nil
		}

		var readResourceReq mcp.ReadResourceRequest
		if err := json.Unmarshal(req.Params, &readResourceReq); err != nil {
			return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeInvalidParams, "invalid parameters", nil), nil
		}

		contents, err := resourcesCap.ReadResource(ctx, session, readResourceReq.URI)
		if err != nil {
			return mapHooksErrorToJSONRPCError(req.ID, err), nil
		}

		return jsonrpc.NewResultResponse(req.ID, &mcp.ReadResourceResult{
			Contents: contents,
		})
	case string(mcp.ResourcesTemplatesListMethod):
		resourcesCap, ok, err := h.mcp.GetResourcesCapability(ctx, session)
		if err != nil {
			return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeInternalError, "internal error", nil), nil
		}
		if !ok {
			return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeMethodNotFound, "resources capability not supported", nil), nil
		}

		var listTemplatesReq mcp.ListResourceTemplatesRequest
		if err := json.Unmarshal(req.Params, &listTemplatesReq); err != nil {
			return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeInvalidParams, "invalid parameters", nil), nil
		}

		page, err := resourcesCap.ListResourceTemplates(ctx, session, func() *string {
			if listTemplatesReq.Cursor == "" {
				return nil
			}
			s := listTemplatesReq.Cursor
			return &s
		}())
		if err != nil {
			return mapHooksErrorToJSONRPCError(req.ID, err), nil
		}

		return jsonrpc.NewResultResponse(req.ID, &mcp.ListResourceTemplatesResult{
			ResourceTemplates: page.Items,
			PaginatedResult: mcp.PaginatedResult{
				NextCursor: func() string {
					if page.NextCursor == nil {
						return ""
					}
					return *page.NextCursor
				}(),
			},
		})
	case string(mcp.ResourcesSubscribeMethod):
		resourcesCap, ok, err := h.mcp.GetResourcesCapability(ctx, session)
		if err != nil {
			return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeInternalError, "internal error", nil), nil
		}
		if !ok {
			return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeMethodNotFound, "resources capability not supported", nil), nil
		}

		var subscribeReq mcp.SubscribeRequest
		if err := json.Unmarshal(req.Params, &subscribeReq); err != nil {
			return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeInvalidParams, "invalid parameters", nil), nil
		}

		subCap, ok, err := resourcesCap.GetSubscriptionCapability(ctx, session)
		if err != nil {
			return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeInternalError, "internal error", nil), nil
		}
		if !ok || subCap == nil {
			return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeMethodNotFound, "resources subscription capability not supported", nil), nil
		}

		if err := subCap.Subscribe(ctx, session, subscribeReq.URI); err != nil {
			return mapHooksErrorToJSONRPCError(req.ID, err), nil
		}

		// Maintain a per-session, per-URI forwarder that listens for local update ticks
		// and publishes notifications/resources/updated onto the session bus. If a forwarder
		// already exists for this (session, uri), do nothing (idempotent).
		// Establish forwarder only if the capability is our fs-backed implementation or any implementation
		// that exposes an internal Subscriber channel via ChangeNotifier pattern.
		if fsCap, ok, _ := h.mcp.GetResourcesCapability(ctx, session); ok {
			// Best-effort: try to access a subscriber channel for this URI if supported by the implementation.
			type uriSubscriber interface {
				SubscriberForURI(uri string) <-chan struct{}
			}
			if us, uok := any(fsCap).(uriSubscriber); uok {
				ch := us.SubscriberForURI(subscribeReq.URI)
				if ch != nil {
					sid := session.SessionID()
					h.subMu.Lock()
					if _, ok := h.subCancels[sid]; !ok {
						h.subCancels[sid] = make(map[string]context.CancelFunc)
					}
					if _, exists := h.subCancels[sid][subscribeReq.URI]; !exists {
						// Forwarder lifetime is tied to the session (until unsubscribe).
						// Derive from the per-session parent context that preserves values
						// but is not canceled by the request.
						pctx := h.ensureSessionParentContext(ctx, sid)
						fctx, cancel := context.WithCancel(pctx)
						h.subCancels[sid][subscribeReq.URI] = cancel
						go func(sess sessions.Session, uri string, ch <-chan struct{}) {
							defer func() {
								// On forwarder exit, clean up cancel map entry if still present
								h.subMu.Lock()
								if m, ok := h.subCancels[sid]; ok {
									if _, ok := m[subscribeReq.URI]; ok {
										delete(m, subscribeReq.URI)
										if len(m) == 0 {
											delete(h.subCancels, sid)
										}
									}
								}
								h.subMu.Unlock()
							}()
							for {
								select {
								case <-fctx.Done():
									return
								case _, ok := <-ch:
									if !ok {
										return
									}
									// Publish JSON payload {"uri": uri}
									payload, _ := json.Marshal(&mcp.ResourceUpdatedNotification{URI: uri})
									_ = h.sessionHost.PublishEvent(fctx, sess.SessionID(), string(mcp.ResourcesUpdatedNotificationMethod), payload)
								}
							}
						}(session, subscribeReq.URI, ch)
					}
					h.subMu.Unlock()
				}
			}
		}

		return jsonrpc.NewResultResponse(req.ID, struct{}{})
	case string(mcp.ResourcesUnsubscribeMethod):
		resourcesCap, ok, err := h.mcp.GetResourcesCapability(ctx, session)
		if err != nil {
			return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeInternalError, "internal error", nil), nil
		}
		if !ok {
			return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeMethodNotFound, "resources capability not supported", nil), nil
		}

		var subscribeReq mcp.SubscribeRequest
		if err := json.Unmarshal(req.Params, &subscribeReq); err != nil {
			return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeInvalidParams, "invalid parameters", nil), nil
		}

		subCap, ok, err := resourcesCap.GetSubscriptionCapability(ctx, session)
		if err != nil {
			return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeInternalError, "internal error", nil), nil
		}
		if !ok || subCap == nil {
			return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeMethodNotFound, "resources subscription capability not supported", nil), nil
		}

		if err := subCap.Unsubscribe(ctx, session, subscribeReq.URI); err != nil {
			return mapHooksErrorToJSONRPCError(req.ID, err), nil
		}

		// Tear down the forwarder if present
		{
			sid := session.SessionID()
			h.subMu.Lock()
			if m, ok := h.subCancels[sid]; ok {
				if cancel, ok := m[subscribeReq.URI]; ok {
					cancel()
					delete(m, subscribeReq.URI)
					if len(m) == 0 {
						delete(h.subCancels, sid)
					}
				}
			}
			h.subMu.Unlock()
		}

		return jsonrpc.NewResultResponse(req.ID, struct{}{})
	case string(mcp.PromptsListMethod):
		promptsCap, ok, err := h.mcp.GetPromptsCapability(ctx, session)
		if err != nil {
			return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeInternalError, "internal error", nil), nil
		}
		if !ok || promptsCap == nil {
			return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeMethodNotFound, "prompts capability not supported", nil), nil
		}

		var listPromptsReq mcp.ListPromptsRequest
		if err := json.Unmarshal(req.Params, &listPromptsReq); err != nil {
			return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeInvalidParams, "invalid parameters", nil), nil
		}

		page, err := promptsCap.ListPrompts(ctx, session, func() *string {
			if listPromptsReq.Cursor == "" {
				return nil
			}
			s := listPromptsReq.Cursor
			return &s
		}())
		if err != nil {
			return mapHooksErrorToJSONRPCError(req.ID, err), nil
		}

		return jsonrpc.NewResultResponse(req.ID, &mcp.ListPromptsResult{
			Prompts: page.Items,
			PaginatedResult: mcp.PaginatedResult{
				NextCursor: func() string {
					if page.NextCursor == nil {
						return ""
					}
					return *page.NextCursor
				}(),
			},
		})
	case string(mcp.PromptsGetMethod):
		promptsCap, ok, err := h.mcp.GetPromptsCapability(ctx, session)
		if err != nil {
			return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeInternalError, "internal error", nil), nil
		}
		if !ok || promptsCap == nil {
			return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeMethodNotFound, "prompts capability not supported", nil), nil
		}

		var getPromptReq mcp.GetPromptRequestReceived
		if err := json.Unmarshal(req.Params, &getPromptReq); err != nil {
			return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeInvalidParams, "invalid parameters", nil), nil
		}

		result, err := promptsCap.GetPrompt(ctx, session, &getPromptReq)
		if err != nil {
			return mapHooksErrorToJSONRPCError(req.ID, err), nil
		}

		return jsonrpc.NewResultResponse(req.ID, result)
	case string(mcp.LoggingSetLevelMethod):
		loggingCap, ok, err := h.mcp.GetLoggingCapability(ctx, session)
		if err != nil {
			return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeInternalError, "internal error", nil), nil
		}
		if !ok || loggingCap == nil {
			return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeMethodNotFound, "logging capability not supported", nil), nil
		}

		var setLevelReq mcp.SetLevelRequest
		if err := json.Unmarshal(req.Params, &setLevelReq); err != nil {
			return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeInvalidParams, "invalid parameters", nil), nil
		}

		if !mcp.IsValidLoggingLevel(setLevelReq.Level) {
			return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeInvalidParams, "invalid logging level", nil), nil
		}

		if err := loggingCap.SetLevel(ctx, session, setLevelReq.Level); err != nil {
			if errors.Is(err, mcpservice.ErrInvalidLoggingLevel) {
				return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeInvalidParams, "invalid logging level", nil), nil
			}
			return mapHooksErrorToJSONRPCError(req.ID, err), nil
		}

		return jsonrpc.NewResultResponse(req.ID, struct{}{})
	case string(mcp.CompletionCompleteMethod):
		completionsCap, ok, err := h.mcp.GetCompletionsCapability(ctx, session)
		if err != nil {
			return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeInternalError, "internal error", nil), nil
		}
		if !ok || completionsCap == nil {
			return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeMethodNotFound, "completions capability not supported", nil), nil
		}

		var completeReq mcp.CompleteRequest
		if err := json.Unmarshal(req.Params, &completeReq); err != nil {
			return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeInvalidParams, "invalid parameters", nil), nil
		}

		result, err := completionsCap.Complete(ctx, session, &completeReq)
		if err != nil {
			return mapHooksErrorToJSONRPCError(req.ID, err), nil
		}

		return jsonrpc.NewResultResponse(req.ID, result)
		// case string(mcp.LoggingSetLevelMethod):
		// 	loggingCap := h.hooks.GetLoggingCapability()

		// 	if loggingCap == nil {
		// 		return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeMethodNotFound, "logging capability not supported", nil), nil
		// 	}

		// 	var setLevelReq mcp.SetLevelRequest
		// 	if err := json.Unmarshal(req.Params, &setLevelReq); err != nil {
		// 		return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeInvalidParams, "invalid parameters", nil), nil
		// 	}

		// 	err := loggingCap.SetLevel(ctx, session, setLevelReq.Level)
		// 	if err != nil {
		// 		return mapHooksErrorToJSONRPCError(req.ID, err), nil
		// 	}

		// 	return jsonrpc.NewResultResponse(req.ID, struct{}{})
		// case string(mcp.CompletionCompleteMethod):
		// 	completionsCap := h.hooks.GetCompletionsCapability()

		// 	if completionsCap == nil {
		// 		return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeMethodNotFound, "completions capability not supported", nil), nil
		// 	}

		// 	var completeReq mcp.CompleteRequest
		// 	if err := json.Unmarshal(req.Params, &completeReq); err != nil {
		// 		return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeInvalidParams, "invalid parameters", nil), nil
		// 	}

		// 	result, err := completionsCap.Complete(ctx, session, &completeReq)
		// 	if err != nil {
		// 		return mapHooksErrorToJSONRPCError(req.ID, err), nil
		// 	}

		// 	return jsonrpc.NewResultResponse(req.ID, result)
	}
	// TODO: Actually handle the request
	return &jsonrpc.Response{
		JSONRPCVersion: jsonrpc.ProtocolVersion,
		Error: &jsonrpc.Error{
			Code:    jsonrpc.ErrorCodeMethodNotFound,
			Message: "method not found",
			Data: map[string]string{
				"method": req.Method,
			},
		},
		ID: req.ID,
	}, nil
}

func (h *StreamingHTTPHandler) handleResponse(ctx context.Context, session sessions.Session, userInfo auth.UserInfo, res *jsonrpc.Response) error {
	h.log.Debug("handleResponse", slog.String("user", userInfo.UserID()), slog.String("session", session.SessionID()))
	if res == nil || res.ID == nil || res.ID.IsNil() {
		return fmt.Errorf("response missing id")
	}

	// Re-encode to bytes for delivery to the awaiting goroutine on any instance.
	payload, err := json.Marshal(res)
	if err != nil {
		return fmt.Errorf("marshal response: %w", err)
	}

	// Deliver to rendezvous topic as a server-internal event; drop if no subscriber.
	topic := "rv:" + res.ID.String()
	if err := h.sessionHost.PublishEvent(ctx, session.SessionID(), topic, payload); err != nil {
		return fmt.Errorf("publish rendezvous event: %w", err)
	}
	return nil
}

func (h *StreamingHTTPHandler) checkAuthentication(ctx context.Context, r *http.Request, w http.ResponseWriter) auth.UserInfo {
	authHeader := r.Header.Get(authorizationHeader)

	if authHeader == "" {
		w.Header().Add(wwwAuthenticateHeader, fmt.Sprintf(`Bearer realm="%s", error="invalid_token", error_description="no token provided"`, h.serverURL.String()))
		w.WriteHeader(http.StatusUnauthorized)
		return nil
	}

	tok, ok := strings.CutPrefix(authHeader, "Bearer ")
	if !ok || tok == "" {
		w.Header().Add(wwwAuthenticateHeader, fmt.Sprintf(`Bearer realm="%s", error="invalid_request", error_description="invalid or absent authorization header"`, h.serverURL.String()))
		w.WriteHeader(http.StatusBadRequest)
		return nil
	}

	userInfo, err := h.auth.CheckAuthentication(ctx, tok)
	if err != nil {
		if errors.Is(err, auth.ErrUnauthorized) {
			w.Header().Add(wwwAuthenticateHeader, fmt.Sprintf(`Bearer realm="%s", error="invalid_token", error_description="%s"`, h.serverURL.String(), err.Error()))
			w.WriteHeader(http.StatusUnauthorized)
			return nil
		}

		if errors.Is(err, auth.ErrInsufficientScope) {
			w.Header().Add(wwwAuthenticateHeader, fmt.Sprintf(`Bearer realm="%s", error="insufficient_scope", error_description="%s"`, h.serverURL.String(), err.Error()))
			w.WriteHeader(http.StatusForbidden)
			return nil
		}

		w.WriteHeader(http.StatusInternalServerError)
		return nil
	}

	return userInfo
}

// writeSSEEvent writes a Server-Sent Event to the response writer with the given event type and message.
// The message will be JSON encoded and written as the data field of the SSE event.
// It automatically flushes the response after writing.
func writeSSEEvent(wf *lockedWriteFlusher, msgID string, payload []byte) error {
	if msgID != "" {
		if _, err := fmt.Fprintf(wf, "id: %s\n", msgID); err != nil {
			return fmt.Errorf("failed to write SSE event ID: %w", err)
		}
	}
	if _, err := wf.Write([]byte("data: ")); err != nil {
		return fmt.Errorf("failed to write SSE data prefix: %w", err)
	}
	if _, err := wf.Write(payload); err != nil {
		return fmt.Errorf("failed to write SSE payload: %w", err)
	}
	if _, err := wf.Write([]byte("\n\n")); err != nil {
		return fmt.Errorf("failed to write SSE frame terminator: %w", err)
	}
	wf.Flush()
	return nil
}

var _ sessions.Session = (*sessionWithWriter)(nil)

func newSessionWithWriter(ctx context.Context, session sessions.Session, wf *lockedWriteFlusher, fallback func(context.Context, []byte) error) sessions.Session {
	return &sessionWithWriter{
		Session:  session,
		reqCtx:   ctx,
		wf:       wf,
		mu:       sync.Mutex{},
		fallback: fallback,
	}
}

type sessionWithWriter struct {
	sessions.Session
	reqCtx   context.Context
	wf       *lockedWriteFlusher
	mu       sync.Mutex // Guards concurrent writes to wf
	fallback func(context.Context, []byte) error
}

func (s *sessionWithWriter) WriteMessage(ctx context.Context, bytes []byte) error {
	// Check the request context has been cancelled. If it has, we should not
	// attempt to write to the response writer as it may be closed.
	// Instead, we fall back to the original session's WriteMessage method.
	// This can happen if the client disconnects while we're trying to write
	// a message to the response.
	if s.reqCtx.Err() != nil {
		// The underlying write flusher should no longer be used so fall back to the original writer
		if s.fallback != nil {
			return s.fallback(ctx, bytes)
		}
		return fmt.Errorf("no fallback available for session WriteMessage")
	}

	s.mu.Lock()
	err := writeSSEEvent(s.wf, "", bytes)
	s.mu.Unlock()

	if err != nil {
		// TODO: Log error?

		// We failed to write the message to the HTTP response. We'll fall back to the provided writer.
		if s.fallback != nil {
			return s.fallback(ctx, bytes)
		}
		return err
	}

	return nil
}

// ensureSessionParentContext returns a per-session parent context that preserves
// the values of parent (using context.WithoutCancel) but is not canceled with it.
// The returned context is canceled only when teardownSession is called for the
// session, or when all per-URI forwarders are explicitly unsubscribed and a
// later teardown occurs. Safe for concurrent use.
func (h *StreamingHTTPHandler) ensureSessionParentContext(parent context.Context, sessionID string) context.Context {
	h.sessMu.Lock()
	defer h.sessMu.Unlock()
	if ctx, ok := h.sessParents[sessionID]; ok && ctx != nil {
		return ctx
	}
	base := context.WithoutCancel(parent)
	ctx, cancel := context.WithCancel(base)
	if h.sessParents == nil {
		h.sessParents = make(map[string]context.Context)
	}
	if h.sessCancel == nil {
		h.sessCancel = make(map[string]context.CancelFunc)
	}
	h.sessParents[sessionID] = ctx
	h.sessCancel[sessionID] = cancel
	return ctx
}

// teardownSession cancels the per-session parent context (if any) and cancels
// all per-URI forwarders. It also removes bookkeeping entries. This does not
// delete the session from the host; it only tears down local resources.
func (h *StreamingHTTPHandler) teardownSession(sessionID string) {
	// Cancel per-session parent first to cascade cancellation to children.
	h.sessMu.Lock()
	if h.sessCancel != nil {
		if cancel, ok := h.sessCancel[sessionID]; ok && cancel != nil {
			cancel()
			delete(h.sessCancel, sessionID)
		}
	}
	if h.sessParents != nil {
		delete(h.sessParents, sessionID)
	}
	h.sessMu.Unlock()

	// Also cancel any lingering per-URI forwarders and clear their entries.
	h.subMu.Lock()
	if m, ok := h.subCancels[sessionID]; ok {
		for _, c := range m {
			if c != nil {
				c()
			}
		}
		delete(h.subCancels, sessionID)
	}
	h.subMu.Unlock()

	// (outbound dispatcher removal: no-op)
}

// messageWriter is satisfied by sessionWithWriter; it provides the low-level message send primitive.
type messageWriter interface {
	WriteMessage(context.Context, []byte) error
}

// streamingProgressReporter emits notifications/progress for a given request over the session stream.
type streamingProgressReporter struct {
	mw        messageWriter
	requestID string
}

func (p streamingProgressReporter) Report(ctx context.Context, progress, total float64) error {
	params := mcp.ProgressNotificationParams{ProgressToken: p.requestID, Progress: progress}
	if total > 0 {
		params.Total = total
	}
	n := &jsonrpc.Request{JSONRPCVersion: jsonrpc.ProtocolVersion, Method: string(mcp.ProgressNotificationMethod)}
	b, err := json.Marshal(params)
	if err != nil {
		return err
	}
	n.Params = b
	msg, err := json.Marshal(n)
	if err != nil {
		return err
	}
	return p.mw.WriteMessage(ctx, msg)
}
