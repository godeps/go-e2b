package e2b

import (
	"testing"
)

func TestResolveAPIKey(t *testing.T) {
	t.Run("explicit value", func(t *testing.T) {
		got := resolveAPIKey("my-key")
		if got != "my-key" {
			t.Errorf("resolveAPIKey = %q, want %q", got, "my-key")
		}
	})

	t.Run("from env", func(t *testing.T) {
		t.Setenv(apiKeyEnv, "env-key")
		got := resolveAPIKey("")
		if got != "env-key" {
			t.Errorf("resolveAPIKey = %q, want %q", got, "env-key")
		}
	})

	t.Run("empty", func(t *testing.T) {
		// See TestResolveAPIBaseURL: the ambient value must not leak in.
		t.Setenv(apiKeyEnv, "")
		got := resolveAPIKey("")
		if got != "" {
			t.Errorf("resolveAPIKey = %q, want %q", got, "")
		}
	})
}

func TestResolveAPIBaseURL(t *testing.T) {
	t.Run("explicit value", func(t *testing.T) {
		got := resolveAPIBaseURL("https://custom.api")
		if got != "https://custom.api" {
			t.Errorf("got %q, want %q", got, "https://custom.api")
		}
	})

	t.Run("from env", func(t *testing.T) {
		t.Setenv(apiURLEnv, "https://env.api")
		got := resolveAPIBaseURL("")
		if got != "https://env.api" {
			t.Errorf("got %q, want %q", got, "https://env.api")
		}
	})

	t.Run("default", func(t *testing.T) {
		// The suite is expected to run with E2B_API_URL exported when it points
		// at a self-hosted deployment, so the default case has to clear it
		// rather than inherit whatever the ambient environment holds.
		t.Setenv(apiURLEnv, "")
		got := resolveAPIBaseURL("")
		if got != DefaultAPIBaseURL {
			t.Errorf("got %q, want %q", got, DefaultAPIBaseURL)
		}
	})
}

func TestResolveSandboxDomain(t *testing.T) {
	t.Run("explicit value", func(t *testing.T) {
		got := resolveSandboxDomain("custom.domain")
		if got != "custom.domain" {
			t.Errorf("got %q, want %q", got, "custom.domain")
		}
	})

	t.Run("from env", func(t *testing.T) {
		t.Setenv(sandboxURLEnv, "env.domain")
		got := resolveSandboxDomain("")
		if got != "env.domain" {
			t.Errorf("got %q, want %q", got, "env.domain")
		}
	})

	t.Run("default", func(t *testing.T) {
		// See TestResolveAPIBaseURL: the ambient value must not leak in.
		t.Setenv(sandboxURLEnv, "")
		got := resolveSandboxDomain("")
		if got != defaultSandboxDomain {
			t.Errorf("got %q, want %q", got, defaultSandboxDomain)
		}
	})
}
