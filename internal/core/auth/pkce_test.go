package auth

import (
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"strings"
	"testing"
)

func TestPKCEChallengesItsVerifierAndHidesIt(t *testing.T) {
	proof := NewPKCE()
	digest := sha256.Sum256([]byte(proof.Verifier))
	if proof.Challenge != base64.RawURLEncoding.EncodeToString(digest[:]) || len(proof.Verifier) != 86 || len(proof.State) != 32 {
		t.Errorf("%d %d", len(proof.Verifier), len(proof.State))
	}
	if NewPKCE().State == proof.State {
		t.Error("two sign-ins share a state")
	}
	params := proof.Parameters()
	if params.Get("code_challenge") != proof.Challenge || params.Get("code_challenge_method") != "S256" || params.Get("state") != proof.State {
		t.Error(params)
	}
	for _, shown := range []string{fmt.Sprint(proof), fmt.Sprintf("%#v", proof), fmt.Sprintf("%+v", proof)} {
		if strings.Contains(shown, proof.Verifier) || strings.Contains(shown, proof.State) {
			t.Errorf("a secret was shown: %s", shown)
		}
	}
}
