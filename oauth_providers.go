package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	// Public native-client IDs and endpoints are compatible with the matching
	// provider CLI flows documented in THIRD_PARTY_NOTICES.md. They are not
	// secrets. ACC never embeds or requests a private OAuth client secret.
	kimiOAuthClientID = "17e5f671-d194-4dfb-9706-5516cb48c098"
	xaiOAuthClientID  = "b1a00492-073a-47ea-816f-4c329264a828"
)

type sleepContextFunc func(context.Context, time.Duration) error

func sleepContext(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

type kimiOAuthDriver struct {
	client    *http.Client
	oauthHost string
	clientID  string
	now       func() time.Time
	sleep     sleepContextFunc
}

func newKimiOAuthDriver(client *http.Client) *kimiOAuthDriver {
	if client == nil {
		client = oauthHTTPClient()
	}
	return &kimiOAuthDriver{
		client: client, oauthHost: "https://auth.kimi.com", clientID: kimiOAuthClientID,
		now: time.Now, sleep: sleepContext,
	}
}

type kimiDeviceAuthorization struct {
	UserCode                string `json:"user_code"`
	DeviceCode              string `json:"device_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	ExpiresIn               int    `json:"expires_in"`
	Interval                int    `json:"interval"`
}

type oauthTokenPayload struct {
	AccessToken      string `json:"access_token"`
	RefreshToken     string `json:"refresh_token"`
	ExpiresIn        int    `json:"expires_in"`
	IDToken          string `json:"id_token"`
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
	Interval         int    `json:"interval"`
	Account          *struct {
		UUID  string `json:"uuid"`
		Email string `json:"email_address"`
	} `json:"account"`
}

func (d *kimiOAuthDriver) Login(ctx context.Context, onAuthorization func(rawURL, userCode string)) (authCredential, error) {
	device, err := d.requestDeviceAuthorization(ctx)
	if err != nil {
		return authCredential{}, err
	}
	verificationURL := device.VerificationURIComplete
	if verificationURL == "" {
		verificationURL = device.VerificationURI
	}
	if onAuthorization != nil {
		onAuthorization(verificationURL, device.UserCode)
	}
	interval := time.Duration(device.Interval) * time.Second
	if interval <= 0 {
		interval = 5 * time.Second
	}
	expires := time.Duration(device.ExpiresIn) * time.Second
	if expires <= 0 {
		expires = 15 * time.Minute
	}
	deadline := d.now().Add(expires)
	wait := interval
	for d.now().Before(deadline) {
		payload, status, err := d.tokenRequest(ctx, url.Values{
			"client_id":   {d.clientID},
			"device_code": {device.DeviceCode},
			"grant_type":  {"urn:ietf:params:oauth:grant-type:device_code"},
		})
		if err != nil {
			return authCredential{}, err
		}
		if status >= 200 && status < 300 && payload.AccessToken != "" {
			return kimiCredentialFromPayload(payload, "", d.now()), nil
		}
		switch payload.Error {
		case "authorization_pending":
			if err := d.sleep(ctx, wait); err != nil {
				return authCredential{}, err
			}
		case "slow_down":
			wait += 5 * time.Second
			if providerWait := time.Duration(payload.Interval) * time.Second; providerWait > wait {
				wait = providerWait
			}
			if err := d.sleep(ctx, wait); err != nil {
				return authCredential{}, err
			}
		case "access_denied", "expired_token", "invalid_grant":
			return authCredential{}, &oauthError{Status: status, Code: payload.Error, Description: payload.ErrorDescription}
		default:
			return authCredential{}, &oauthError{Status: status, Code: payload.Error, Description: payload.ErrorDescription}
		}
	}
	return authCredential{}, context.DeadlineExceeded
}

func (d *kimiOAuthDriver) requestDeviceAuthorization(ctx context.Context) (kimiDeviceAuthorization, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(d.oauthHost, "/")+"/api/oauth/device_authorization", strings.NewReader(url.Values{"client_id": {d.clientID}}.Encode()))
	if err != nil {
		return kimiDeviceAuthorization{}, err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "ACC OAuth client")
	response, err := d.client.Do(request)
	if err != nil {
		return kimiDeviceAuthorization{}, err
	}
	defer response.Body.Close()
	var payload kimiDeviceAuthorization
	if err := readBoundedJSON(response, &payload); err != nil {
		return kimiDeviceAuthorization{}, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return kimiDeviceAuthorization{}, fmt.Errorf("Kimi device authorization HTTP %d", response.StatusCode)
	}
	if payload.UserCode == "" || payload.DeviceCode == "" || payload.VerificationURI == "" {
		return kimiDeviceAuthorization{}, fmt.Errorf("Kimi device authorization response is missing required fields")
	}
	return payload, nil
}

func (d *kimiOAuthDriver) tokenRequest(ctx context.Context, values url.Values) (oauthTokenPayload, int, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(d.oauthHost, "/")+"/api/oauth/token", strings.NewReader(values.Encode()))
	if err != nil {
		return oauthTokenPayload{}, 0, err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "ACC OAuth client")
	response, err := d.client.Do(request)
	if err != nil {
		return oauthTokenPayload{}, 0, err
	}
	defer response.Body.Close()
	var payload oauthTokenPayload
	if err := readBoundedJSON(response, &payload); err != nil {
		return oauthTokenPayload{}, response.StatusCode, err
	}
	return payload, response.StatusCode, nil
}

func (d *kimiOAuthDriver) Refresh(ctx context.Context, old authCredential) (authCredential, error) {
	payload, status, err := d.tokenRequest(ctx, url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {old.RefreshToken},
		"client_id":     {d.clientID},
	})
	if err != nil {
		return authCredential{}, err
	}
	if status < 200 || status >= 300 {
		return authCredential{}, &oauthError{Status: status, Code: payload.Error, Description: payload.ErrorDescription}
	}
	return kimiCredentialFromPayload(payload, old.RefreshToken, d.now()), nil
}

func kimiCredentialFromPayload(payload oauthTokenPayload, refreshFallback string, now time.Time) authCredential {
	refresh := payload.RefreshToken
	if refresh == "" {
		refresh = refreshFallback
	}
	identity := jwtIdentity(payload.IDToken)
	if identity.AccountID == "" {
		identity = jwtIdentity(payload.AccessToken)
	}
	return authCredential{
		Provider: "kimi", Kind: "oauth", AccessToken: payload.AccessToken, RefreshToken: refresh,
		ExpiresAt: now.Add(time.Duration(payload.ExpiresIn) * time.Second),
		AccountID: identity.AccountID, Email: strings.ToLower(identity.Email), Origin: "browser-oauth",
	}
}

type tokenIdentity struct {
	AccountID string
	Email     string
}

func jwtIdentity(token string) tokenIdentity {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return tokenIdentity{}
	}
	b, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return tokenIdentity{}
	}
	var payload struct {
		Sub    string `json:"sub"`
		UserID string `json:"user_id"`
		Email  string `json:"email"`
	}
	if json.Unmarshal(b, &payload) != nil {
		return tokenIdentity{}
	}
	accountID := payload.UserID
	if accountID == "" {
		accountID = payload.Sub
	}
	return tokenIdentity{AccountID: accountID, Email: payload.Email}
}

type xaiOAuthDriver struct {
	client       *http.Client
	discoveryURL string
	clientID     string
	scope        string
	now          func() time.Time
	sleep        sleepContextFunc
}

func newXAIOAuthDriver(client *http.Client) *xaiOAuthDriver {
	if client == nil {
		client = oauthHTTPClient()
	}
	return &xaiOAuthDriver{
		client: client, discoveryURL: "https://auth.x.ai/.well-known/openid-configuration", clientID: xaiOAuthClientID,
		scope: "openid profile email offline_access grok-cli:access api:access", now: time.Now, sleep: sleepContext,
	}
}

type xaiDiscovery struct {
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
}

func validateXAIEndpoint(rawURL string) (string, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}
	host := strings.ToLower(parsed.Hostname())
	if parsed.Scheme != "https" || (host != "x.ai" && !strings.HasSuffix(host, ".x.ai")) {
		return "", fmt.Errorf("xAI discovery returned an unsafe endpoint")
	}
	return parsed.String(), nil
}

func (d *xaiOAuthDriver) discover(ctx context.Context) (xaiDiscovery, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, d.discoveryURL, nil)
	if err != nil {
		return xaiDiscovery{}, err
	}
	request.Header.Set("Accept", "application/json")
	response, err := d.client.Do(request)
	if err != nil {
		return xaiDiscovery{}, err
	}
	defer response.Body.Close()
	var discovery xaiDiscovery
	if err := readBoundedJSON(response, &discovery); err != nil {
		return xaiDiscovery{}, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return xaiDiscovery{}, fmt.Errorf("xAI OAuth discovery HTTP %d", response.StatusCode)
	}
	discovery.AuthorizationEndpoint, err = validateXAIEndpoint(discovery.AuthorizationEndpoint)
	if err != nil {
		return xaiDiscovery{}, err
	}
	discovery.TokenEndpoint, err = validateXAIEndpoint(discovery.TokenEndpoint)
	if err != nil {
		return xaiDiscovery{}, err
	}
	return discovery, nil
}

func (d *xaiOAuthDriver) Login(ctx context.Context, onAuthorization func(string)) (authCredential, error) {
	return d.LoginWithManual(ctx, onAuthorization, nil)
}

func (d *xaiOAuthDriver) LoginWithManual(ctx context.Context, onAuthorization func(string), manualInput func() (string, error)) (authCredential, error) {
	discovery, err := d.discover(ctx)
	if err != nil {
		return authCredential{}, err
	}
	verifier, challenge, err := generatePKCE()
	if err != nil {
		return authCredential{}, err
	}
	state, err := randomURLToken(24)
	if err != nil {
		return authCredential{}, err
	}
	nonce, err := randomURLToken(24)
	if err != nil {
		return authCredential{}, err
	}
	callback, err := startOAuthCallbackServer(56121, "/callback", state)
	if err != nil {
		return authCredential{}, fmt.Errorf("xAI callback port 56121 is unavailable: %w", err)
	}
	defer callback.Close(context.Background())
	params := url.Values{
		"response_type":         {"code"},
		"client_id":             {d.clientID},
		"redirect_uri":          {callback.RedirectURI()},
		"scope":                 {d.scope},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
		"state":                 {state},
		"nonce":                 {nonce},
	}
	authorizationURL := discovery.AuthorizationEndpoint + "?" + params.Encode()
	if onAuthorization != nil {
		onAuthorization(authorizationURL)
	}
	waitCtx := ctx
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		waitCtx, cancel = context.WithTimeout(ctx, oauthCallbackTimeout)
		defer cancel()
	}
	var callbackResult oauthCallbackResult
	if manualInput != nil {
		input, inputErr := manualInput()
		if inputErr != nil {
			return authCredential{}, inputErr
		}
		if strings.TrimSpace(input) != "" {
			callbackResult, err = manualOAuthCallbackResult(input, state)
		} else {
			callbackResult, err = callback.Wait(waitCtx)
		}
	} else {
		callbackResult, err = callback.Wait(waitCtx)
	}
	if err != nil {
		return authCredential{}, err
	}
	payload, err := d.postToken(ctx, discovery.TokenEndpoint, url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {d.clientID},
		"code":          {callbackResult.Code},
		"redirect_uri":  {callback.RedirectURI()},
		"code_verifier": {verifier},
	})
	if err != nil {
		return authCredential{}, err
	}
	return xaiCredentialFromPayload(payload, "", d.now()), nil
}

func manualOAuthCallbackResult(input, expectedState string) (oauthCallbackResult, error) {
	parsed := parseOAuthCallbackInput(input)
	if parsed.Code == "" {
		return oauthCallbackResult{}, fmt.Errorf("manual callback input has no authorization code")
	}
	if parsed.State != expectedState {
		return oauthCallbackResult{}, fmt.Errorf("manual callback state mismatch")
	}
	return oauthCallbackResult{Code: parsed.Code, State: parsed.State}, nil
}

func (d *xaiOAuthDriver) postToken(ctx context.Context, endpoint string, values url.Values) (oauthTokenPayload, error) {
	var lastErr error
	for attempt := 1; attempt <= 3; attempt++ {
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(values.Encode()))
		if err != nil {
			return oauthTokenPayload{}, err
		}
		request.Header.Set("Accept", "application/json")
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		response, err := d.client.Do(request)
		if err != nil {
			lastErr = err
			if attempt < 3 {
				if sleepErr := d.sleep(ctx, time.Duration(attempt)*100*time.Millisecond); sleepErr != nil {
					return oauthTokenPayload{}, sleepErr
				}
				continue
			}
			return oauthTokenPayload{}, err
		}
		var payload oauthTokenPayload
		decodeErr := readBoundedJSON(response, &payload)
		response.Body.Close()
		if decodeErr != nil {
			return oauthTokenPayload{}, decodeErr
		}
		if response.StatusCode >= 200 && response.StatusCode < 300 {
			if payload.AccessToken == "" {
				return oauthTokenPayload{}, fmt.Errorf("xAI token response is missing access_token")
			}
			return payload, nil
		}
		lastErr = &oauthError{Status: response.StatusCode, Code: payload.Error, Description: payload.ErrorDescription}
		temporary := response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500
		if !temporary || attempt == 3 {
			return oauthTokenPayload{}, lastErr
		}
		delay := time.Duration(attempt) * 100 * time.Millisecond
		if retryAfter := response.Header.Get("Retry-After"); retryAfter != "" {
			if seconds, err := strconv.Atoi(retryAfter); err == nil && seconds >= 0 && seconds <= 30 {
				delay = time.Duration(seconds) * time.Second
			}
		}
		if err := d.sleep(ctx, delay); err != nil {
			return oauthTokenPayload{}, err
		}
	}
	return oauthTokenPayload{}, lastErr
}

func (d *xaiOAuthDriver) Refresh(ctx context.Context, old authCredential) (authCredential, error) {
	discovery, err := d.discover(ctx)
	if err != nil {
		return authCredential{}, err
	}
	payload, err := d.postToken(ctx, discovery.TokenEndpoint, url.Values{
		"grant_type":    {"refresh_token"},
		"client_id":     {d.clientID},
		"refresh_token": {old.RefreshToken},
	})
	if err != nil {
		return authCredential{}, err
	}
	return xaiCredentialFromPayload(payload, old.RefreshToken, d.now()), nil
}

func xaiCredentialFromPayload(payload oauthTokenPayload, fallback string, now time.Time) authCredential {
	refresh := payload.RefreshToken
	if refresh == "" {
		refresh = fallback
	}
	identity := jwtIdentity(payload.IDToken)
	if identity.AccountID == "" {
		identity = jwtIdentity(payload.AccessToken)
	}
	return authCredential{
		Provider: "xai", Kind: "oauth", AccessToken: payload.AccessToken, RefreshToken: refresh,
		ExpiresAt: now.Add(time.Duration(payload.ExpiresIn) * time.Second), AccountID: identity.AccountID,
		Email: strings.ToLower(identity.Email), Origin: "browser-oauth",
	}
}

func detectGrokCLICredential() (authCredential, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return authCredential{}, err
	}
	b, err := os.ReadFile(filepath.Join(home, ".grok", "auth.json"))
	if err != nil {
		return authCredential{}, err
	}
	var entries map[string]map[string]any
	if json.Unmarshal(b, &entries) != nil {
		return authCredential{}, fmt.Errorf("Grok CLI credential format is not recognized")
	}
	for key, entry := range entries {
		if !strings.HasPrefix(key, "https://auth.x.ai::") {
			continue
		}
		access, _ := entry["key"].(string)
		refresh, _ := entry["refresh_token"].(string)
		if access == "" || refresh == "" {
			continue
		}
		credential := authCredential{Provider: "xai", Kind: "oauth", AccessToken: access, RefreshToken: refresh, Origin: "grok-cli-import"}
		credential.AccountID, _ = entry["user_id"].(string)
		credential.Email, _ = entry["email"].(string)
		if rawExpiry, ok := entry["expires_at"].(string); ok {
			credential.ExpiresAt, _ = time.Parse(time.RFC3339, rawExpiry)
		}
		return credential, nil
	}
	return authCredential{}, errCredentialNotFound
}

func parseClaudeCredential(b []byte) (authCredential, error) {
	var payload struct {
		OAuth *struct {
			AccessToken  string `json:"accessToken"`
			RefreshToken string `json:"refreshToken"`
			ExpiresAt    int64  `json:"expiresAt"`
		} `json:"claudeAiOauth"`
	}
	if err := json.Unmarshal(b, &payload); err != nil || payload.OAuth == nil || payload.OAuth.AccessToken == "" || payload.OAuth.RefreshToken == "" {
		return authCredential{}, fmt.Errorf("Claude Code credential format is not recognized")
	}
	return authCredential{
		Provider: "anthropic", Kind: "oauth", AccessToken: payload.OAuth.AccessToken,
		RefreshToken: payload.OAuth.RefreshToken, ExpiresAt: time.UnixMilli(payload.OAuth.ExpiresAt),
		Origin: "claude-code-import",
	}, nil
}

func detectClaudeCodeCredential(ctx context.Context) (authCredential, error) {
	if output, err := exec.CommandContext(ctx, "security", "find-generic-password", "-s", "Claude Code-credentials", "-w").Output(); err == nil {
		if credential, parseErr := parseClaudeCredential(output); parseErr == nil {
			return credential, nil
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return authCredential{}, err
	}
	configDir := strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR"))
	if configDir == "" {
		configDir = filepath.Join(home, ".claude")
	}
	b, err := os.ReadFile(filepath.Join(configDir, ".credentials.json"))
	if err != nil {
		return authCredential{}, err
	}
	return parseClaudeCredential(b)
}

func readOAuthErrorBody(response *http.Response) *oauthError {
	defer response.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(response.Body, maxOAuthResponseBytes))
	var payload oauthTokenPayload
	_ = json.Unmarshal(b, &payload)
	return &oauthError{Status: response.StatusCode, Code: payload.Error, Description: payload.ErrorDescription}
}

func isCredentialMissing(err error) bool {
	return errors.Is(err, errCredentialNotFound) || errors.Is(err, os.ErrNotExist)
}
