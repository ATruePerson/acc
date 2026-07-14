package main

import (
	"context"
	"log"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

const rpm = 40

type providerRateLimiter struct {
	mu       sync.RWMutex
	limiters map[string]*rate.Limiter
}

func newProviderRateLimiter(cfg *Config) *providerRateLimiter {
	pr := &providerRateLimiter{
		limiters: make(map[string]*rate.Limiter),
	}
	for name := range cfg.Providers {
		pr.limiters[name] = rate.NewLimiter(rate.Every(time.Minute/rpm), 1)
		log.Printf("rate limiter: %s — %d RPM (1 req / %.1fs)", name, rpm, time.Minute.Seconds()/rpm)
	}
	return pr
}

func (pr *providerRateLimiter) Wait(ctx context.Context, provider string) error {
	pr.mu.RLock()
	l, ok := pr.limiters[provider]
	pr.mu.RUnlock()
	if !ok {
		return nil
	}
	return l.Wait(ctx)
}
