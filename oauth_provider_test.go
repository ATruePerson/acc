package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestKimiDeviceFlowPollsPendingThenStoresIdentity(t *testing.T) {
	var polls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/oauth/device_authorization":
			json.NewEncoder(w).Encode(map[string]any{
				"user_code": "ABCD", "device_code": "device", "verification_uri": "https://kimi.test/activate", "expires_in": 60, "interval": 1,
			})
		case "/api/oauth/token":
			if polls.Add(1) == 1 {
				w.WriteHeader(http.StatusBadRequest)
				json.NewEncoder(w).Encode(map[string]any{"error": "authorization_pending"})
				return
			}
			json.NewEncoder(w).Encode(map[string]any{
				"access_token":  jwtForTest(map[string]any{"sub": "user-1", "email": "TRUE@EXAMPLE.COM"}),
				"refresh_token": "refresh-1", "expires_in": 3600,
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	driver := newKimiOAuthDriver(server.Client())
	driver.oauthHost = server.URL
	driver.sleep = func(context.Context, time.Duration) error { return nil }
	var authURL, userCode string
	credential, err := driver.Login(context.Background(), func(rawURL, code string) {
		authURL, userCode = rawURL, code
	})
	if err != nil {
		t.Fatal(err)
	}
	if authURL != "https://kimi.test/activate" || userCode != "ABCD" {
		t.Fatalf("authorization prompt = %q %q", authURL, userCode)
	}
	if polls.Load() != 2 || credential.Provider != "kimi" || credential.AccountID != "user-1" || credential.Email != "true@example.com" {
		t.Fatalf("Kimi credential = %+v, polls=%d", credential, polls.Load())
	}
}

func TestKimiDeviceFlowHandlesSlowDownAndDenial(t *testing.T) {
	var waits []time.Duration
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "device_authorization") {
			json.NewEncoder(w).Encode(map[string]any{
				"user_code": "ABCD", "device_code": "device", "verification_uri": "https://kimi.test", "expires_in": 60, "interval": 1,
			})
			return
		}
		w.WriteHeader(http.StatusBadRequest)
		if len(waits) == 0 {
			json.NewEncoder(w).Encode(map[string]any{"error": "slow_down", "interval": 3})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"error": "access_denied"})
	}))
	defer server.Close()
	driver := newKimiOAuthDriver(server.Client())
	driver.oauthHost = server.URL
	driver.sleep = func(_ context.Context, duration time.Duration) error {
		waits = append(waits, duration)
		return nil
	}
	if _, err := driver.Login(context.Background(), func(string, string) {}); !isPermanentOAuthError(err) {
		t.Fatalf("denial error = %v", err)
	}
	if len(waits) != 1 || waits[0] < 3*time.Second {
		t.Fatalf("slow_down wait = %v", waits)
	}
}

func TestKimiRefreshPreservesOldRefreshTokenWhenNotRotated(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"access_token": "new-access", "expires_in": 3600})
	}))
	defer server.Close()
	driver := newKimiOAuthDriver(server.Client())
	driver.oauthHost = server.URL
	credential, err := driver.Refresh(context.Background(), authCredential{Provider: "kimi", RefreshToken: "old-refresh"})
	if err != nil {
		t.Fatal(err)
	}
	if credential.AccessToken != "new-access" || credential.RefreshToken != "old-refresh" {
		t.Fatalf("refresh credential = %+v", credential)
	}
}

func TestValidateXAIEndpointRequiresHTTPSXAIHost(t *testing.T) {
	for _, rawURL := range []string{"http://auth.x.ai/token", "https://evil.example/token", "https://x.ai.evil.example/token"} {
		if _, err := validateXAIEndpoint(rawURL); err == nil {
			t.Fatalf("accepted unsafe xAI endpoint %q", rawURL)
		}
	}
	if got, err := validateXAIEndpoint("https://auth.x.ai/oauth/token"); err != nil || got == "" {
		t.Fatalf("rejected xAI endpoint: %q %v", got, err)
	}
}

func TestXAITokenRequestRetriesTemporaryFailuresOnly(t *testing.T) {
	for _, tc := range []struct {
		name      string
		statuses  []int
		wantCalls int
		wantErr   bool
	}{
		{"temporary", []int{http.StatusTooManyRequests, http.StatusBadGateway, http.StatusOK}, 3, false},
		{"permanent", []int{http.StatusBadRequest, http.StatusOK}, 1, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				index := int(calls.Add(1)) - 1
				status := tc.statuses[index]
				w.WriteHeader(status)
				if status == http.StatusOK {
					json.NewEncoder(w).Encode(map[string]any{"access_token": "access", "refresh_token": "refresh", "expires_in": 3600})
				} else {
					json.NewEncoder(w).Encode(map[string]any{"error": "temporarily_unavailable"})
				}
			}))
			defer server.Close()
			driver := newXAIOAuthDriver(server.Client())
			driver.sleep = func(context.Context, time.Duration) error { return nil }
			_, err := driver.postToken(context.Background(), server.URL, url.Values{"grant_type": {"refresh_token"}})
			if (err != nil) != tc.wantErr || int(calls.Load()) != tc.wantCalls {
				t.Fatalf("error=%v calls=%d", err, calls.Load())
			}
		})
	}
}

func TestParseClaudeCredentialIsReadOnlyAndRejectsChangedFormat(t *testing.T) {
	good := `{"claudeAiOauth":{"accessToken":"access","refreshToken":"refresh","expiresAt":12345}}`
	credential, err := parseClaudeCredential([]byte(good))
	if err != nil || credential.AccessToken != "access" || credential.Origin != "claude-code-import" {
		t.Fatalf("parsed credential = %+v, %v", credential, err)
	}
	if _, err := parseClaudeCredential([]byte(`{"unknown":true}`)); err == nil {
		t.Fatal("changed Claude credential format was accepted")
	}
}

func jwtForTest(payload map[string]any) string {
	b, _ := json.Marshal(payload)
	return "header." + base64RawURL(b) + ".signature"
}

func base64RawURL(value []byte) string {
	return strings.TrimRight(base64URLEncode(value), "=")
}

func base64URLEncode(value []byte) string {
	return base64.RawURLEncoding.EncodeToString(value)
}

func responseBody(value string) io.ReadCloser { return io.NopCloser(strings.NewReader(value)) }
