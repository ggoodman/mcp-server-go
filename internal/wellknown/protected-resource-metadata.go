package wellknown

// ProtectedResourceMetadata represents OAuth 2.0 Protected Resource Metadata,
// as defined by RFC 9449 and drafts referenced therein. Field names and JSON
// tags follow the specification and common OIDC/OAuth conventions.
type ProtectedResourceMetadata struct {
	Resource                              string   `json:"resource"`
	AuthorizationServers                  []string `json:"authorization_servers,omitempty"`
	JwksURI                               string   `json:"jwks_uri,omitempty"`
	ScopesSupported                       []string `json:"scopes_supported,omitempty"`
	BearerMethodsSupported                []string `json:"bearer_methods_supported,omitempty"`
	ResourceSigningAlgValuesSupported     []string `json:"resource_signing_alg_values_supported,omitempty"`
	ResourceName                          string   `json:"resource_name,omitempty"`
	ResourceDocumentation                 string   `json:"resource_documentation,omitempty"`
	ResourcePolicyURI                     string   `json:"resource_policy_uri,omitempty"`
	ResourceTosURI                        string   `json:"resource_tos_uri,omitempty"`
	TLSClientCertificateBoundAccessTokens bool     `json:"tls_client_certificate_bound_access_tokens,omitempty"`
	AuthorizationDetailsTypesSupported    []string `json:"authorization_details_types_supported,omitempty"`
	DpopSigningAlgValuesSupported         []string `json:"dpop_signing_alg_values_supported,omitempty"`
	DpopBoundAccessTokensRequired         bool     `json:"dpop_bound_access_tokens_required,omitempty"`
	SignedMetadata                        string   `json:"signed_metadata,omitempty"`
}
