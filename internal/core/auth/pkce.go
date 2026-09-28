package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"net/url"
)

// PKCE is the proof key for a browser sign-in (RFC 7636), so a stolen authorisation code
// is useless.
//
// The Challenge goes in the link the person opens; the Verifier, kept here, goes with the
// code when it is redeemed, and only whoever made the challenge has it. State comes back
// with the code, proving the answer is to this sign-in and no other.
type PKCE struct {
	Verifier  string
	Challenge string
	State     string
}

// NewPKCE is a fresh verifier, its challenge and a state, for one sign-in.
func NewPKCE() PKCE {
	verifier := randomText(64)
	digest := sha256.Sum256([]byte(verifier))
	return PKCE{Verifier: verifier, Challenge: base64.RawURLEncoding.EncodeToString(digest[:]), State: randomText(24)}
}

// String shows the challenge only: the verifier and the state are the secrets.
func (p PKCE) String() string { return "PKCE{challenge: " + p.Challenge + "}" }

// GoString is String, so %#v hides the secrets too.
func (p PKCE) GoString() string { return p.String() }

// Parameters are what the authorisation link carries: the challenge, its method and the
// state.
func (p PKCE) Parameters() url.Values {
	return url.Values{"code_challenge": {p.Challenge}, "code_challenge_method": {"S256"}, "state": {p.State}}
}

// randomText is size random bytes, URL-safe, as Python's secrets.token_urlsafe gives.
func randomText(size int) string {
	data := make([]byte, size)
	// crypto/rand.Read never fails: it panics rather than hand back weak bytes.
	_, _ = rand.Read(data)
	return base64.RawURLEncoding.EncodeToString(data)
}
