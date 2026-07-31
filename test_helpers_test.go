package main

import "net/http"

type errReader struct {
	err error
}

func (r *errReader) Read(p []byte) (int, error) {
	return 0, r.err
}

func testServer(cfg *Config) *server {
	s := &server{
		http:    &http.Client{},
		limiter: newProviderRateLimiter(cfg),
	}
	s.cfg.Store(cfg)
	return s
}
