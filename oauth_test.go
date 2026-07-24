package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestGeneratePKCEUsesS256AndRFCVerifierLength(t *testing.T) {
	verifier, challenge, err := generatePKCE()
	if err != nil {
		t.Fatal(err)
	}
	if len(verifier) < 43 || len(verifier) > 128 {
		t.Fatalf("verifier length = %d", len(verifier))
	}
	digest := sha256.Sum256([]byte(verifier))
	want := base64.RawURLEncoding.EncodeToString(digest[:])
	if challenge != want {
		t.Fatalf("challenge = %q, want S256 %q", challenge, want)
	}
}

func TestOAuthCallbackBindsOnlyIPv4LoopbackAndRejectsStateMismatch(t *testing.T) {
	callback, err := startOAuthCallbackServer(0, "/callback", "expected-state")
	if err != nil {
		t.Fatal(err)
	}
	defer callback.Close(context.Background())
	host, _, err := net.SplitHostPort(callback.listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	if host != "127.0.0.1" {
		t.Fatalf("callback bound to %q", host)
	}

	client := &http.Client{Timeout: time.Second}
	bad, err := client.Get(callback.RedirectURI() + "?code=bad&state=wrong")
	if err != nil {
		t.Fatal(err)
	}
	bad.Body.Close()
	if bad.StatusCode != http.StatusBadRequest {
		t.Fatalf("state mismatch status = %d", bad.StatusCode)
	}

	good, err := client.Get(callback.RedirectURI() + "?code=good-code&state=expected-state")
	if err != nil {
		t.Fatal(err)
	}
	good.Body.Close()
	result, err := callback.Wait(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Code != "good-code" || result.State != "expected-state" {
		t.Fatalf("callback result = %+v", result)
	}
}

func TestOAuthCallbackHonorsTimeoutAndCancellation(t *testing.T) {
	for _, tc := range []struct {
		name string
		ctx  func() (context.Context, context.CancelFunc)
	}{
		{"timeout", func() (context.Context, context.CancelFunc) {
			return context.WithTimeout(context.Background(), 10*time.Millisecond)
		}},
		{"cancel", func() (context.Context, context.CancelFunc) {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			return ctx, func() {}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			callback, err := startOAuthCallbackServer(0, "/callback", "state")
			if err != nil {
				t.Fatal(err)
			}
			defer callback.Close(context.Background())
			ctx, cancel := tc.ctx()
			defer cancel()
			if _, err := callback.Wait(ctx); err == nil {
				t.Fatal("expected context error")
			}
		})
	}
}

func TestParseOAuthCallbackInputDistinguishesResponsesFromRawCode(t *testing.T) {
	cases := []struct {
		input string
		kind  oauthCallbackInputKind
		code  string
		state string
	}{
		{"http://127.0.0.1/callback?code=abc&state=s", oauthCallbackURL, "abc", "s"},
		{"?code=def&state=t", oauthCallbackQuery, "def", "t"},
		{"raw-code", oauthCallbackRaw, "raw-code", ""},
	}
	for _, tc := range cases {
		got := parseOAuthCallbackInput(tc.input)
		if got.Kind != tc.kind || got.Code != tc.code || got.State != tc.state {
			t.Fatalf("parse %q = %+v", tc.input, got)
		}
	}
}

func TestManualOAuthCallbackRequiresMatchingState(t *testing.T) {
	result, err := manualOAuthCallbackResult("http://127.0.0.1/callback?code=abc&state=expected", "expected")
	if err != nil || result.Code != "abc" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	for _, input := range []string{"abc", "?code=abc&state=wrong", "?state=expected"} {
		if _, err := manualOAuthCallbackResult(input, "expected"); err == nil {
			t.Fatalf("expected rejection for %q", input)
		}
	}
}

func TestReadBoundedJSONRejectsOversizedOAuthResponse(t *testing.T) {
	response := &http.Response{Body: io.NopCloser(strings.NewReader(strings.Repeat("x", maxOAuthResponseBytes+1)))}
	defer response.Body.Close()
	var target map[string]any
	if err := readBoundedJSON(response, &target); err == nil {
		t.Fatal("expected oversized response to fail")
	}
}
