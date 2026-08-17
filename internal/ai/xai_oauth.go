package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/tiredbooy/internal/appdirs"
)

const (
	xaiOAuthClientID            = "b1a00492-073a-47ea-816f-4c329264a828"
	xaiTokenURL                 = "https://auth.x.ai/oauth2/token"
	xaiDeviceAuthorizationURL   = "https://auth.x.ai/oauth2/device/code"
	xaiDeviceGrantType          = "urn:ietf:params:oauth:grant-type:device_code"
	xaiOAuthScope               = "openid profile email offline_access grok-cli:access api:access"
	xaiDefaultPollInterval      = 5 * time.Second
	xaiMinimumPollInterval      = time.Second
	xaiPollingSafetyMargin      = 3 * time.Second
	xaiSlowDownIncrement        = 5 * time.Second
	xaiDefaultDeviceCodeTimeout = 5 * time.Minute
	xaiRefreshSkew              = 2 * time.Minute
)

type XAICredentials struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresAt    int64  `json:"expires_at"`
}

type xaiTokenResponse struct {
	AccessToken  string       `json:"access_token"`
	RefreshToken string       `json:"refresh_token"`
	ExpiresIn    oauthSeconds `json:"expires_in"`
}

type xaiDeviceCode struct {
	DeviceCode              string       `json:"device_code"`
	UserCode                string       `json:"user_code"`
	VerificationURI         string       `json:"verification_uri"`
	VerificationURIComplete string       `json:"verification_uri_complete"`
	ExpiresIn               oauthSeconds `json:"expires_in"`
	Interval                oauthSeconds `json:"interval"`
}

// XAIOAuth owns the xAI device authorization and token-refresh lifecycle.
// The chat provider only asks it for a valid bearer token, which keeps OAuth
// policy out of the OpenAI-compatible transport adapter.
type XAIOAuth struct {
	mu                     sync.Mutex
	http                   *http.Client
	credentials            XAICredentials
	tokenURL               string
	deviceAuthorizationURL string
	openBrowser            func(string) error
	credentialsPath        string
}

func LoadXAIOAuth() (*XAIOAuth, error) {
	path, err := appdirs.PrepareConfigFile("xai-oauth.json")
	if err != nil {
		return nil, err
	}
	oauth := &XAIOAuth{
		http:                   newOAuthHTTPClient(),
		tokenURL:               xaiTokenURL,
		deviceAuthorizationURL: xaiDeviceAuthorizationURL,
		openBrowser:            openBrowser,
		credentialsPath:        path,
	}
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return oauth, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read xAI OAuth credentials: %w", err)
	}
	if err := json.Unmarshal(raw, &oauth.credentials); err != nil {
		return nil, fmt.Errorf("parse xAI OAuth credentials: %w", err)
	}
	return oauth, nil
}

// RunDeviceLogin opens xAI's verification page when possible, always reports
// the URL and short code to the UI, then polls until the user approves or the
// server-provided deadline expires.
func (o *XAIOAuth) RunDeviceLogin(ctx context.Context, onLine func(string)) error {
	device, err := o.requestDeviceCode(ctx)
	if err != nil {
		return err
	}
	browserURL := device.VerificationURIComplete
	if browserURL == "" {
		browserURL = device.VerificationURI
	}
	if onLine != nil {
		onLine(fmt.Sprintf("Open %s and enter code: %s", device.VerificationURI, device.UserCode))
		if browserURL != device.VerificationURI {
			onLine("Sign-in link with the code pre-filled: " + browserURL)
		}
	}
	if o.openBrowser != nil {
		_ = o.openBrowser(browserURL)
	}
	tokens, err := o.pollDeviceToken(ctx, device, onLine)
	if err != nil {
		return err
	}
	if strings.TrimSpace(tokens.AccessToken) == "" {
		return fmt.Errorf("xAI device login returned no access token")
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.saveLocked(o.credentialsFrom(tokens, ""))
}

func (o *XAIOAuth) requestDeviceCode(ctx context.Context) (xaiDeviceCode, error) {
	form := url.Values{"client_id": {xaiOAuthClientID}, "scope": {xaiOAuthScope}}
	request, err := o.formRequest(ctx, o.deviceAuthorizationURL, form)
	if err != nil {
		return xaiDeviceCode{}, fmt.Errorf("build xAI device login request: %w", err)
	}
	response, err := o.http.Do(request)
	if err != nil {
		return xaiDeviceCode{}, fmt.Errorf("start xAI device login: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return xaiDeviceCode{}, xaiOAuthStatusError("device login", response)
	}
	var device xaiDeviceCode
	if err := json.NewDecoder(response.Body).Decode(&device); err != nil {
		return xaiDeviceCode{}, fmt.Errorf("decode xAI device login: %w", err)
	}
	if device.DeviceCode == "" || device.UserCode == "" || device.VerificationURI == "" {
		return xaiDeviceCode{}, fmt.Errorf("xAI device login response is missing its code or verification URL")
	}
	return device, nil
}

func (o *XAIOAuth) pollDeviceToken(ctx context.Context, device xaiDeviceCode, onLine func(string)) (xaiTokenResponse, error) {
	interval := durationFromSeconds(int64(device.Interval), xaiDefaultPollInterval)
	if interval < xaiMinimumPollInterval {
		interval = xaiMinimumPollInterval
	}
	expiresIn := durationFromSeconds(int64(device.ExpiresIn), xaiDefaultDeviceCodeTimeout)
	deadline := time.Now().Add(expiresIn)
	attempts := 0
	for time.Now().Before(deadline) {
		form := url.Values{
			"grant_type":  {xaiDeviceGrantType},
			"client_id":   {xaiOAuthClientID},
			"device_code": {device.DeviceCode},
		}
		request, err := o.formRequest(ctx, o.tokenURL, form)
		if err != nil {
			return xaiTokenResponse{}, fmt.Errorf("build xAI device token request: %w", err)
		}
		response, err := o.http.Do(request)
		if err != nil {
			return xaiTokenResponse{}, fmt.Errorf("poll xAI device login: %w", err)
		}
		if response.StatusCode == http.StatusOK {
			var tokens xaiTokenResponse
			err := json.NewDecoder(response.Body).Decode(&tokens)
			response.Body.Close()
			if err != nil {
				return xaiTokenResponse{}, fmt.Errorf("decode xAI device token: %w", err)
			}
			return tokens, nil
		}
		var problem struct {
			Error       string `json:"error"`
			Description string `json:"error_description"`
		}
		_ = json.NewDecoder(io.LimitReader(response.Body, 4096)).Decode(&problem)
		response.Body.Close()
		switch problem.Error {
		case "authorization_pending":
		case "slow_down":
			interval += xaiSlowDownIncrement
		case "access_denied", "authorization_denied":
			return xaiTokenResponse{}, fmt.Errorf("xAI device authorization was denied")
		case "expired_token":
			return xaiTokenResponse{}, fmt.Errorf("xAI device code expired; start /connect again")
		default:
			detail := strings.TrimSpace(problem.Description)
			if detail == "" {
				detail = strings.TrimSpace(problem.Error)
			}
			return xaiTokenResponse{}, fmt.Errorf("xAI device token exchange failed (%d): %s", response.StatusCode, detail)
		}
		attempts++
		if attempts%3 == 0 && onLine != nil {
			onLine("Still waiting for xAI approval…")
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			break
		}
		if err := waitForContext(ctx, minDuration(interval+xaiPollingSafetyMargin, remaining)); err != nil {
			return xaiTokenResponse{}, err
		}
	}
	return xaiTokenResponse{}, fmt.Errorf("xAI device authorization timed out; start /connect again")
}

// Connected reports whether a stored session exists, without touching the
// network. See CodexOAuth.Connected for the same caveat about revocation.
func (o *XAIOAuth) Connected() bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.credentials.AccessToken != "" || o.credentials.RefreshToken != ""
}

func (o *XAIOAuth) AccessToken(ctx context.Context) (string, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.credentials.AccessToken == "" {
		return "", fmt.Errorf("xAI subscription is not signed in; run /connect")
	}
	if !xaiTokenExpiresSoon(o.credentials) {
		return o.credentials.AccessToken, nil
	}
	if o.credentials.RefreshToken == "" {
		return "", fmt.Errorf("xAI sign-in expired and cannot be refreshed; run /connect again")
	}
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {o.credentials.RefreshToken},
		"client_id":     {xaiOAuthClientID},
	}
	request, err := o.formRequest(ctx, o.tokenURL, form)
	if err != nil {
		return "", fmt.Errorf("build xAI token refresh: %w", err)
	}
	response, err := o.http.Do(request)
	if err != nil {
		return "", fmt.Errorf("refresh xAI sign-in: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", xaiOAuthStatusError("token refresh", response)
	}
	var tokens xaiTokenResponse
	if err := json.NewDecoder(response.Body).Decode(&tokens); err != nil {
		return "", fmt.Errorf("decode xAI token refresh: %w", err)
	}
	credentials := o.credentialsFrom(tokens, o.credentials.RefreshToken)
	if credentials.AccessToken == "" {
		return "", fmt.Errorf("xAI token refresh returned no access token")
	}
	if err := o.saveLocked(credentials); err != nil {
		return "", err
	}
	return credentials.AccessToken, nil
}

func (o *XAIOAuth) credentialsFrom(tokens xaiTokenResponse, fallbackRefresh string) XAICredentials {
	refresh := strings.TrimSpace(tokens.RefreshToken)
	if refresh == "" {
		refresh = fallbackRefresh
	}
	expiresIn := durationFromSeconds(int64(tokens.ExpiresIn), time.Hour)
	return XAICredentials{
		AccessToken:  strings.TrimSpace(tokens.AccessToken),
		RefreshToken: refresh,
		ExpiresAt:    time.Now().Add(expiresIn).Unix(),
	}
}

func (o *XAIOAuth) saveLocked(credentials XAICredentials) error {
	if err := writeOwnerOnlyJSON(o.credentialsPath, credentials); err != nil {
		return fmt.Errorf("save xAI OAuth credentials: %w", err)
	}
	o.credentials = credentials
	return nil
}

func (o *XAIOAuth) formRequest(ctx context.Context, endpoint string, form url.Values) (*http.Request, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "athena/1")
	return request, nil
}

func xaiTokenExpiresSoon(credentials XAICredentials) bool {
	expiresAt := credentials.ExpiresAt
	if jwt := jwtExpiry(credentials.AccessToken); jwt > 0 && (expiresAt == 0 || jwt < expiresAt) {
		expiresAt = jwt
	}
	return expiresAt == 0 || time.Unix(expiresAt, 0).Before(time.Now().Add(xaiRefreshSkew))
}

func xaiOAuthStatusError(operation string, response *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(response.Body, 2048))
	detail := strings.TrimSpace(string(body))
	if detail == "" {
		return fmt.Errorf("xAI %s failed with status %d", operation, response.StatusCode)
	}
	return fmt.Errorf("xAI %s failed with status %d: %s", operation, response.StatusCode, detail)
}

func durationFromSeconds(seconds int64, fallback time.Duration) time.Duration {
	const maximumOAuthDuration = 365 * 24 * time.Hour
	if seconds <= 0 || seconds > int64(maximumOAuthDuration/time.Second) {
		return fallback
	}
	return time.Duration(seconds) * time.Second
}

func minDuration(left, right time.Duration) time.Duration {
	if left < right {
		return left
	}
	return right
}

func waitForContext(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func xaiCredentialsPath() (string, error) {
	return appdirs.ConfigFile("xai-oauth.json")
}
