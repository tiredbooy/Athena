package ai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestXAIDeviceLoginReportsCodeAndPersistsTokens(t *testing.T) {
	var tokenRequests int
	client := &http.Client{Transport: roundTripper(func(request *http.Request) (*http.Response, error) {
		switch request.URL.Path {
		case "/device":
			if err := request.ParseForm(); err != nil {
				t.Fatal(err)
			}
			if request.Form.Get("client_id") != xaiOAuthClientID || !strings.Contains(request.Form.Get("scope"), "offline_access") {
				t.Fatalf("unexpected device form: %v", request.Form)
			}
			return jsonResponse(http.StatusOK, xaiDeviceCode{
				DeviceCode: "device", UserCode: "ABCD-1234", VerificationURI: "https://x.ai/device",
				VerificationURIComplete: "https://x.ai/device?user_code=ABCD-1234", ExpiresIn: 60, Interval: 1,
			}), nil
		case "/token":
			tokenRequests++
			if err := request.ParseForm(); err != nil {
				t.Fatal(err)
			}
			if request.Form.Get("grant_type") != xaiDeviceGrantType || request.Form.Get("device_code") != "device" {
				t.Fatalf("unexpected token form: %v", request.Form)
			}
			return jsonResponse(http.StatusOK, xaiTokenResponse{AccessToken: "access", RefreshToken: "refresh", ExpiresIn: 3600}), nil
		default:
			return jsonResponse(http.StatusNotFound, map[string]string{"error": "not_found"}), nil
		}
	})}

	path := filepath.Join(t.TempDir(), "xai-oauth.json")
	var opened string
	oauth := &XAIOAuth{
		http: client, tokenURL: "https://auth.test/token", deviceAuthorizationURL: "https://auth.test/device",
		credentialsPath: path, openBrowser: func(rawURL string) error { opened = rawURL; return nil },
	}
	var lines []string
	if err := oauth.RunDeviceLogin(context.Background(), func(line string) { lines = append(lines, line) }); err != nil {
		t.Fatal(err)
	}
	if tokenRequests != 1 || opened != "https://x.ai/device?user_code=ABCD-1234" {
		t.Fatalf("token requests = %d, opened = %q", tokenRequests, opened)
	}
	if len(lines) < 1 || !strings.Contains(lines[0], "ABCD-1234") {
		t.Fatalf("status lines = %#v", lines)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "ABCD-1234") || !strings.Contains(string(raw), `"refresh_token": "refresh"`) {
		t.Fatalf("unexpected credential file: %s", raw)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("credential permissions = %o", info.Mode().Perm())
	}
}

func TestXAIAccessTokenRefreshesRotatingCredentials(t *testing.T) {
	client := &http.Client{Transport: roundTripper(func(request *http.Request) (*http.Response, error) {
		if err := request.ParseForm(); err != nil {
			t.Fatal(err)
		}
		if request.Form.Get("grant_type") != "refresh_token" || request.Form.Get("refresh_token") != "old-refresh" {
			t.Fatalf("unexpected refresh form: %v", request.Form)
		}
		return jsonResponse(http.StatusOK, xaiTokenResponse{AccessToken: "new-access", RefreshToken: "new-refresh", ExpiresIn: 3600}), nil
	})}

	oauth := &XAIOAuth{
		http: client, tokenURL: "https://auth.test/token", credentialsPath: filepath.Join(t.TempDir(), "xai-oauth.json"),
		credentials: XAICredentials{AccessToken: "old-access", RefreshToken: "old-refresh", ExpiresAt: time.Now().Add(-time.Minute).Unix()},
	}
	token, err := oauth.AccessToken(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if token != "new-access" || oauth.credentials.RefreshToken != "new-refresh" {
		t.Fatalf("token = %q, credentials = %#v", token, oauth.credentials)
	}
}

func TestXAIDevicePollingHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	client := &http.Client{Transport: roundTripper(func(request *http.Request) (*http.Response, error) {
		cancel()
		return jsonResponse(http.StatusBadRequest, map[string]string{"error": "authorization_pending"}), nil
	})}
	oauth := &XAIOAuth{http: client, tokenURL: "https://auth.test/token"}
	_, err := oauth.pollDeviceToken(ctx, xaiDeviceCode{DeviceCode: "device", ExpiresIn: 60, Interval: 1}, nil)
	if err == nil || err != context.Canceled {
		t.Fatalf("error = %v", err)
	}
}

func jsonResponse(status int, value any) *http.Response {
	raw, _ := json.Marshal(value)
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(string(raw))), Header: make(http.Header)}
}
