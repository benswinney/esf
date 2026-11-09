package factory

import (
	"strings"
	"testing"
	"time"

	"github.com/mitkox/esf/internal/tomlx"
)

func subscriptionHarness() HarnessConfig {
	return HarnessConfig{Type: "claude", Binary: "/opt/esf/agents/claude", BinarySHA256: strings.Repeat("a", 64), Preinstalled: true,
		BaseURL: "https://api.anthropic.com", Timeout: tomlx.FromStd(time.Minute), CredentialMode: "cube_egress", CredentialType: "subscription_token", APIKeyEnv: "CLAUDE_SUBSCRIPTION_TOKEN"}
}

func TestSubscriptionTokenInjectsBearerAuthorization(t *testing.T) {
	cfg := testValidConfigForCredentialPolicy()
	hc := subscriptionHarness()
	cfg.Harnesses["claude"] = hc
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	network, err := cfg.networkForHarnessCredential("claude", hc, "token-value")
	if err != nil {
		t.Fatal(err)
	}
	if len(network.Rules) == 0 {
		t.Fatal("no rules")
	}
	for _, rule := range network.Rules {
		inject := rule.Action.Inject
		if len(inject) != 1 || inject[0].Header != "Authorization" || inject[0].Format != "Bearer ${SECRET}" || inject[0].Secret != "token-value" {
			t.Fatalf("inject = %+v", inject)
		}
	}
}

func TestSubscriptionTokenRejectedOutsideClaudeOrCodexEgress(t *testing.T) {
	for name, mutate := range map[string]func(*HarnessConfig){
		"environment mode": func(h *HarnessConfig) { h.CredentialMode = "environment" },
		"opencode":         func(h *HarnessConfig) { h.Type = "opencode" },
		"unknown type":     func(h *HarnessConfig) { h.CredentialType = "session_cookie" },
	} {
		t.Run(name, func(t *testing.T) {
			cfg := testValidConfigForCredentialPolicy()
			hc := subscriptionHarness()
			mutate(&hc)
			cfg.Harnesses["claude"] = hc
			if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "credential_type") {
				t.Fatalf("err = %v", err)
			}
		})
	}
}
