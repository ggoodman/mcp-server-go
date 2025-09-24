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
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/elnormous/contenttype"
	"github.com/ggoodman/mcp-server-go/auth"
	"github.com/ggoodman/mcp-server-go/internal/engine"
	"github.com/ggoodman/mcp-server-go/internal/jsonrpc"
	"github.com/ggoodman/mcp-server-go/internal/logctx"
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
	// internal engine fanout topic for rendezvous and notifications
	sessionFanoutTopic = "session:events"
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
	eng         *engine.Engine
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

	h := &StreamingHTTPHandler{
		log:         log,
		serverURL:   mcpURL,
		auth:        authenticator,
		mcp:         server,
		sessionHost: host,
		subCancels:  make(map[string]map[string]context.CancelFunc),
		sessParents: make(map[string]context.Context),
		sessCancel:  make(map[string]context.CancelFunc),
	}

	// Initialize Engine and start its event consumer loop.
	h.eng = engine.NewEngine(host, server, engine.WithLogger(log))
	go func() {
		if err := h.eng.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
			log.Error("engine.run.fail", slog.String("err", err.Error()))
		}
	}()

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
	start := time.Now()
	baseCtx := r.Context()
	reqID := logctx.NewRequestID()
	ctx := logctx.WithRequestID(baseCtx, reqID)
	r = r.WithContext(ctx)
	logger := logctx.Enrich(ctx, h.log).With(
		slog.String("transport", "streaminghttp"),
		slog.String("op", "delete_session"),
		slog.String("http_method", r.Method),
		slog.String("path", r.URL.Path),
	)
	logger.InfoContext(ctx, "http.delete.start")

	userInfo := h.checkAuthentication(ctx, r, w)
	if userInfo == nil {
		logger.InfoContext(ctx, "auth.fail")
		return
	}
	ctx = logctx.WithUserID(ctx, userInfo.UserID())
	logger = logctx.Enrich(ctx, logger)
	logger.InfoContext(ctx, "auth.ok")

	sessID := r.Header.Get(mcpSessionIDHeader)
	if sessID == "" {
		logger.WarnContext(ctx, "delete.missing_session_id")
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	ctx = logctx.WithSessionID(ctx, sessID)
	logger = logctx.Enrich(ctx, logger)

	// Verify ownership & existence via host metadata
	meta, err := h.sessionHost.GetSession(ctx, sessID)
	if err != nil || meta == nil || meta.Revoked || meta.UserID != userInfo.UserID() {
		logger.InfoContext(ctx, "session.load.miss")
		h.teardownSession(sessID)
		w.WriteHeader(http.StatusNotFound)
		return
	}
	logger.InfoContext(ctx, "session.load.ok")
	respProtoVersion := meta.ProtocolVersion

	if pv := r.Header.Get(mcpProtocolVersionHeader); respProtoVersion != "" && pv != "" && pv != respProtoVersion {
		logger.WarnContext(ctx, "protocol.version.mismatch", slog.String("session_version", respProtoVersion), slog.String("client_version", pv))
		w.WriteHeader(http.StatusPreconditionFailed)
		return
	}

	if err := h.sessionHost.DeleteSession(ctx, sessID); err != nil {
		logger.ErrorContext(ctx, "session.delete.fail", slog.String("err", err.Error()))
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
	logger.InfoContext(ctx, "http.delete.ok", slog.Duration("dur", time.Since(start)))
}

// handlePostMCP handles the POST /mcp endpoint, which is used by the client to send
// MCP messages to the server and to establish a session.
func (h *StreamingHTTPHandler) handlePostMCP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	baseCtx := r.Context()
	reqID := logctx.NewRequestID()
	ctx := logctx.WithRequestID(baseCtx, reqID)
	r = r.WithContext(ctx)
	logger := logctx.Enrich(ctx, h.log).With(
		slog.String("transport", "streaminghttp"),
		slog.String("op", "post_mcp"),
		slog.String("http_method", r.Method),
		slog.String("path", r.URL.Path),
	)
	logger.InfoContext(ctx, "http.post.start")

	ctype, err := contenttype.GetMediaType(r)
	if err != nil || !ctype.Matches(jsonMediaType) {
		writeJSONError(w, http.StatusUnsupportedMediaType, "content-type must be application/json")
		logger.WarnContext(ctx, "content_type.unsupported")
		return
	}

	f, ok := w.(http.Flusher)
	if !ok {
		w.WriteHeader(http.StatusInternalServerError)
		logger.ErrorContext(ctx, "flusher.missing")
		return
	}

	ctx = r.Context()
	wf := &lockedWriteFlusher{Writer: w, Flusher: f, ctx: ctx}

	userInfo := h.checkAuthentication(ctx, r, w)
	if userInfo == nil {
		logger.InfoContext(ctx, "auth.fail")
		return
	}
	ctx = logctx.WithUserID(ctx, userInfo.UserID())
	logger = logctx.Enrich(ctx, logger)
	logger.InfoContext(ctx, "auth.ok")

	var raw json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid JSON body")
		logger.WarnContext(ctx, "json.decode.fail", slog.String("err", err.Error()))
		return
	}
	if len(raw) > 0 && raw[0] == '[' {
		writeJSONError(w, http.StatusBadRequest, "JSON-RPC batch arrays are forbidden on streaming HTTP transport")
		logger.WarnContext(ctx, "jsonrpc.batch.forbidden")
		return
	}

	var msg jsonrpc.AnyMessage
	if err := json.Unmarshal(raw, &msg); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid JSON-RPC message: "+err.Error())
		logger.WarnContext(ctx, "jsonrpc.message.invalid", slog.String("err", err.Error()))
		return
	}

	sessID := r.Header.Get(mcpSessionIDHeader)
	if sessID == "" {
		// Session initialization via Engine
		req := msg.AsRequest()
		if req == nil || req.Method != string(mcp.InitializeMethod) {
			writeJSONError(w, http.StatusNotFound, "expected initialize request")
			logger.WarnContext(ctx, "session.initialize.invalid")
			return
		}
		var initReq mcp.InitializeRequest
		if err := json.Unmarshal(req.Params, &initReq); err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid initialize params")
			logger.WarnContext(ctx, "session.initialize.params.fail", slog.String("err", err.Error()))
			return
		}
		sess, initRes, err := h.eng.InitializeSession(ctx, userInfo.UserID(), &initReq)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "failed to initialize session")
			logger.ErrorContext(ctx, "session.initialize.fail", slog.String("err", err.Error()))
			return
		}
		resp, err := jsonrpc.NewResultResponse(req.ID, initRes)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "failed to encode initialize response")
			logger.ErrorContext(ctx, "session.initialize.encode.fail", slog.String("err", err.Error()))
			return
		}
		w.Header().Set(mcpSessionIDHeader, sess.SessionID())
		if v := initRes.ProtocolVersion; v != "" {
			w.Header().Set(mcpProtocolVersionHeader, v)
		}
		w.Header().Set("Content-Type", jsonMediaType.String())
		w.WriteHeader(http.StatusOK)
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			logger.ErrorContext(ctx, "session.initialize.write.fail", slog.String("err", err.Error()))
		}
		logger.InfoContext(ctx, "session.initialize.ok", slog.Duration("dur", time.Since(start)))
		return
	}
	ctx = logctx.WithSessionID(ctx, sessID)
	logger = logctx.Enrich(ctx, logger)

	// Validate session via host metadata
	meta, err := h.sessionHost.GetSession(ctx, sessID)
	if err != nil || meta == nil || meta.Revoked || meta.UserID != userInfo.UserID() {
		w.WriteHeader(http.StatusNotFound)
		logger.InfoContext(ctx, "session.load.miss")
		h.teardownSession(sessID)
		return
	}
	logger.InfoContext(ctx, "session.load.ok")

	if req := msg.AsRequest(); req != nil && req.Method == "initialize" {
		writeJSONError(w, http.StatusConflict, "session already initialized")
		logger.WarnContext(ctx, "session.initialize.redundant")
		return
	}
	clientPV := r.Header.Get(mcpProtocolVersionHeader)
	sessionPV := meta.ProtocolVersion
	if clientPV != "" && sessionPV != "" && clientPV != sessionPV {
		writeJSONError(w, http.StatusBadRequest, "protocol version mismatch")
		logger.WarnContext(ctx, "protocol.version.mismatch", slog.String("session_version", sessionPV), slog.String("client_version", clientPV))
		return
	}

	if req := msg.AsRequest(); req != nil {
		if req.ID.IsNil() {
			if err := h.eng.HandleNotification(ctx, sessID, userInfo.UserID(), req); err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				logger.ErrorContext(ctx, "notification.inbound.fail", slog.String("method", req.Method), slog.String("err", err.Error()))
				return
			}
			if spv := meta.ProtocolVersion; spv != "" {
				w.Header().Set(mcpProtocolVersionHeader, spv)
			}
			w.WriteHeader(http.StatusAccepted)
			logger.InfoContext(ctx, "notification.inbound.ok", slog.String("method", req.Method), slog.Duration("dur", time.Since(start)))
			return
		}

		acc := r.Header.Get("Accept")
		if acc != "" {
			if _, _, err := contenttype.GetAcceptableMediaType(r, eventStreamMediaTypes); err != nil {
				w.WriteHeader(http.StatusUnsupportedMediaType)
				logger.WarnContext(ctx, "accept.unsupported", slog.String("accept", acc))
				return
			}
		}
		if spv := meta.ProtocolVersion; spv != "" {
			w.Header().Set(mcpProtocolVersionHeader, spv)
		}
		w.Header().Set("Content-Type", eventStreamMediaType.String())
		w.WriteHeader(http.StatusOK)
		wf.Flush()

		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()

		rid := req.ID.String()
		ctx = mcpservice.WithProgressReporter(ctx, streamingProgressReporter{mw: wf, requestID: rid})

		writer := engine.NewMessageWriterFunc(func(dwCtx context.Context, msg jsonrpc.Message) error {
			if err := writeSSEEvent(wf, "", msg); err != nil {
				if _, pubErr := h.sessionHost.PublishSession(dwCtx, sessID, msg); pubErr != nil {
					return fmt.Errorf("direct write failed: %v; fallback publish failed: %v", err, pubErr)
				}
			}
			return nil
		})

		res, err := h.eng.HandleRequest(ctx, sessID, userInfo.UserID(), req, writer)
		if err != nil {
			logger.ErrorContext(ctx, "rpc.inbound.fail", slog.String("method", req.Method), slog.String("err", err.Error()))
			res = &jsonrpc.Response{JSONRPCVersion: jsonrpc.ProtocolVersion, Error: &jsonrpc.Error{Code: jsonrpc.ErrorCodeInternalError, Message: "internal server error"}, ID: req.ID}
		}
		b, mErr := json.Marshal(res)
		if mErr != nil {
			logger.ErrorContext(ctx, "rpc.response.marshal.fail", slog.String("err", mErr.Error()))
			return
		}
		if err := writeSSEEvent(wf, "", b); err != nil {
			logger.ErrorContext(ctx, "sse.write.fail", slog.String("err", err.Error()))
			return
		}
		logger.InfoContext(ctx, "rpc.inbound.ok", slog.String("method", req.Method), slog.Duration("dur", time.Since(start)))
		return
	}

	if res := msg.AsResponse(); res != nil {
		// Forward client responses into Engine rendezvous via the shared fanout topic
		payload, err := json.Marshal(res)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			logger.ErrorContext(ctx, "response.marshal.fail", slog.String("err", err.Error()))
			return
		}
		envelope, err := json.Marshal(struct {
			SessionID string `json:"sess_id"`
			UserID    string `json:"user_id"`
			Msg       []byte `json:"msg"`
		}{SessionID: sessID, UserID: userInfo.UserID(), Msg: payload})
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			logger.ErrorContext(ctx, "response.envelope.fail", slog.String("err", err.Error()))
			return
		}
		if err := h.sessionHost.PublishEvent(ctx, sessionFanoutTopic, envelope); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			logger.ErrorContext(ctx, "response.publish.fail", slog.String("err", err.Error()))
			return
		}
		if spv := meta.ProtocolVersion; spv != "" {
			w.Header().Set(mcpProtocolVersionHeader, spv)
		}
		w.WriteHeader(http.StatusAccepted)
		logger.InfoContext(ctx, "response.inbound.ok", slog.Duration("dur", time.Since(start)))
		return
	}
	logger.WarnContext(ctx, "jsonrpc.message.unrecognized", slog.Duration("dur", time.Since(start)))
}

// handleGetMCP handles the GET /mcp endpoint, which is used to consume messages
// from an established session.
func (h *StreamingHTTPHandler) handleGetMCP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	baseCtx := r.Context()
	reqID := logctx.NewRequestID()
	ctx := logctx.WithRequestID(baseCtx, reqID)
	r = r.WithContext(ctx)
	logger := logctx.Enrich(ctx, h.log).With(
		slog.String("transport", "streaminghttp"),
		slog.String("op", "get_stream"),
		slog.String("http_method", r.Method),
		slog.String("path", r.URL.Path),
	)

	_, _, err := contenttype.GetAcceptableMediaType(r, eventStreamMediaTypes)
	if err != nil {
		w.WriteHeader(http.StatusUnsupportedMediaType)
		logger.WarnContext(ctx, "http.get.unsupported_media_type")
		return
	}

	f, ok := w.(http.Flusher)
	if !ok {
		w.WriteHeader(http.StatusInternalServerError)
		logger.ErrorContext(ctx, "sse.flusher.missing")
		return
	}

	ctx = r.Context()
	wf := &lockedWriteFlusher{Writer: w, Flusher: f, ctx: ctx}

	userInfo := h.checkAuthentication(ctx, r, w)
	if userInfo == nil {
		logger.InfoContext(ctx, "auth.fail")
		return
	}
	ctx = logctx.WithUserID(ctx, userInfo.UserID())
	logger = logctx.Enrich(ctx, logger)
	logger.InfoContext(ctx, "auth.ok")

	sessionHeader := r.Header.Get(mcpSessionIDHeader)
	if sessionHeader == "" {
		w.WriteHeader(http.StatusBadRequest)
		logger.WarnContext(ctx, "session.id.missing")
		return
	}
	ctx = logctx.WithSessionID(ctx, sessionHeader)
	logger = logctx.Enrich(ctx, logger)

	// Validate session via host metadata
	meta, err := h.sessionHost.GetSession(ctx, sessionHeader)
	if err != nil || meta == nil || meta.Revoked || meta.UserID != userInfo.UserID() {
		w.WriteHeader(http.StatusNotFound)
		logger.InfoContext(ctx, "session.load.miss")
		h.teardownSession(sessionHeader)
		return
	}
	logger.InfoContext(ctx, "session.load.ok")

	sessionID := meta.SessionID

	if pv := r.Header.Get(mcpProtocolVersionHeader); pv != "" {
		if spv := meta.ProtocolVersion; spv != "" && pv != spv {
			w.WriteHeader(http.StatusPreconditionFailed)
			logger.WarnContext(ctx, "protocol.version.mismatch", slog.String("session_version", spv), slog.String("client_version", pv))
			return
		}
	}

	lastEventID := r.Header.Get(lastEventIDHeader)

	// Bridge server-internal events to the session SSE stream for legacy topics.
	const resourcesListChangedTopic = string(mcp.ResourcesListChangedNotificationMethod)
	if err := h.sessionHost.SubscribeEvents(ctx, resourcesListChangedTopic, func(evtCtx context.Context, payload []byte) error {
		n := &jsonrpc.Request{JSONRPCVersion: jsonrpc.ProtocolVersion, Method: string(mcp.ResourcesListChangedNotificationMethod)}
		b, err := json.Marshal(n)
		if err != nil {
			return err
		}
		_, err = h.sessionHost.PublishSession(evtCtx, sessionID, b)
		return err
	}); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		logger.ErrorContext(ctx, "subscribe.resources_list_changed.fail", slog.String("err", err.Error()))
		return
	}

	const resourcesUpdatedTopic = string(mcp.ResourcesUpdatedNotificationMethod)
	if err := h.sessionHost.SubscribeEvents(ctx, resourcesUpdatedTopic, func(evtCtx context.Context, payload []byte) error {
		n := &jsonrpc.Request{JSONRPCVersion: jsonrpc.ProtocolVersion, Method: string(mcp.ResourcesUpdatedNotificationMethod), Params: payload}
		b, err := json.Marshal(n)
		if err != nil {
			return err
		}
		_, err = h.sessionHost.PublishSession(evtCtx, sessionID, b)
		return err
	}); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		logger.ErrorContext(ctx, "subscribe.resources_updated.fail", slog.String("err", err.Error()))
		return
	}

	const toolsListChangedTopic = string(mcp.ToolsListChangedNotificationMethod)
	if err := h.sessionHost.SubscribeEvents(ctx, toolsListChangedTopic, func(evtCtx context.Context, payload []byte) error {
		n := &jsonrpc.Request{JSONRPCVersion: jsonrpc.ProtocolVersion, Method: string(mcp.ToolsListChangedNotificationMethod)}
		b, err := json.Marshal(n)
		if err != nil {
			return err
		}
		_, err = h.sessionHost.PublishSession(evtCtx, sessionID, b)
		return err
	}); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		logger.ErrorContext(ctx, "subscribe.tools_list_changed.fail", slog.String("err", err.Error()))
		return
	}

	const promptsListChangedTopic = string(mcp.PromptsListChangedNotificationMethod)
	if err := h.sessionHost.SubscribeEvents(ctx, promptsListChangedTopic, func(evtCtx context.Context, payload []byte) error {
		n := &jsonrpc.Request{JSONRPCVersion: jsonrpc.ProtocolVersion, Method: string(mcp.PromptsListChangedNotificationMethod)}
		b, err := json.Marshal(n)
		if err != nil {
			return err
		}
		_, err = h.sessionHost.PublishSession(evtCtx, sessionID, b)
		return err
	}); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		logger.ErrorContext(ctx, "subscribe.prompts_list_changed.fail", slog.String("err", err.Error()))
		return
	}

	_ = h.sessionHost.PublishEvent(ctx, "streaminghttp/ready", nil)

	if spv := meta.ProtocolVersion; spv != "" {
		w.Header().Set(mcpProtocolVersionHeader, spv)
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	wf.Flush()
	logger.InfoContext(ctx, "sse.stream.start")

	if err := h.sessionHost.SubscribeSession(ctx, sessionID, lastEventID, func(cbCtx context.Context, msgID string, bytes []byte) error {
		if err := writeSSEEvent(wf, msgID, bytes); err != nil {
			logger.ErrorContext(cbCtx, "sse.write.fail", slog.String("err", err.Error()))
			return fmt.Errorf("failed to write session message to http response: %w", err)
		}
		logger.InfoContext(cbCtx, "sse.message.deliver")
		return nil
	}); err != nil {
		logger.ErrorContext(ctx, "subscribe.session.fail", slog.String("err", err.Error()))
		return
	}

	logger.InfoContext(ctx, "sse.stream.end", slog.Duration("dur", time.Since(start)))
}

// handleGetProtectedResourceMetadata serves the OAuth2 Protected Resource Metadata
// document crafted when the StreamingHTTPHandler initialized.
func (h *StreamingHTTPHandler) handleGetProtectedResourceMetadata(w http.ResponseWriter, r *http.Request) {
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
	// CORS: allow cross-origin browser fetches of the well-known metadata
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Vary", "Origin")
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(h.authServerMetadata); err != nil {
		http.Error(w, fmt.Sprintf("failed to encode authorization server metadata: %v", err), http.StatusInternalServerError)
		return
	}
}

// handleOptionsAuthorizationServerMetadata responds to CORS preflight requests
// for the authorization server metadata endpoint.
func (h *StreamingHTTPHandler) handleOptionsAuthorizationServerMetadata(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Accept, Authorization")
	w.Header().Set("Access-Control-Max-Age", "600")
	w.WriteHeader(http.StatusNoContent)
}

// (legacy handleResponse removed)

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

// (sessionWithWriter wrapper removed; superseded by SessionHandle.SetDirectWriter)

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

// streamingProgressReporter emits notifications/progress for a given request over the session stream.
type streamingProgressReporter struct {
	mw        io.Writer
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
	_, err = p.mw.Write(msg)
	return err
}
