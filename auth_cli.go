package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

func normalizeAuthProvider(provider string) (string, error) {
	provider = strings.ToLower(strings.TrimSpace(provider))
	if provider == "grok" {
		provider = "xai"
	}
	switch provider {
	case "kimi", "xai", "anthropic":
		return provider, nil
	default:
		return "", fmt.Errorf("unsupported auth provider %q (use kimi, xai/grok, or anthropic)", provider)
	}
}

func cmdAuth(args []string) {
	manager, err := newDefaultAuthManager()
	if err != nil {
		fmt.Printf("  Secure credential store unavailable: %v\n", err)
		return
	}
	if len(args) == 0 {
		args = []string{"list"}
	}
	ctx := context.Background()
	switch args[0] {
	case "list":
		fmt.Println("  kimi       OAuth device login")
		fmt.Println("  xai/grok   Experimental OAuth PKCE login or XAI_API_KEY")
		fmt.Println("  anthropic  Stable ANTHROPIC_API_KEY; optional read-only Claude Code import")
	case "status":
		provider := ""
		if len(args) > 1 {
			provider, err = normalizeAuthProvider(args[1])
			if err != nil {
				fmt.Printf("  %v\n", err)
				return
			}
		}
		if err := runAuthStatus(ctx, os.Stdout, manager, provider); err != nil {
			fmt.Printf("  Auth status failed: %v\n", err)
		}
	case "logout":
		if len(args) != 2 {
			fmt.Println("  Usage: acc auth logout PROVIDER")
			return
		}
		if err := logoutProvider(ctx, manager, args[1], providerModelCachePath()); err != nil {
			fmt.Printf("  Logout failed: %v\n", err)
			return
		}
		provider, _ := normalizeAuthProvider(args[1])
		fmt.Printf("  Logged out of %s. API keys and other providers were preserved.\n", provider)
	case "login":
		if len(args) < 2 {
			fmt.Println("  Usage: acc auth login PROVIDER [--import-grok-cli|--import-claude-code|--experimental-oauth]")
			return
		}
		if err := loginProvider(ctx, os.Stdout, manager, args[1], args[2:]); err != nil {
			fmt.Printf("  Login failed: %v\n", err)
		}
	default:
		fmt.Println("  Usage: acc auth list|login|status|logout")
	}
}

func runAuthStatus(ctx context.Context, out io.Writer, manager *authManager, only string) error {
	providers := []string{"kimi", "xai", "anthropic"}
	if only != "" {
		providers = []string{only}
	}
	for _, provider := range providers {
		if provider == "xai" && strings.TrimSpace(os.Getenv("XAI_API_KEY")) != "" {
			fmt.Fprintln(out, "  xai: ready (API key)")
			continue
		}
		if provider == "anthropic" && strings.TrimSpace(os.Getenv("ANTHROPIC_API_KEY")) != "" {
			fmt.Fprintln(out, "  anthropic: ready (API key, stable)")
			continue
		}
		credential, err := manager.Status(ctx, provider)
		if err != nil {
			if isCredentialMissing(err) {
				fmt.Fprintf(out, "  %s: logged out\n", provider)
				continue
			}
			return err
		}
		state := "ready"
		if credential.NeedsReauth {
			state = "reauthentication required"
		} else if !credential.ExpiresAt.IsZero() && credential.ExpiresAt.Before(time.Now()) {
			state = "expired; refreshes lazily"
		}
		identity := ""
		if credential.Email != "" {
			identity = ", " + credential.Email
		}
		expiry := ""
		if !credential.ExpiresAt.IsZero() {
			expiry = ", expires " + credential.ExpiresAt.Local().Format(time.RFC3339)
		}
		fmt.Fprintf(out, "  %s: %s (%s, %s%s%s)\n", provider, state, credential.Kind, credential.Origin, identity, expiry)
	}
	return nil
}

func logoutProvider(ctx context.Context, manager *authManager, provider, cachePath string) error {
	normalized, err := normalizeAuthProvider(provider)
	if err != nil {
		return err
	}
	if err := manager.Delete(ctx, normalized); err != nil && !isCredentialMissing(err) {
		return err
	}
	if cachePath != "" {
		cache, err := readProviderModelCache(cachePath)
		if err != nil {
			return err
		}
		delete(cache.Providers, normalized)
		if err := writeProviderModelCache(cachePath, cache); err != nil {
			return err
		}
	}
	return nil
}

func loginProvider(ctx context.Context, out io.Writer, manager *authManager, provider string, args []string) error {
	provider, err := normalizeAuthProvider(provider)
	if err != nil {
		return err
	}
	flags := map[string]bool{}
	for _, arg := range args {
		flags[arg] = true
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Minute)
	defer cancel()
	var credential authCredential
	switch provider {
	case "kimi":
		if len(args) != 0 {
			return fmt.Errorf("Kimi login takes no flags")
		}
		driver := newKimiOAuthDriver(nil)
		credential, err = driver.Login(ctx, func(rawURL, userCode string) {
			fmt.Fprintf(out, "  Open: %s\n  Code: %s\n", rawURL, userCode)
			_ = openBrowserURL(rawURL)
		})
	case "xai":
		if flags["--import-grok-cli"] {
			fmt.Fprintln(out, "  Importing may cause refresh-token rotation to detach the official Grok CLI session.")
			credential, err = detectGrokCLICredential()
		} else {
			if len(args) != 0 {
				return fmt.Errorf("unknown xAI login flag")
			}
			fmt.Fprintln(out, "  Experimental: xAI does not currently document this third-party OAuth flow. API keys are the stable path.")
			driver := newXAIOAuthDriver(nil)
			credential, err = driver.LoginWithManual(ctx, func(rawURL string) {
				fmt.Fprintf(out, "  Open: %s\n", rawURL)
				_ = openBrowserURL(rawURL)
			}, func() (string, error) {
				fmt.Fprintln(out, "  Press Enter to wait for the browser callback, or paste the full callback URL:")
				return bufio.NewReader(os.Stdin).ReadString('\n')
			})
		}
	case "anthropic":
		switch {
		case flags["--import-claude-code"]:
			fmt.Fprintln(out, "  Copying the existing Claude Code credential read-only. ACC will not modify the official credential.")
			credential, err = detectClaudeCodeCredential(ctx)
		case flags["--experimental-oauth"]:
			return fmt.Errorf("Anthropic subscription OAuth is not implemented: official third-party inference support could not be verified; use ANTHROPIC_API_KEY")
		case len(args) == 0:
			if strings.TrimSpace(os.Getenv("ANTHROPIC_API_KEY")) == "" {
				return fmt.Errorf("set ANTHROPIC_API_KEY for the stable Anthropic path, or explicitly use --import-claude-code for a read-only copy")
			}
			fmt.Fprintln(out, "  Anthropic API key detected. It remains in your environment and was not copied.")
			return refreshCatalogAfterLogin(ctx, out, manager, provider)
		default:
			return fmt.Errorf("unknown Anthropic login flag")
		}
	}
	if err != nil {
		return err
	}
	if credential.Provider != provider {
		return fmt.Errorf("credential provider mismatch")
	}
	if err := manager.Save(ctx, credential); err != nil {
		return err
	}
	fmt.Fprintf(out, "  Logged in to %s using %s. Tokens were saved in %s.\n", provider, credential.Origin, manager.storeName)
	return refreshCatalogAfterLogin(ctx, out, manager, provider)
}

func refreshCatalogAfterLogin(ctx context.Context, out io.Writer, manager *authManager, provider string) error {
	loadDotenv(defaultEnvPath())
	var cfg *Config
	if loaded, err := loadConfig(defaultConfigPath()); err == nil {
		cfg = loaded
	}
	discoveryCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := refreshProviderModelCache(discoveryCtx, &http.Client{Timeout: 15 * time.Second}, cfg, manager, provider); err != nil {
		fmt.Fprintf(out, "  Login saved. live model refresh failed, so ACC will use its local model catalog: %v\n", err)
		return nil
	}
	fmt.Fprintln(out, "  Refreshed the provider's real-model catalog.")
	return nil
}
