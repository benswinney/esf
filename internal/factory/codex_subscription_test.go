package factory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mitkox/esf/internal/tomlx"
)

func writeSecret(t *testing.T, dir, name, value string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(value), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func codexSubscriptionHarness(t *testing.T) HarnessConfig {
	t.Helper()
	dir := t.TempDir()
	return HarnessConfig{Type: "codex", Binary: "/opt/esf/agents/codex", BinarySHA256: strings.Repeat("a", 64), Preinstalled: true,
		BaseURL: "https://chatgpt.com/backend-api/codex", Timeout: tomlx.FromStd(time.Minute),
		CredentialMode: "cube_egress", CredentialType: "subscription_token",
		APIKeyFile:    writeSecret(t, dir, "token", "chatgpt-access-token\n"),
		AccountIDFile: writeSecret(t, dir, "account", "acct-1234\n")}
}

func TestCodexSubscriptionTokenConfigValidates(t *testing.T) {
	cfg := testValidConfigForCredentialPolicy()
	cfg.Harnesses["codex"] = codexSubscriptionHarness(t)
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestAccountIDFileRules(t *testing.T) {
	type invalid struct {
		mutate func(*HarnessConfig)
		want   string
	}
	for name, tc := range map[string]invalid{
		"codex subscription without account_id_file": {func(h *HarnessConfig) { h.AccountIDFile = "" }, "requires account_id_file"},
		"codex subscription without cube_egress": {func(h *HarnessConfig) {
			h.CredentialMode = "environment"
			h.APIKeyFile = ""
			h.APIKeyEnv = "TOKEN"
		}, "credential_type subscription_token requires"},
		"account_id_file with codex api_key": {func(h *HarnessConfig) {
			h.CredentialType = "api_key"
			h.BaseURL = "https://api.openai.com/v1"
		}, "account_id_file requires"},
		"account_id_file with claude subscription": {func(h *HarnessConfig) {
			h.Type = "claude"
			h.Binary = "/opt/esf/agents/claude"
			h.BaseURL = "https://api.anthropic.com"
		}, "account_id_file requires"},
		"account_id_file with claude api_key": {func(h *HarnessConfig) {
			h.Type = "claude"
			h.Binary = "/opt/esf/agents/claude"
			h.BaseURL = "https://api.anthropic.com"
			h.CredentialType = ""
		}, "account_id_file requires"},
		"account_id_file with opencode": {func(h *HarnessConfig) {
			h.Type = "opencode"
			h.CredentialType = ""
		}, "account_id_file requires"},
		"relative account_id_file": {func(h *HarnessConfig) { h.AccountIDFile = "account" }, "absolute path"},
	} {
		t.Run(name, func(t *testing.T) {
			cfg := testValidConfigForCredentialPolicy()
			hc := codexSubscriptionHarness(t)
			tc.mutate(&hc)
			cfg.Harnesses["codex"] = hc
			if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestCodexSubscriptionTokenInjectsTokenAndAccountID(t *testing.T) {
	cfg := testValidConfigForCredentialPolicy()
	cfg.Harnesses["codex"] = codexSubscriptionHarness(t)
	network, applies, err := cfg.runtimeNetworkForHarness("codex")
	if err != nil || !applies {
		t.Fatalf("applies = %v, err = %v", applies, err)
	}
	wantPaths := []string{"/backend-api/codex/responses", "/backend-api/codex/responses/compact"}
	if len(network.Rules) != len(wantPaths) {
		t.Fatalf("rules = %+v", network.Rules)
	}
	for i, rule := range network.Rules {
		m := rule.Match
		if m.Host != "chatgpt.com" || m.SNI != "chatgpt.com" || m.Path != wantPaths[i] || len(m.Method) != 1 || m.Method[0] != "POST" {
			t.Fatalf("rule %d match = %+v", i, m)
		}
		inject := rule.Action.Inject
		if len(inject) != 2 ||
			inject[0].Header != "Authorization" || inject[0].Format != "Bearer ${SECRET}" || inject[0].Secret != "chatgpt-access-token" ||
			inject[1].Header != "chatgpt-account-id" || inject[1].Format != "${SECRET}" || inject[1].Secret != "acct-1234" {
			t.Fatalf("rule %d inject = %+v", i, inject)
		}
	}
}

func TestCodexSubscriptionRejectsBadAccountIDFile(t *testing.T) {
	for name, value := range map[string]string{
		"empty":     "  \n",
		"too large": strings.Repeat("a", 2049),
	} {
		t.Run(name, func(t *testing.T) {
			cfg := testValidConfigForCredentialPolicy()
			hc := codexSubscriptionHarness(t)
			hc.AccountIDFile = writeSecret(t, t.TempDir(), "account", value)
			cfg.Harnesses["codex"] = hc
			if _, _, err := cfg.runtimeNetworkForHarness("codex"); err == nil {
				t.Fatal("accepted a bad account ID file")
			}
		})
	}
}

func TestCodexSubscriptionReadsFilesForEachNetwork(t *testing.T) {
	cfg := testValidConfigForCredentialPolicy()
	hc := codexSubscriptionHarness(t)
	cfg.Harnesses["codex"] = hc
	if err := os.WriteFile(hc.APIKeyFile, []byte("refreshed-token"), 0o600); err != nil {
		t.Fatal(err)
	}
	network, _, err := cfg.runtimeNetworkForHarness("codex")
	if err != nil {
		t.Fatal(err)
	}
	if got := network.Rules[0].Action.Inject[0].Secret; got != "refreshed-token" {
		t.Fatalf("token = %q", got)
	}
}
