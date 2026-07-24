package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"strings"
	"sync"
	"time"
)

const (
	maxOAuthResponseBytes = 1 << 20
	oauthRequestTimeout   = 30 * time.Second
	oauthCallbackTimeout  = 5 * time.Minute
)

func generatePKCE() (verifier, challenge string, err error) {
	b := make([]byte, 64)
	if _, err := rand.Read(b); err != nil {
		return "", "", err
	}
	verifier = base64.RawURLEncoding.EncodeToString(b)
	digest := sha256.Sum256([]byte(verifier))
	challenge = base64.RawURLEncoding.EncodeToString(digest[:])
	return verifier, challenge, nil
}

func randomURLToken(bytesCount int) (string, error) {
	b := make([]byte, bytesCount)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

type oauthCallbackResult struct {
	Code  string
	State string
}

type oauthCallbackServer struct {
	listener net.Listener
	server   *http.Server
	path     string
	result   chan oauthCallbackResult
	err      chan error
	once     sync.Once
}

func startOAuthCallbackServer(port int, path, expectedState string) (*oauthCallbackServer, error) {
	if path == "" || !strings.HasPrefix(path, "/") {
		return nil, fmt.Errorf("OAuth callback path must start with /")
	}
	listener, err := net.Listen("tcp4", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return nil, err
	}
	callback := &oauthCallbackServer{
		listener: listener,
		path:     path,
		result:   make(chan oauthCallbackResult, 1),
		err:      make(chan error, 1),
	}
	mux := http.NewServeMux()
	mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		state := query.Get("state")
		if state != expectedState {
			oauthCallbackPage(w, http.StatusBadRequest, "Login failed", "State mismatch. Return to ACC and try again.")
			return
		}
		if providerError := query.Get("error"); providerError != "" {
			description := query.Get("error_description")
			if description == "" {
				description = providerError
			}
			callback.deliverError(&oauthError{Code: providerError, Description: description})
			oauthCallbackPage(w, http.StatusBadRequest, "Login failed", "The provider denied or could not complete authorization.")
			return
		}
		code := query.Get("code")
		if code == "" {
			oauthCallbackPage(w, http.StatusBadRequest, "Login failed", "The callback did not contain an authorization code.")
			return
		}
		callback.once.Do(func() {
			callback.result <- oauthCallbackResult{Code: code, State: state}
		})
		oauthCallbackPage(w, http.StatusOK, "Login complete", "You can close this tab and return to ACC.")
	})
	callback.server = &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       5 * time.Second,
	}
	go func() {
		if err := callback.server.Serve(listener); err != nil && err != http.ErrServerClosed {
			callback.deliverError(err)
		}
	}()
	return callback, nil
}

func (s *oauthCallbackServer) deliverError(err error) {
	s.once.Do(func() { s.err <- err })
}

func (s *oauthCallbackServer) RedirectURI() string {
	_, port, _ := net.SplitHostPort(s.listener.Addr().String())
	return "http://127.0.0.1:" + port + s.path
}

func (s *oauthCallbackServer) Wait(ctx context.Context) (oauthCallbackResult, error) {
	select {
	case result := <-s.result:
		return result, nil
	case err := <-s.err:
		return oauthCallbackResult{}, err
	case <-ctx.Done():
		return oauthCallbackResult{}, ctx.Err()
	}
}

func (s *oauthCallbackServer) Close(ctx context.Context) error {
	return s.server.Shutdown(ctx)
}

func oauthCallbackPage(w http.ResponseWriter, status int, title, message string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = fmt.Fprintf(w, "<!doctype html><html><head><meta charset=utf-8><title>ACC</title></head><body><h2>%s</h2><p>%s</p></body></html>", html.EscapeString(title), html.EscapeString(message))
}

type oauthCallbackInputKind int

const (
	oauthCallbackRaw oauthCallbackInputKind = iota
	oauthCallbackURL
	oauthCallbackQuery
)

type oauthCallbackInput struct {
	Kind  oauthCallbackInputKind
	Code  string
	State string
}

func parseOAuthCallbackInput(input string) oauthCallbackInput {
	value := strings.TrimSpace(input)
	if parsed, err := url.ParseRequestURI(value); err == nil && parsed.IsAbs() {
		return oauthCallbackInput{Kind: oauthCallbackURL, Code: parsed.Query().Get("code"), State: parsed.Query().Get("state")}
	}
	if strings.Contains(value, "code=") {
		query, _ := url.ParseQuery(strings.TrimLeft(value, "?#"))
		return oauthCallbackInput{Kind: oauthCallbackQuery, Code: query.Get("code"), State: query.Get("state")}
	}
	parts := strings.SplitN(value, "#", 2)
	result := oauthCallbackInput{Kind: oauthCallbackRaw, Code: parts[0]}
	if len(parts) == 2 {
		result.State = parts[1]
	}
	return result
}

func readBoundedJSON(response *http.Response, target any) error {
	limited := io.LimitReader(response.Body, maxOAuthResponseBytes+1)
	b, err := io.ReadAll(limited)
	if err != nil {
		return err
	}
	if len(b) > maxOAuthResponseBytes {
		return fmt.Errorf("OAuth response exceeds %d bytes", maxOAuthResponseBytes)
	}
	if err := json.Unmarshal(b, target); err != nil {
		return fmt.Errorf("OAuth response is malformed JSON")
	}
	return nil
}

func oauthHTTPClient() *http.Client {
	return &http.Client{Timeout: oauthRequestTimeout}
}

func openBrowserURL(rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Scheme != "https" {
		return fmt.Errorf("refusing to open non-HTTPS authorization URL")
	}
	return exec.Command("open", rawURL).Start()
}
