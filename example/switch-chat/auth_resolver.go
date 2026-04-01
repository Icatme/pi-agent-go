package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	core "github.com/Icatme/pi-agent-go"
	pigo "github.com/Icatme/pi-go/pkg/pigo"
)

type storedOAuthCredentials struct {
	Type    string `json:"type"`
	Access  string `json:"access"`
	Refresh string `json:"refresh"`
	Expires int64  `json:"expires"`
}

func resolveModelRef(provider, model, authRoot string) (core.ModelRef, error) {
	ref := core.ModelRef{
		Provider: provider,
		Model:    model,
	}
	if strings.TrimSpace(authRoot) == "" {
		return ref, nil
	}

	supportDir := filepath.Join(authRoot, ".pigo")
	info, err := os.Stat(supportDir)
	if err != nil {
		return core.ModelRef{}, fmt.Errorf("switch-chat: auth root %q does not contain .pigo: %w", authRoot, err)
	}
	if !info.IsDir() {
		return core.ModelRef{}, fmt.Errorf("switch-chat: auth root %q has a non-directory .pigo entry", authRoot)
	}

	envValues := loadDotEnvFile(filepath.Join(supportDir, ".env"))
	authValues, err := loadAuthFile(filepath.Join(supportDir, "auth.json"))
	if err != nil {
		return core.ModelRef{}, err
	}

	apiKeyEnv := providerAPIKeyEnvName(provider)
	if apiKeyEnv != "" {
		if apiKey := strings.TrimSpace(os.Getenv(apiKeyEnv)); apiKey != "" {
			ref.ProviderConfig.APIKey = apiKey
			return ref, nil
		}
		if apiKey := strings.TrimSpace(envValues[apiKeyEnv]); apiKey != "" {
			ref.ProviderConfig.APIKey = apiKey
			return ref, nil
		}
	}

	if credential, ok := authValues[provider]; ok && strings.EqualFold(strings.TrimSpace(credential.Type), "oauth") && strings.TrimSpace(credential.Access) != "" {
		ref.ProviderConfig.Auth = &core.ProviderAuthConfig{
			Type: core.ProviderAuthTypeOAuth,
			OAuth: &core.OAuthCredentials{
				AccessToken:  credential.Access,
				RefreshToken: credential.Refresh,
				ExpiresUnix:  credential.Expires / 1000,
			},
		}
		return ref, nil
	}

	if pigo.RequiresOAuth(pigo.Provider(provider)) {
		return core.ModelRef{}, fmt.Errorf("switch-chat: missing OAuth credentials for %s in %s", provider, filepath.Join(supportDir, "auth.json"))
	}
	if apiKeyEnv != "" {
		return core.ModelRef{}, fmt.Errorf("switch-chat: missing API key %s for %s in environment or %s", apiKeyEnv, provider, filepath.Join(supportDir, ".env"))
	}
	return core.ModelRef{}, fmt.Errorf("switch-chat: no credential strategy is configured for provider %s", provider)
}

func providerAPIKeyEnvName(provider string) string {
	switch strings.TrimSpace(provider) {
	case "anthropic":
		return "ANTHROPIC_API_KEY"
	case "kimi-coding":
		return "KIMI_API_KEY"
	}

	normalized := strings.ToUpper(strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(provider), "-", "_"), " ", "_"))
	if normalized == "" {
		return ""
	}
	return normalized + "_API_KEY"
}

func loadAuthFile(path string) (map[string]storedOAuthCredentials, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]storedOAuthCredentials{}, nil
		}
		return nil, fmt.Errorf("switch-chat: failed to read %s: %w", path, err)
	}

	var auth map[string]storedOAuthCredentials
	if err := json.Unmarshal(body, &auth); err != nil {
		return nil, fmt.Errorf("switch-chat: failed to parse %s: %w", path, err)
	}
	if auth == nil {
		auth = map[string]storedOAuthCredentials{}
	}
	return auth, nil
}

func loadDotEnvFile(path string) map[string]string {
	file, err := os.Open(path)
	if err != nil {
		return map[string]string{}
	}
	defer file.Close()

	values := map[string]string{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		values[strings.TrimSpace(key)] = strings.TrimSpace(value)
	}
	return values
}
