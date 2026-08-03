package ai

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

const (
	codexOAuthClientID = "app_EMoamEEZ73f0CkXaXp7hrann"
	codexOAuthIssuer   = "https://auth.openai.com"
	codexRedirectURI   = "http://localhost:1455/auth/callback"
)

// CodexCredentials are OAuth tokens for the ChatGPT/Codex subscription flow.
// They are written only to a 0600 local file, never to the normal config YAML.
type CodexCredentials struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresAt    int64  `json:"expires_at"`
	AccountID    string `json:"account_id,omitempty"`
}

type CodexOAuth struct {
	http        *http.Client
	mu          sync.Mutex
	credentials CodexCredentials
	openBrowser func(string) error
}

func LoadCodexOAuth() (*CodexOAuth, error) {
	o := &CodexOAuth{http: &http.Client{Timeout: 30 * time.Second}, openBrowser: openBrowser}
	path, err := codexCredentialsPath()
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return o, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read OpenAI subscription credentials: %w", err)
	}
	if err := json.Unmarshal(raw, &o.credentials); err != nil {
		return nil, fmt.Errorf("parse OpenAI subscription credentials: %w", err)
	}
	return o, nil
}

// Start opens a localhost callback listener, launches the default browser when
// possible, and returns the authorization URL as a fallback for headless use.
// The returned channel delivers exactly one result.
func (o *CodexOAuth) Start(ctx context.Context) (string, <-chan error, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	// The OAuth redirect uses localhost, not 127.0.0.1. Binding to that same
	// hostname avoids a failed callback on systems where browsers prefer ::1.
	listener, err := net.Listen("tcp", "localhost:1455")
	if err != nil {
		return "", nil, fmt.Errorf("start OpenAI sign-in callback on port 1455: %w", err)
	}
	verifier, challenge, err := pkce()
	if err != nil {
		listener.Close()
		return "", nil, err
	}
	state, err := randomURLToken(32)
	if err != nil {
		listener.Close()
		return "", nil, err
	}
	result := make(chan error, 1)
	var once sync.Once
	var server *http.Server
	complete := func(err error) {
		once.Do(func() {
			result <- err
			go func() { _ = server.Close() }()
		})
	}
	server = &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/auth/callback" {
			http.NotFound(w, r)
			return
		}
		if r.URL.Query().Get("state") != state {
			http.Error(w, "Invalid sign-in state.", http.StatusBadRequest)
			complete(fmt.Errorf("OpenAI sign-in state did not match"))
			return
		}
		if problem := r.URL.Query().Get("error"); problem != "" {
			http.Error(w, "Sign-in was not approved.", http.StatusBadRequest)
			complete(fmt.Errorf("OpenAI sign-in failed: %s", problem))
			return
		}
		credentials, err := o.exchange(r.Context(), r.URL.Query().Get("code"), verifier)
		if err == nil {
			err = o.save(credentials)
		}
		if err != nil {
			http.Error(w, "Sign-in failed; return to Athena.", http.StatusInternalServerError)
		} else {
			_, _ = io.WriteString(w, "<html><body><h2>Athena is connected.</h2><p>You can return to your terminal.</p></body></html>")
		}
		complete(err)
	})}
	go func() { _ = server.Serve(listener) }()
	go func() { <-ctx.Done(); complete(ctx.Err()) }()
	authorizeURL := codexAuthorizeURL(challenge, state)
	if o.openBrowser != nil {
		// A browser can be unavailable in a headless session. The caller still
		// receives the URL and can complete the same flow from another browser.
		_ = o.openBrowser(authorizeURL)
	}
	return authorizeURL, result, nil
}

func openBrowser(rawURL string) error {
	var command *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		command = exec.Command("open", rawURL)
	case "windows":
		command = exec.Command("rundll32", "url.dll,FileProtocolHandler", rawURL)
	default:
		command = exec.Command("xdg-open", rawURL)
	}
	return command.Start()
}

func codexAuthorizeURL(challenge, state string) string {
	// Match the Codex PKCE flow while identifying Athena truthfully rather than
	// pretending to be another client such as OpenCode.
	params := url.Values{"response_type": {"code"}, "client_id": {codexOAuthClientID}, "redirect_uri": {codexRedirectURI}, "scope": {"openid profile email offline_access"}, "code_challenge": {challenge}, "code_challenge_method": {"S256"}, "id_token_add_organizations": {"true"}, "codex_cli_simplified_flow": {"true"}, "originator": {"athena"}, "state": {state}}
	return codexOAuthIssuer + "/oauth/authorize?" + params.Encode()
}

func (o *CodexOAuth) Credentials(ctx context.Context) (CodexCredentials, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.credentials.RefreshToken == "" {
		return CodexCredentials{}, fmt.Errorf("OpenAI subscription is not connected; run /connect")
	}
	if time.Now().Unix() < o.credentials.ExpiresAt-60 {
		return o.credentials, nil
	}
	form := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {o.credentials.RefreshToken}, "client_id": {codexOAuthClientID}}
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, codexOAuthIssuer+"/oauth/token", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := o.http.Do(req)
	if err != nil {
		return CodexCredentials{}, fmt.Errorf("refresh OpenAI subscription token: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return CodexCredentials{}, fmt.Errorf("refresh OpenAI subscription token: status %d", resp.StatusCode)
	}
	var token tokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&token); err != nil {
		return CodexCredentials{}, err
	}
	o.credentials.AccessToken, o.credentials.ExpiresAt = token.AccessToken, time.Now().Add(time.Duration(token.ExpiresIn)*time.Second).Unix()
	if token.RefreshToken != "" {
		o.credentials.RefreshToken = token.RefreshToken
	}
	if err := o.save(o.credentials); err != nil {
		return CodexCredentials{}, err
	}
	return o.credentials, nil
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	IDToken      string `json:"id_token"`
	ExpiresIn    int    `json:"expires_in"`
}

func (o *CodexOAuth) exchange(ctx context.Context, code, verifier string) (CodexCredentials, error) {
	if code == "" {
		return CodexCredentials{}, fmt.Errorf("OpenAI did not return an authorization code")
	}
	form := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {codexRedirectURI}, "client_id": {codexOAuthClientID}, "code_verifier": {verifier}}
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, codexOAuthIssuer+"/oauth/token", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := o.http.Do(req)
	if err != nil {
		return CodexCredentials{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return CodexCredentials{}, fmt.Errorf("exchange OpenAI authorization: status %d", resp.StatusCode)
	}
	var token tokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&token); err != nil {
		return CodexCredentials{}, err
	}
	return CodexCredentials{AccessToken: token.AccessToken, RefreshToken: token.RefreshToken, ExpiresAt: time.Now().Add(time.Duration(token.ExpiresIn) * time.Second).Unix(), AccountID: accountID(token.IDToken)}, nil
}
func (o *CodexOAuth) save(credentials CodexCredentials) error {
	o.credentials = credentials
	path, err := codexCredentialsPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	raw, err := json.Marshal(credentials)
	if err != nil {
		return err
	}
	return os.WriteFile(path, raw, 0o600)
}
func codexCredentialsPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "second-brain", "openai-codex-auth.json"), nil
}
func pkce() (string, string, error) {
	verifier, err := randomURLToken(48)
	if err != nil {
		return "", "", err
	}
	sum := sha256.Sum256([]byte(verifier))
	return verifier, base64.RawURLEncoding.EncodeToString(sum[:]), nil
}
func randomURLToken(bytes int) (string, error) {
	raw := make([]byte, bytes)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}
func accountID(token string) string {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return ""
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ""
	}
	var claims struct {
		Auth struct {
			AccountID string `json:"chatgpt_account_id"`
		} `json:"https://api.openai.com/auth"`
	}
	if json.Unmarshal(raw, &claims) != nil {
		return ""
	}
	return claims.Auth.AccountID
}
