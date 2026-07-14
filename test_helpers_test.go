package main

import "net/http"

func testServer(cfg *Config) *server {
	s := &server{
		http:    &http.Client{},
		limiter: newProviderRateLimiter(cfg),
	}
	s.cfg.Store(cfg)
	return s
}
