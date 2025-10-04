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

	"github.com/elnormous/contenttype"
	"github.com/ggoodman/mcp-server-go/auth"
	"github.com/ggoodman/mcp-server-go/internal/engine"
	"github.com/ggoodman/mcp-server-go/internal/jsonrpc"
	"github.com/ggoodman/mcp-server-go/internal/logctx"
	"github.com/ggoodman/mcp-server-go/internal/wellknown"
	"github.com/ggoodman/mcp-server-go/mcp"
	"github.com/ggoodman/mcp-server-go/mcpservice"
	"github.com/ggoodman/mcp-server-go/sessions"
	"github.com/google/uuid"
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

type newConfig struct {
	serverName     string
	logger         *slog.Logger
	securityConfig *auth.SecurityConfig
	realm          string
}

// WithServerName sets a human-readable server name surfaced in PRM.
func WithServerName(name string) Option {
	return func(c *newConfig) { c.serverName = name }
}

// WithLogger sets the slog handler used by the server. If not provided, logs are discarded.
func WithLogger(h *slog.Logger) Option {
	return func(c *newConfig) { c.logger = h }
}

// WithSecurityConfig provides a unified security configuration for both
// advertisement and (if the authenticator supports it) consistency checks.
func WithSecurityConfig(sc auth.SecurityConfig) Option {
	return func(c *newConfig) { cfgCopy := sc.Copy(); c.securityConfig = &cfgCopy }
}

// WithRealm sets the HTTP authentication realm advertised in WWW-Authenticate
// challenges. If empty (default), the realm attribute is omitted entirely per
// RFC 6750 (it is optional) keeping challenges concise. Provide a short stable
// token (e.g. "mcp") if you want clients to bucket credentials across multiple
// handlers.
func WithRealm(realm string) Option {
	return func(c *newConfig) { c.realm = strings.TrimSpace(realm) }
}

// buildBearerChallenge builds a standardized Bearer challenge header value.
// Format:
//
//	Bearer realm="<realm>", error="...", error_description="..."
//
// Realm is omitted if empty. Params map order is stable only for tests if a
// deterministic container is used; since Go map iteration is randomized, we
// build a slice in key order we care about explicitly.
func buildBearerChallenge(realm string, resourceMetadata string, params map[string]string) string {
	pieces := make([]string, 0, 1+len(params))
	esc := func(v string) string { return strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(v) }
	if realm != "" {
		pieces = append(pieces, fmt.Sprintf(`realm="%s"`, esc(realm)))
	}
	if resourceMetadata != "" {
		pieces = append(pieces, fmt.Sprintf(`resource_metadata="%s"`, esc(resourceMetadata)))
	}
	// Preserve a logical ordering: error, error_description, scope (if later added), others alphabetical.
	if params != nil {
		if v, ok := params["error"]; ok {
			pieces = append(pieces, fmt.Sprintf(`error="%s"`, esc(v)))
		}
		if v, ok := params["error_description"]; ok {
			pieces = append(pieces, fmt.Sprintf(`error_description="%s"`, esc(v)))
		}
		if v, ok := params["scope"]; ok {
			pieces = append(pieces, fmt.Sprintf(`scope="%s"`, esc(v)))
		}
		// Add any remaining keys deterministically (stable order not critical for current use, best-effort alphabetical)
		for k, v := range params {
			if k == "error" || k == "error_description" || k == "scope" {
				continue
			}
			pieces = append(pieces, fmt.Sprintf(`%s="%s"`, k, esc(v)))
		}
	}
	if len(pieces) == 0 {
		return "Bearer"
	}
	return "Bearer " + strings.Join(pieces, ", ")
}

// pathIfSet returns the string form of u if non-nil, else empty.
func pathIfSet(u *url.URL) string {
	if u == nil {
		return ""
	}
	return u.String()
}

// StreamingHTTPHandler implements the stream HTTP transport protocol of
// the Model Context Protocol.
type StreamingHTTPHandler struct {
	mux                   *http.ServeMux
	log                   *slog.Logger
	prmDocument           wellknown.ProtectedResourceMetadata
	prmDocumentURL        *url.URL
	serverURL             *url.URL
	authServerMetadata    wellknown.AuthServerMetadata
	authServerMetadataURL *url.URL

	auth        auth.Authenticator
	mcp         mcpservice.ServerCapabilities
	eng         *engine.Engine
	sessionHost sessions.SessionHost
	realm       string
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
//   - authenticator: auth.Authenticator implementation (may also implement auth.SecurityDescriptor)
//
// Authentication configuration resolution order:
//  1. Explicit WithSecurityConfig option (highest precedence)
//  2. authenticator implements auth.SecurityDescriptor (inferred)
//
// If neither produces a security config but an authenticator is supplied, the
// handler will operate without advertising well-known security metadata. If
// no authenticator and no security config are provided New returns an error.
func New(ctx context.Context, publicEndpoint string, host sessions.SessionHost, server mcpservice.ServerCapabilities, authenticator auth.Authenticator, opts ...Option) (*StreamingHTTPHandler, error) {
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

	cfg := &newConfig{logger: slog.Default()}
	for _, opt := range opts {
		opt(cfg)
	}

	var resolved *auth.SecurityConfig
	if cfg.securityConfig != nil {
		cc := cfg.securityConfig.Copy()
		resolved = &cc
	}
	if resolved == nil && authenticator != nil {
		if sd, ok := authenticator.(auth.SecurityDescriptor); ok {
			cc := sd.SecurityConfig().Copy()
			resolved = &cc
		}
	}
	if resolved == nil && authenticator == nil {
		return nil, fmt.Errorf("either authenticator or WithSecurityConfig required")
	}

	loggerWithContextHandler := slog.New(logctx.Handler{Handler: cfg.logger.Handler()})

	h := &StreamingHTTPHandler{log: loggerWithContextHandler, serverURL: mcpURL, auth: authenticator, mcp: server, sessionHost: host, realm: cfg.realm}

	h.eng = engine.NewEngine(host, server, engine.WithLogger(h.log))
	go func() {
		if err := h.eng.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
			h.log.Error("engine.run.fail", slog.String("err", err.Error()))
		}
	}()

	if resolved != nil && resolved.Advertise {
		issuer := resolved.Issuer
		jwks := resolved.JWKSURL
		var scopes []string
		var svcDoc, pol, tos string
		var authzEP, tokenEP, regEP string
		var respTypes []string
		var grantTypes, responseModes, codeChal, tokenAuthMethods, tokenAuthAlgs []string
		if resolved.OIDC != nil {
			scopes = resolved.OIDC.ScopesSupported
			svcDoc = resolved.OIDC.ServiceDocumentation
			pol = resolved.OIDC.OpPolicyURI
			tos = resolved.OIDC.OpTosURI
			authzEP = resolved.OIDC.AuthorizationEndpoint
			tokenEP = resolved.OIDC.TokenEndpoint
			regEP = resolved.OIDC.RegistrationEndpoint
			respTypes = resolved.OIDC.ResponseTypesSupported
			grantTypes = resolved.OIDC.GrantTypesSupported
			responseModes = resolved.OIDC.ResponseModesSupported
			codeChal = resolved.OIDC.CodeChallengeMethodsSupported
			tokenAuthMethods = resolved.OIDC.TokenEndpointAuthMethodsSupported
			tokenAuthAlgs = resolved.OIDC.TokenEndpointAuthSigningAlgValuesSupported
		}
		// respTypes intentionally left empty if not provided by discovery; strict discovery
		// validation ensures they are present when using discovery-based auth.
		h.prmDocument = wellknown.ProtectedResourceMetadata{Resource: mcpURL.String(), AuthorizationServers: []string{issuer}, JwksURI: jwks, ScopesSupported: scopes, BearerMethodsSupported: []string{"authorization_header"}, ResourceName: cfg.serverName, ResourceDocumentation: svcDoc, ResourcePolicyURI: pol, ResourceTosURI: tos, TLSClientCertificateBoundAccessTokens: false, AuthorizationDetailsTypesSupported: []string{"urn:ietf:params:oauth:authorization-details"}}
		h.authServerMetadata = wellknown.AuthServerMetadata{Issuer: issuer, ResponseTypesSupported: respTypes, AuthorizationEndpoint: authzEP, TokenEndpoint: tokenEP, RegistrationEndpoint: regEP, JwksURI: jwks, ScopesSupported: scopes, ServiceDocumentation: svcDoc, OpPolicyURI: pol, OpTosURI: tos, GrantTypesSupported: grantTypes, ResponseModesSupported: responseModes, CodeChallengeMethodsSupported: codeChal, TokenEndpointAuthMethodsSupported: tokenAuthMethods, TokenEndpointAuthSigningAlgValuesSupported: tokenAuthAlgs}
	}

	h.prmDocumentURL = &url.URL{Scheme: mcpURL.Scheme, Host: mcpURL.Host, Path: fmt.Sprintf("/.well-known/oauth-protected-resource%s", mcpURL.Path)}
	h.authServerMetadataURL = &url.URL{Scheme: mcpURL.Scheme, Host: mcpURL.Host, Path: "/.well-known/oauth-authorization-server"}

	mux := http.NewServeMux()
	mux.HandleFunc(fmt.Sprintf("POST %s", pathOnly(mcpURL)), h.handlePostMCP)
	mux.HandleFunc(fmt.Sprintf("GET %s", pathOnly(mcpURL)), h.handleGetMCP)
	mux.HandleFunc(fmt.Sprintf("DELETE %s", pathOnly(mcpURL)), h.handleDeleteMCP)
	prmPath := pathOnly(h.prmDocumentURL)
	// If MCP is at root (prmPath ends with "/oauth-protected-resource/") also serve no-slash to avoid ServeMux 301.
	if strings.HasSuffix(prmPath, "/") {
		base := strings.TrimSuffix(prmPath, "/")
		mux.HandleFunc(fmt.Sprintf("GET %s", base), h.handleGetProtectedResourceMetadata)
		mux.HandleFunc(fmt.Sprintf("OPTIONS %s", base), h.handleOptionsProtectedResourceMetadata)
		mux.HandleFunc(fmt.Sprintf("GET %s/", base), h.handleGetProtectedResourceMetadata)
		mux.HandleFunc(fmt.Sprintf("OPTIONS %s/", base), h.handleOptionsProtectedResourceMetadata)
	} else {
		mux.HandleFunc(fmt.Sprintf("GET %s", prmPath), h.handleGetProtectedResourceMetadata)
		mux.HandleFunc(fmt.Sprintf("OPTIONS %s", prmPath), h.handleOptionsProtectedResourceMetadata)
		mux.HandleFunc(fmt.Sprintf("GET %s/", prmPath), h.handleGetProtectedResourceMetadata)
		mux.HandleFunc(fmt.Sprintf("OPTIONS %s/", prmPath), h.handleOptionsProtectedResourceMetadata)
	}
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
	h.mux.ServeHTTP(w, r.WithContext(logctx.WithRequestData(r.Context(), &logctx.RequestData{
		RequestID:  uuid.NewString(),
		Method:     r.Method,
		UserAgent:  r.UserAgent(),
		RemoteAddr: r.RemoteAddr,
		Path:       r.URL.Path,
	})))
}

// handleDeleteMCP handles the DELETE /mcp endpoint, which terminates an existing
// session. Authentication is required. On success, both persistent host-side
// resources and any process-local ephemeral resources associated with the
// session are cleaned up.
func (h *StreamingHTTPHandler) handleDeleteMCP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	ctx := r.Context()
	h.log.InfoContext(ctx, "http.delete.start")

	userInfo := h.checkAuthentication(ctx, r, w)
	if userInfo == nil {
		h.log.InfoContext(ctx, "auth.fail")
		return
	}
	h.log.InfoContext(ctx, "auth.ok")

	sessID := r.Header.Get(mcpSessionIDHeader)
	if sessID == "" {
		h.log.WarnContext(ctx, "delete.missing_session_id")
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	sess, err := h.eng.LoadSession(ctx, sessID, userInfo.UserID(), nil)
	if err != nil {
		if errors.Is(err, sessions.ErrSessionNotFound) {
			h.log.InfoContext(ctx, "session.delete.miss")
			w.WriteHeader(http.StatusNotFound)
			return
		}

		ctx = logctx.WithSessionData(ctx, &logctx.SessionData{
			SessionID: sessID,
			UserID:    userInfo.UserID(),
		})

		h.log.ErrorContext(ctx, "session.load.fail", slog.String("err", err.Error()))
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	ctx = logctx.WithSessionData(ctx, &logctx.SessionData{
		SessionID:       sess.SessionID(),
		UserID:          userInfo.UserID(),
		ProtocolVersion: sess.ProtocolVersion(),
		State:           sess.State(),
	})

	pvHeader := r.Header.Get(mcpProtocolVersionHeader)

	if pvHeader != "" && sess.ProtocolVersion() != "" && pvHeader != sess.ProtocolVersion() {
		h.log.WarnContext(ctx, "protocol.version.mismatch", slog.String("client_version", pvHeader))
		w.WriteHeader(http.StatusPreconditionFailed)
		return
	}

	if err := h.eng.DeleteSession(ctx, sess); err != nil {
		if errors.Is(err, sessions.ErrSessionNotFound) {
			h.log.InfoContext(ctx, "session.delete.miss")
			w.WriteHeader(http.StatusNotFound)
			return
		}
		h.log.ErrorContext(ctx, "session.delete.fail", slog.String("err", err.Error()))
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	// If we captured a protocol version, advertise it
	if sess.ProtocolVersion() != "" {
		w.Header().Set(mcpProtocolVersionHeader, sess.ProtocolVersion())
	}

	// Success: no content.
	w.WriteHeader(http.StatusNoContent)
	h.log.InfoContext(ctx, "http.delete.ok", slog.Duration("dur", time.Since(start)))
}

// handlePostMCP handles the POST /mcp endpoint, which is used by the client to send
// MCP messages to the server and to establish a session.
func (h *StreamingHTTPHandler) handlePostMCP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	ctx := logctx.WithRequestData(r.Context(), &logctx.RequestData{})
	h.log.InfoContext(ctx, "http.post.start")

	ctype, err := contenttype.GetMediaType(r)
	if err != nil || !ctype.Matches(jsonMediaType) {
		writeJSONError(w, http.StatusUnsupportedMediaType, "content-type must be application/json")
		h.log.WarnContext(ctx, "content_type.unsupported")
		return
	}

	f, ok := w.(http.Flusher)
	if !ok {
		w.WriteHeader(http.StatusInternalServerError)
		h.log.ErrorContext(ctx, "flusher.missing")
		return
	}

	ctx = r.Context()
	wf := &lockedWriteFlusher{Writer: w, Flusher: f, ctx: ctx}

	userInfo := h.checkAuthentication(ctx, r, w)
	if userInfo == nil {
		h.log.InfoContext(ctx, "auth.fail")
		return
	}

	var raw json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid JSON body")
		h.log.WarnContext(ctx, "json.decode.fail", slog.String("err", err.Error()))
		return
	}
	if len(raw) > 0 && raw[0] == '[' {
		writeJSONError(w, http.StatusBadRequest, "JSON-RPC batch arrays are forbidden on streaming HTTP transport")
		h.log.WarnContext(ctx, "jsonrpc.batch.forbidden")
		return
	}

	var msg jsonrpc.AnyMessage
	if err := json.Unmarshal(raw, &msg); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid JSON-RPC message: "+err.Error())
		h.log.WarnContext(ctx, "jsonrpc.message.invalid", slog.String("err", err.Error()))
		return
	}

	ctx = logctx.WithRPCMessage(ctx, &logctx.RPCMessage{
		Method: msg.Method,
		ID:     msg.ID.String(),
		Type:   msg.Type(),
	})

	sessID := r.Header.Get(mcpSessionIDHeader)
	if sessID == "" {
		// Session initialization via Engine
		req := msg.AsRequest()
		if req == nil || req.Method != string(mcp.InitializeMethod) {
			writeJSONError(w, http.StatusNotFound, "expected initialize request")
			h.log.InfoContext(ctx, "session.initialize.invalid")
			return
		}
		var initReq mcp.InitializeRequest
		if err := json.Unmarshal(req.Params, &initReq); err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid initialize params")
			h.log.InfoContext(ctx, "session.initialize.params.fail", slog.String("err", err.Error()))
			return
		}
		sess, initRes, err := h.eng.InitializeSession(ctx, userInfo.UserID(), &initReq)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "failed to initialize session")
			h.log.ErrorContext(ctx, "session.initialize.fail", slog.String("err", err.Error()))
			return
		}

		ctx = logctx.WithSessionData(ctx, &logctx.SessionData{SessionID: sess.SessionID(), UserID: userInfo.UserID()})

		resp, err := jsonrpc.NewResultResponse(req.ID, initRes)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "failed to encode initialize response")
			h.log.ErrorContext(ctx, "session.initialize.encode.fail", slog.String("err", err.Error()))
			return
		}
		w.Header().Set(mcpSessionIDHeader, sess.SessionID())
		if v := initRes.ProtocolVersion; v != "" {
			w.Header().Set(mcpProtocolVersionHeader, v)
		}
		w.Header().Set("Content-Type", jsonMediaType.String())
		w.WriteHeader(http.StatusOK)
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			h.log.ErrorContext(ctx, "session.initialize.write.fail", slog.String("err", err.Error()))
		}
		h.log.InfoContext(ctx, "session.initialize.ok", slog.Duration("dur", time.Since(start)))
		return
	}

	writer := engine.NewMessageWriterFunc(func(dwCtx context.Context, msg jsonrpc.Message) error {
		// We intentionally use an empty message ID here because we're not sending it through the session's
		// message queue. As a result, if the message is lost after flushing it to the OS but before the
		// client actually processes it, the message is permanently lost.
		//
		// This is a trade-off we make to support some unfortunate wording in the MCP spec (2.1, bullet 6):
		// https://modelcontextprotocol.io/specification/2025-06-18/basic/transports#sending-messages-to-the-server
		if err := writeSSEEvent(wf, "", msg); err != nil {
			if _, pubErr := h.eng.PublishToSession(dwCtx, sessID, userInfo.UserID(), msg); pubErr != nil {
				return fmt.Errorf("direct write failed: %v; fallback publish failed: %v", err, pubErr)
			}
		}
		return nil
	})

	sess, err := h.eng.LoadSession(ctx, sessID, userInfo.UserID(), writer)
	if err != nil {
		if errors.Is(err, sessions.ErrSessionNotFound) {
			writeJSONError(w, http.StatusNotFound, "session not found")
			h.log.InfoContext(ctx, "session.load.miss")
			return
		}
		writeJSONError(w, http.StatusInternalServerError, "failed to load session")
		h.log.ErrorContext(ctx, "session.load.fail", slog.String("err", err.Error()))
		return
	}

	ctx = logctx.WithSessionData(ctx, &logctx.SessionData{
		SessionID:       sess.SessionID(),
		UserID:          sess.UserID(),
		ProtocolVersion: sess.ProtocolVersion(),
		State:           sess.State(),
	})

	h.log.InfoContext(ctx, "session.load.ok")

	if req := msg.AsRequest(); req != nil && req.Method == "initialize" {
		writeJSONError(w, http.StatusConflict, "session already initialized")
		h.log.WarnContext(ctx, "session.initialize.redundant")
		return
	}
	clientPV := r.Header.Get(mcpProtocolVersionHeader)
	if clientPV != "" && sess.ProtocolVersion() != "" && clientPV != sess.ProtocolVersion() {
		writeJSONError(w, http.StatusBadRequest, "protocol version mismatch")
		h.log.WarnContext(ctx, "protocol.version.mismatch", slog.String("client_version", clientPV))
		return
	}

	if req := msg.AsRequest(); req != nil {
		if req.ID.IsNil() {
			if err := h.eng.HandleNotification(ctx, sess, req); err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				h.log.ErrorContext(ctx, "notification.inbound.fail", slog.String("err", err.Error()))
				return
			}
			if spv := sess.ProtocolVersion(); spv != "" {
				w.Header().Set(mcpProtocolVersionHeader, spv)
			}
			w.WriteHeader(http.StatusAccepted)
			h.log.InfoContext(ctx, "notification.inbound.ok", slog.Duration("dur", time.Since(start)))
			return
		}

		acc := r.Header.Get("Accept")
		if acc != "" {
			if _, _, err := contenttype.GetAcceptableMediaType(r, eventStreamMediaTypes); err != nil {
				w.WriteHeader(http.StatusUnsupportedMediaType)
				h.log.WarnContext(ctx, "accept.unsupported", slog.String("accept", acc))
				return
			}
		}
		if spv := sess.ProtocolVersion(); spv != "" {
			w.Header().Set(mcpProtocolVersionHeader, spv)
		}
		w.Header().Set("Content-Type", eventStreamMediaType.String())
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no")
		w.WriteHeader(http.StatusOK)
		wf.Flush()

		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()

		rid := req.ID.String()
		ctx = mcpservice.WithProgressReporter(ctx, streamingProgressReporter{mw: wf, requestID: rid})

		res, err := h.eng.HandleRequest(ctx, sess, req)
		if err != nil {
			h.log.ErrorContext(ctx, "rpc.inbound.fail", slog.String("err", err.Error()))
			res = &jsonrpc.Response{JSONRPCVersion: jsonrpc.ProtocolVersion, Error: &jsonrpc.Error{Code: jsonrpc.ErrorCodeInternalError, Message: "internal server error"}, ID: req.ID}
		}

		b, mErr := json.Marshal(res)
		if mErr != nil {
			h.log.ErrorContext(ctx, "rpc.response.marshal.fail", slog.String("err", mErr.Error()))
			return
		}
		if err := writer.WriteMessage(ctx, b); err != nil {
			h.log.ErrorContext(ctx, "sse.write.fail", slog.String("err", err.Error()))
			return
		}
		h.log.InfoContext(ctx, "rpc.inbound.ok", slog.Duration("dur", time.Since(start)))
		return
	}

	if res := msg.AsResponse(); res != nil {
		if err := h.eng.HandleClientResponse(ctx, sess, res); err != nil {
			if errors.Is(err, sessions.ErrSessionNotFound) {
				w.WriteHeader(http.StatusNotFound)
			} else {
				w.WriteHeader(http.StatusInternalServerError)
			}
			h.log.ErrorContext(ctx, "response.forward.fail", slog.String("err", err.Error()))
			return
		}
		if spv := sess.ProtocolVersion(); spv != "" {
			w.Header().Set(mcpProtocolVersionHeader, spv)
		}
		w.WriteHeader(http.StatusAccepted)
		h.log.InfoContext(ctx, "response.inbound.ok", slog.Duration("dur", time.Since(start)))
		return
	}

	h.log.WarnContext(ctx, "jsonrpc.message.unrecognized", slog.Duration("dur", time.Since(start)))
}

// handleGetMCP handles the GET /mcp endpoint, which is used to consume messages
// from an established session.
func (h *StreamingHTTPHandler) handleGetMCP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	ctx := r.Context()

	_, _, err := contenttype.GetAcceptableMediaType(r, eventStreamMediaTypes)
	if err != nil {
		w.WriteHeader(http.StatusUnsupportedMediaType)
		h.log.WarnContext(ctx, "http.get.unsupported_media_type")
		return
	}

	f, ok := w.(http.Flusher)
	if !ok {
		w.WriteHeader(http.StatusInternalServerError)
		h.log.ErrorContext(ctx, "sse.flusher.missing")
		return
	}

	ctx = r.Context()
	wf := &lockedWriteFlusher{Writer: w, Flusher: f, ctx: ctx}

	userInfo := h.checkAuthentication(ctx, r, w)
	if userInfo == nil {
		h.log.InfoContext(ctx, "auth.fail")
		return
	}
	h.log.InfoContext(ctx, "auth.ok")

	sessionHeader := r.Header.Get(mcpSessionIDHeader)
	if sessionHeader == "" {
		w.WriteHeader(http.StatusBadRequest)
		h.log.WarnContext(ctx, "session.id.missing")
		return
	}

	sess, err := h.eng.LoadSession(ctx, sessionHeader, userInfo.UserID(), nil)
	if err != nil {
		if errors.Is(err, sessions.ErrSessionNotFound) {
			w.WriteHeader(http.StatusNotFound)
			h.log.InfoContext(ctx, "session.load.miss")
			return
		}

		ctx := logctx.WithSessionData(ctx, &logctx.SessionData{SessionID: sessionHeader, UserID: userInfo.UserID()})

		w.WriteHeader(http.StatusInternalServerError)
		h.log.ErrorContext(ctx, "session.load.fail", slog.String("err", err.Error()))
		return
	}

	if pv := r.Header.Get(mcpProtocolVersionHeader); pv != "" {
		if spv := sess.ProtocolVersion(); spv != "" && pv != spv {
			w.WriteHeader(http.StatusPreconditionFailed)
			h.log.WarnContext(ctx, "protocol.version.mismatch", slog.String("client_version", pv))
			return
		}
	}

	lastEventID := r.Header.Get(lastEventIDHeader)

	if spv := sess.ProtocolVersion(); spv != "" {
		w.Header().Set(mcpProtocolVersionHeader, spv)
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	wf.Flush()

	h.log.InfoContext(ctx, "sse.stream.start")

	if err := h.eng.StreamSession(ctx, sess, lastEventID, func(cbCtx context.Context, msgID string, bytes []byte) error {
		if err := writeSSEEvent(wf, msgID, bytes); err != nil {
			h.log.ErrorContext(cbCtx, "sse.write.fail", slog.String("err", err.Error()))
			return err
		}
		h.log.InfoContext(cbCtx, "sse.message.deliver")
		return nil
	}); err != nil {
		if errors.Is(err, context.Canceled) {
			h.log.InfoContext(ctx, "subscribe.session.done")
		} else {
			h.log.ErrorContext(ctx, "subscribe.session.fail", slog.String("err", err.Error()))
		}
		return
	}

	h.log.InfoContext(ctx, "sse.stream.end", slog.Duration("dur", time.Since(start)))
}

func (h *StreamingHTTPHandler) handleOptionsProtectedResourceMetadata(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Accept, Authorization")
	w.Header().Set("Access-Control-Max-Age", "600")
	w.WriteHeader(http.StatusNoContent)
}

// handleGetProtectedResourceMetadata serves the OAuth2 Protected Resource Metadata document.
func (h *StreamingHTTPHandler) handleGetProtectedResourceMetadata(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Vary", "Origin")
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(h.prmDocument); err != nil {
		http.Error(w, fmt.Sprintf("failed to encode protected resource metadata: %v", err), http.StatusInternalServerError)
		return
	}
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

func (h *StreamingHTTPHandler) checkAuthentication(ctx context.Context, r *http.Request, w http.ResponseWriter) auth.UserInfo {
	authHeader := r.Header.Get(authorizationHeader)

	if authHeader == "" {
		// RFC 6750 §3.1: If the request lacks any authentication information the
		// resource server SHOULD NOT include an error code. Provide only a bare
		// Bearer challenge with realm.
		h.log.InfoContext(ctx, "auth.check.missing", slog.String("err", "no authorization header"))
		w.Header().Add(wwwAuthenticateHeader, buildBearerChallenge(h.realm, pathIfSet(h.prmDocumentURL), nil))
		w.WriteHeader(http.StatusUnauthorized)
		return nil
	}

	// Malformed header or wrong scheme -> invalid_request 400 per RFC 6750 §3.1.
	const bearerPrefix = "Bearer "
	if !strings.HasPrefix(authHeader, bearerPrefix) || len(authHeader) <= len(bearerPrefix) {
		h.log.InfoContext(ctx, "auth.check.invalid", slog.String("err", "malformed bearer authorization header"))
		w.Header().Add(wwwAuthenticateHeader, buildBearerChallenge(h.realm, pathIfSet(h.prmDocumentURL), map[string]string{"error": "invalid_request", "error_description": "malformed bearer authorization header"}))
		w.WriteHeader(http.StatusBadRequest)
		return nil
	}
	tok := strings.TrimSpace(authHeader[len(bearerPrefix):])
	if tok == "" {
		h.log.InfoContext(ctx, "auth.check.invalid", slog.String("err", "empty bearer token"))
		w.Header().Add(wwwAuthenticateHeader, buildBearerChallenge(h.realm, pathIfSet(h.prmDocumentURL), map[string]string{"error": "invalid_request", "error_description": "empty bearer token"}))
		w.WriteHeader(http.StatusBadRequest)
		return nil
	}

	userInfo, err := h.auth.CheckAuthentication(ctx, tok)
	if err != nil {
		if errors.Is(err, auth.ErrUnauthorized) {
			// Authentication attempted but token invalid -> 401 invalid_token
			h.log.InfoContext(ctx, "auth.check.fail", slog.String("err", err.Error()))
			w.Header().Add(wwwAuthenticateHeader, buildBearerChallenge(h.realm, pathIfSet(h.prmDocumentURL), map[string]string{"error": "invalid_token", "error_description": err.Error()}))
			w.WriteHeader(http.StatusUnauthorized)
			return nil
		}

		if errors.Is(err, auth.ErrInsufficientScope) {
			// Auth succeeded but insufficient privileges -> 403 insufficient_scope
			h.log.InfoContext(ctx, "auth.check.fail", slog.String("err", err.Error()))
			// Optionally we could append scope="..." when we know required scopes.
			w.Header().Add(wwwAuthenticateHeader, buildBearerChallenge(h.realm, pathIfSet(h.prmDocumentURL), map[string]string{"error": "insufficient_scope", "error_description": err.Error()}))
			w.WriteHeader(http.StatusForbidden)
			return nil
		}

		h.log.InfoContext(ctx, "auth.check.err", slog.String("err", err.Error()))
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
