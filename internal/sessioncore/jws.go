package sessioncore

import (
	"crypto/ed25519"
	"fmt"

	jose "github.com/go-jose/go-jose/v4"
)

// JWSSignerVerifier provides minimal JWS operations needed by the session manager.
type JWSSignerVerifier interface {
	// Sign returns a compact JWS for the given payload using the active key.
	Sign(payload []byte) (string, error)
	// Verify parses and verifies a compact JWS and returns its payload and the kid used.
	Verify(token string) (payload []byte, kid string, err error)
}

// MemoryJWS implements JWSSignerVerifier using an in-memory set of Ed25519 keys
// with a designated active key for signing.
type MemoryJWS struct {
	activeKid string
	privKeys  map[string]ed25519.PrivateKey
	pubKeys   map[string]ed25519.PublicKey
}

func NewMemoryJWS() *MemoryJWS {
	return &MemoryJWS{
		privKeys: make(map[string]ed25519.PrivateKey),
		pubKeys:  make(map[string]ed25519.PublicKey),
	}
}

// AddEd25519Key registers a key pair under kid. The active key is unchanged.
func (m *MemoryJWS) AddEd25519Key(kid string, priv ed25519.PrivateKey) {
	m.privKeys[kid] = priv
	m.pubKeys[kid] = priv.Public().(ed25519.PublicKey)
}

// SetActive selects the key used for signing.
func (m *MemoryJWS) SetActive(kid string) error {
	if _, ok := m.privKeys[kid]; !ok {
		return fmt.Errorf("unknown kid: %s", kid)
	}
	m.activeKid = kid
	return nil
}

func (m *MemoryJWS) ActiveKID() string { return m.activeKid }

func (m *MemoryJWS) Sign(payload []byte) (string, error) {
	if m.activeKid == "" {
		return "", fmt.Errorf("no active kid configured")
	}
	priv, ok := m.privKeys[m.activeKid]
	if !ok {
		return "", fmt.Errorf("active kid not found: %s", m.activeKid)
	}
	opts := (&jose.SignerOptions{}).WithType("JWT").WithHeader("kid", m.activeKid)
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.EdDSA, Key: priv}, opts)
	if err != nil {
		return "", fmt.Errorf("failed to create signer: %w", err)
	}
	jws, err := signer.Sign(payload)
	if err != nil {
		return "", fmt.Errorf("failed to sign payload: %w", err)
	}
	compact, err := jws.CompactSerialize()
	if err != nil {
		return "", fmt.Errorf("failed to serialize jws: %w", err)
	}
	return compact, nil
}

func (m *MemoryJWS) Verify(token string) ([]byte, string, error) {
	jws, err := jose.ParseSigned(token, []jose.SignatureAlgorithm{jose.EdDSA})
	if err != nil {
		return nil, "", fmt.Errorf("failed to parse jws: %w", err)
	}
	if len(jws.Signatures) != 1 {
		return nil, "", fmt.Errorf("unexpected signatures: %d", len(jws.Signatures))
	}
	kid := jws.Signatures[0].Protected.KeyID
	pub, ok := m.pubKeys[kid]
	if !ok {
		return nil, kid, fmt.Errorf("unknown kid: %s", kid)
	}
	payload, err := jws.Verify(pub)
	if err != nil {
		return nil, kid, fmt.Errorf("signature verification failed: %w", err)
	}
	return payload, kid, nil
}
