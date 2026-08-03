package ai

import (
	"net/url"
	"testing"
)

func TestCodexAuthorizeURLMatchesPKCEFlow(t *testing.T) {
	raw := codexAuthorizeURL("challenge", "state")
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	if query.Get("client_id") != codexOAuthClientID || query.Get("redirect_uri") != codexRedirectURI {
		t.Fatalf("unexpected OAuth identity: %s", raw)
	}
	if query.Get("scope") != "openid profile email offline_access" {
		t.Fatalf("scope = %q", query.Get("scope"))
	}
	if query.Get("originator") != "athena" {
		t.Fatalf("originator = %q", query.Get("originator"))
	}
	if query.Get("code_challenge_method") != "S256" || query.Get("code_challenge") != "challenge" {
		t.Fatalf("missing PKCE parameters: %s", raw)
	}
}

func TestPKCEChallengeUsesURLSafeSHA256(t *testing.T) {
	verifier, challenge, err := pkce()
	if err != nil {
		t.Fatal(err)
	}
	if verifier == "" || challenge == "" || len(challenge) != 43 {
		t.Fatalf("invalid PKCE values: verifier=%q challenge=%q", verifier, challenge)
	}
}
