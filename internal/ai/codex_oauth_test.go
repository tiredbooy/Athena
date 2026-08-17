package ai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
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

// fakeCodexCLIHome points HOME at a scratch directory holding a signed-in
// Codex CLI. The real ~/.codex is never read.
func fakeCodexCLIHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	// appdirs prefers XDG_CONFIG_HOME when it is absolute; clear it so the
	// decision and credential files land under the scratch HOME.
	t.Setenv("XDG_CONFIG_HOME", "")
	if err := os.MkdirAll(filepath.Join(home, ".codex"), 0o700); err != nil {
		t.Fatal(err)
	}
	auth := []byte(`{"tokens":{"access_token":"cli-access","refresh_token":"cli-refresh","account_id":"cli-account"}}`)
	if err := os.WriteFile(filepath.Join(home, ".codex", "auth.json"), auth, 0o600); err != nil {
		t.Fatal(err)
	}
	return home
}

func TestCodexCLIImportIsOfferedNotTaken(t *testing.T) {
	fakeCodexCLIHome(t)
	oauth, err := LoadCodexOAuth()
	if err != nil {
		t.Fatal(err)
	}
	if oauth.Connected() {
		t.Fatal("an unanswered import used the Codex CLI tokens")
	}
	if _, err := oauth.Credentials(context.Background()); err == nil {
		t.Fatal("an unanswered import handed out the Codex CLI tokens")
	}
	if !oauth.PendingCLIImport() {
		t.Fatal("expected the import to be offered")
	}
}

func TestCodexCLIImportDeclineSurvivesReload(t *testing.T) {
	fakeCodexCLIHome(t)
	oauth, err := LoadCodexOAuth()
	if err != nil {
		t.Fatal(err)
	}
	if err := oauth.ResolveCLIImport(false); err != nil {
		t.Fatal(err)
	}
	if oauth.PendingCLIImport() || oauth.Connected() {
		t.Fatal("a declined import was still pending or connected")
	}
	reloaded, err := LoadCodexOAuth()
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.PendingCLIImport() {
		t.Fatal("a declined import was asked again after a restart")
	}
	if reloaded.Connected() {
		t.Fatal("a declined import was used after a restart")
	}
}

func TestCodexCLIImportApprovalAdoptsCredentials(t *testing.T) {
	fakeCodexCLIHome(t)
	oauth, err := LoadCodexOAuth()
	if err != nil {
		t.Fatal(err)
	}
	if err := oauth.ResolveCLIImport(true); err != nil {
		t.Fatal(err)
	}
	if !oauth.Connected() || oauth.PendingCLIImport() {
		t.Fatal("an approved import did not connect")
	}
	reloaded, err := LoadCodexOAuth()
	if err != nil {
		t.Fatal(err)
	}
	credentials, err := reloaded.Credentials(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if credentials.AccessToken != "cli-access" || credentials.RefreshToken != "cli-refresh" {
		t.Fatalf("credentials = %#v", credentials)
	}
}

func TestCodexDeviceLoginCompletesWithoutExternalCLI(t *testing.T) {
	var opened string
	client := &http.Client{Transport: roundTripper(func(request *http.Request) (*http.Response, error) {
		switch request.URL.Path {
		case "/api/accounts/deviceauth/usercode":
			var body map[string]string
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body["client_id"] != codexOAuthClientID {
				t.Fatalf("client id = %q", body["client_id"])
			}
			return jsonResponse(http.StatusOK, map[string]string{"device_auth_id": "device", "user_code": "OPEN-AI", "interval": "1"}), nil
		case "/api/accounts/deviceauth/token":
			return jsonResponse(http.StatusOK, map[string]string{"authorization_code": "authorization", "code_verifier": "verifier"}), nil
		case "/oauth/token":
			if err := request.ParseForm(); err != nil {
				t.Fatal(err)
			}
			if request.Form.Get("redirect_uri") != codexOAuthIssuer+"/deviceauth/callback" || request.Form.Get("code_verifier") != "verifier" {
				t.Fatalf("exchange form = %v", request.Form)
			}
			return jsonResponse(http.StatusOK, tokenResponse{AccessToken: "access", RefreshToken: "refresh", ExpiresIn: 3600}), nil
		default:
			return jsonResponse(http.StatusNotFound, map[string]string{"error": "not_found"}), nil
		}
	})}
	home := t.TempDir()
	t.Setenv("HOME", home)
	// appdirs prefers an absolute XDG_CONFIG_HOME over $HOME, so isolating HOME
	// alone is not enough: RunDeviceLogin saves credentials, and on a machine
	// where XDG_CONFIG_HOME is set this test would overwrite the user's real
	// openai-codex-auth.json with these fake tokens.
	t.Setenv("XDG_CONFIG_HOME", "")
	oauth := &CodexOAuth{http: client, openBrowser: func(rawURL string) error { opened = rawURL; return nil }}
	var lines []string
	if err := oauth.RunDeviceLogin(context.Background(), func(line string) { lines = append(lines, line) }); err != nil {
		t.Fatal(err)
	}
	if opened != codexDeviceURL || len(lines) != 1 || !strings.Contains(lines[0], "OPEN-AI") {
		t.Fatalf("opened = %q, lines = %#v", opened, lines)
	}
	path := filepath.Join(home, ".config", "athena", "openai-codex-auth.json")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("credential permissions = %o", info.Mode().Perm())
	}
}
