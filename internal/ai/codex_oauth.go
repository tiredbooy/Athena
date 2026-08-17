package ai

import (
	"bytes"
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

	"github.com/tiredbooy/internal/appdirs"
)

const (
	codexOAuthClientID = "app_EMoamEEZ73f0CkXaXp7hrann"
	codexOAuthIssuer   = "https://auth.openai.com"
	codexRedirectURI   = "http://localhost:1455/auth/callback"
	codexDeviceURL     = codexOAuthIssuer + "/codex/device"
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
	http          *http.Client
	mu            sync.Mutex
	credentials   CodexCredentials
	pendingImport bool
	openBrowser   func(string) error
}

func LoadCodexOAuth() (*CodexOAuth, error) {
	o := &CodexOAuth{http: newOAuthHTTPClient(), openBrowser: openBrowser}
	path, err := appdirs.PrepareConfigFile("openai-codex-auth.json")
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return o, o.offerCLIImport()
	}
	if err != nil {
		return nil, fmt.Errorf("read OpenAI subscription credentials: %w", err)
	}
	if err := json.Unmarshal(raw, &o.credentials); err != nil {
		return nil, fmt.Errorf("parse OpenAI subscription credentials: %w", err)
	}
	return o, nil
}

// offerCLIImport decides what Athena may do with the OpenAI Codex CLI's tokens
// when Athena has none of its own. Those tokens belong to another application,
// so silence is not consent: without a recorded answer they are never used and
// the import is only *offered*, for a caller with a UI to resolve.
func (o *CodexOAuth) offerCLIImport() error {
	approved, decided, err := loadCodexImportDecision()
	if err != nil {
		return err
	}
	if decided && !approved {
		return nil
	}
	credentials, cliErr := loadCodexCLICredentials()
	if cliErr != nil {
		// The Codex CLI is absent or signed out: nothing to offer, and an
		// earlier approval simply has nothing to import today.
		return nil
	}
	if approved {
		o.credentials = credentials
		return nil
	}
	o.pendingImport = true
	return nil
}

// PendingCLIImport reports that the Codex CLI has credentials Athena could
// adopt but nobody has answered yet. Until ResolveCLIImport is called those
// tokens are not used, so a caller that never asks is safe by default.
func (o *CodexOAuth) PendingCLIImport() bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.pendingImport
}

// ResolveCLIImport records the user's answer and, on approval, adopts the Codex
// CLI credentials as Athena's own. The answer is persisted so the question is
// asked once: a decline must survive a restart.
func (o *CodexOAuth) ResolveCLIImport(approve bool) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if err := saveCodexImportDecision(approve); err != nil {
		return err
	}
	o.pendingImport = false
	if !approve {
		return nil
	}
	credentials, err := loadCodexCLICredentials()
	if err != nil {
		return err
	}
	return o.save(credentials)
}

// The decision lives in its own 0600 config file rather than inside the
// credential file so that it survives every rewrite of the tokens — signing in,
// refreshing, or signing out must not make Athena forget a "no".
func codexImportDecisionPath() (string, error) {
	return appdirs.ConfigFile("openai-codex-import.json")
}

// loadCodexImportDecision reports the persisted answer; decided is false when
// the question has never been asked.
func loadCodexImportDecision() (approved, decided bool, err error) {
	path, err := codexImportDecisionPath()
	if err != nil {
		return false, false, err
	}
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return false, false, nil
	}
	if err != nil {
		return false, false, fmt.Errorf("read Codex import decision: %w", err)
	}
	var decision struct {
		Approved bool `json:"approved"`
	}
	if err := json.Unmarshal(raw, &decision); err != nil {
		return false, false, fmt.Errorf("parse Codex import decision: %w", err)
	}
	return decision.Approved, true, nil
}

func saveCodexImportDecision(approved bool) error {
	path, err := codexImportDecisionPath()
	if err != nil {
		return err
	}
	if err := writeOwnerOnlyJSON(path, struct {
		Approved bool `json:"approved"`
	}{Approved: approved}); err != nil {
		return fmt.Errorf("save Codex import decision: %w", err)
	}
	return nil
}

func loadCodexCLICredentials() (CodexCredentials, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return CodexCredentials{}, err
	}
	raw, err := os.ReadFile(filepath.Join(home, ".codex", "auth.json"))
	if err != nil {
		return CodexCredentials{}, fmt.Errorf("read Codex login credentials: %w", err)
	}
	var auth struct {
		Tokens struct {
			AccessToken  string `json:"access_token"`
			RefreshToken string `json:"refresh_token"`
			IDToken      string `json:"id_token"`
			AccountID    string `json:"account_id"`
		} `json:"tokens"`
	}
	if err := json.Unmarshal(raw, &auth); err != nil {
		return CodexCredentials{}, fmt.Errorf("parse Codex login credentials: %w", err)
	}
	if auth.Tokens.AccessToken == "" || auth.Tokens.RefreshToken == "" {
		return CodexCredentials{}, fmt.Errorf("Codex is not signed in with a ChatGPT account")
	}
	account := auth.Tokens.AccountID
	if account == "" {
		account = accountID(auth.Tokens.IDToken)
	}
	expires := jwtExpiry(auth.Tokens.AccessToken)
	if expires == 0 {
		expires = time.Now().Add(5 * time.Minute).Unix()
	}
	return CodexCredentials{AccessToken: auth.Tokens.AccessToken, RefreshToken: auth.Tokens.RefreshToken, ExpiresAt: expires, AccountID: account}, nil
}

func jwtExpiry(token string) int64 {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return 0
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return 0
	}
	var claims struct {
		ExpiresAt int64 `json:"exp"`
	}
	if json.Unmarshal(raw, &claims) != nil {
		return 0
	}
	return claims.ExpiresAt
}

// RunDeviceLogin uses OpenAI's Codex device authorization flow directly. It
// works locally and over SSH without requiring a separately installed Codex
// binary: Athena shows the URL/code, opens a browser when possible, then waits
// for OpenAI to confirm the user's approval.
func (o *CodexOAuth) RunDeviceLogin(ctx context.Context, onLine func(string)) error {
	body, _ := json.Marshal(map[string]string{"client_id": codexOAuthClientID})
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, codexOAuthIssuer+"/api/accounts/deviceauth/usercode", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build OpenAI device login request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "athena/1")
	response, err := o.http.Do(request)
	if err != nil {
		return fmt.Errorf("start OpenAI device login: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return codexOAuthStatusError("device login", response)
	}
	var device struct {
		DeviceAuthID string       `json:"device_auth_id"`
		UserCode     string       `json:"user_code"`
		Interval     oauthSeconds `json:"interval"`
	}
	if err := json.NewDecoder(response.Body).Decode(&device); err != nil {
		return fmt.Errorf("decode OpenAI device login: %w", err)
	}
	if device.DeviceAuthID == "" || device.UserCode == "" {
		return fmt.Errorf("OpenAI device login returned no authorization code")
	}
	if onLine != nil {
		onLine(fmt.Sprintf("Open %s and enter code: %s", codexDeviceURL, device.UserCode))
	}
	if o.openBrowser != nil {
		_ = o.openBrowser(codexDeviceURL)
	}
	intervalSeconds := int64(device.Interval)
	if intervalSeconds < 1 {
		intervalSeconds = 5
	}
	interval := time.Duration(intervalSeconds)*time.Second + 3*time.Second
	deadline := time.Now().Add(10 * time.Minute)
	attempts := 0
	for time.Now().Before(deadline) {
		credentials, pending, err := o.pollDeviceLogin(ctx, device.DeviceAuthID, device.UserCode)
		if err != nil {
			return err
		}
		if !pending {
			o.mu.Lock()
			defer o.mu.Unlock()
			return o.save(credentials)
		}
		attempts++
		if attempts%3 == 0 && onLine != nil {
			onLine("Still waiting for OpenAI approval…")
		}
		if err := waitForContext(ctx, minDuration(interval, time.Until(deadline))); err != nil {
			return err
		}
	}
	return fmt.Errorf("OpenAI device authorization timed out; start /connect again")
}

func (o *CodexOAuth) pollDeviceLogin(ctx context.Context, deviceAuthID, userCode string) (CodexCredentials, bool, error) {
	body, _ := json.Marshal(map[string]string{"device_auth_id": deviceAuthID, "user_code": userCode})
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, codexOAuthIssuer+"/api/accounts/deviceauth/token", bytes.NewReader(body))
	if err != nil {
		return CodexCredentials{}, false, fmt.Errorf("build OpenAI device approval request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "athena/1")
	response, err := o.http.Do(request)
	if err != nil {
		return CodexCredentials{}, false, fmt.Errorf("poll OpenAI device approval: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusForbidden || response.StatusCode == http.StatusNotFound {
		return CodexCredentials{}, true, nil
	}
	if response.StatusCode != http.StatusOK {
		return CodexCredentials{}, false, codexOAuthStatusError("device approval", response)
	}
	var approval struct {
		AuthorizationCode string `json:"authorization_code"`
		CodeVerifier      string `json:"code_verifier"`
	}
	if err := json.NewDecoder(response.Body).Decode(&approval); err != nil {
		return CodexCredentials{}, false, fmt.Errorf("decode OpenAI device approval: %w", err)
	}
	credentials, err := o.exchangeDeviceCode(ctx, approval.AuthorizationCode, approval.CodeVerifier)
	return credentials, false, err
}

func (o *CodexOAuth) exchangeDeviceCode(ctx context.Context, code, verifier string) (CodexCredentials, error) {
	if code == "" || verifier == "" {
		return CodexCredentials{}, fmt.Errorf("OpenAI device approval response was incomplete")
	}
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {codexOAuthIssuer + "/deviceauth/callback"},
		"client_id":     {codexOAuthClientID},
		"code_verifier": {verifier},
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, codexOAuthIssuer+"/oauth/token", strings.NewReader(form.Encode()))
	if err != nil {
		return CodexCredentials{}, fmt.Errorf("build OpenAI device token exchange: %w", err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := o.http.Do(request)
	if err != nil {
		return CodexCredentials{}, fmt.Errorf("exchange OpenAI device token: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return CodexCredentials{}, codexOAuthStatusError("device token exchange", response)
	}
	var token tokenResponse
	if err := json.NewDecoder(response.Body).Decode(&token); err != nil {
		return CodexCredentials{}, fmt.Errorf("decode OpenAI device token: %w", err)
	}
	if token.AccessToken == "" || token.RefreshToken == "" {
		return CodexCredentials{}, fmt.Errorf("OpenAI device token response was incomplete")
	}
	expiresIn := token.ExpiresIn
	if expiresIn <= 0 {
		expiresIn = 3600
	}
	return CodexCredentials{
		AccessToken: token.AccessToken, RefreshToken: token.RefreshToken,
		ExpiresAt: time.Now().Add(time.Duration(expiresIn) * time.Second).Unix(), AccountID: accountID(token.IDToken),
	}, nil
}

func codexOAuthStatusError(operation string, response *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(response.Body, 2048))
	detail := strings.TrimSpace(string(body))
	if detail == "" {
		return fmt.Errorf("OpenAI %s failed with status %d", operation, response.StatusCode)
	}
	return fmt.Errorf("OpenAI %s failed with status %d: %s", operation, response.StatusCode, detail)
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

// Connected reports whether a stored session exists, without touching the
// network. A stored refresh token can still have been revoked; only a real
// request proves otherwise. Callers use this to decide whether to offer the
// provider at all, not to skip error handling.
func (o *CodexOAuth) Connected() bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.credentials.RefreshToken != ""
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
	path, err := codexCredentialsPath()
	if err != nil {
		return err
	}
	if err := writeOwnerOnlyJSON(path, credentials); err != nil {
		return fmt.Errorf("save OpenAI subscription credentials: %w", err)
	}
	o.credentials = credentials
	return nil
}
func codexCredentialsPath() (string, error) {
	return appdirs.ConfigFile("openai-codex-auth.json")
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
