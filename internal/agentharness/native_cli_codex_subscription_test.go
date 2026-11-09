package agentharness

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mitkox/esf/internal/sandbox"
)

func TestCodexSubscriptionTokenKeepsCredentialsOutOfSandbox(t *testing.T) {
	ctx := context.Background()
	const token, account = "real-chatgpt-access-token", "real-chatgpt-account-id"
	t.Setenv("OPENAI_API_KEY", token)
	h, err := NewCodex(NativeCLIOptions{EgressManaged: true, SubscriptionToken: true,
		BaseURL: "https://chatgpt.com/backend-api/codex", Timeout: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	h.lookupEnv = func(string) (string, bool) { t.Fatal("read host credential"); return "", false }
	fake := sandbox.NewFake()
	seen := false
	fake.ExecuteFunc = func(cmd sandbox.Command) (sandbox.Execution, error) {
		if slices.Contains(cmd.Argv, "--version") {
			return sandbox.Execution{}, nil
		}
		seen = true
		if cmd.Env["ESF_CODEX_API_KEY"] != "cube-egress-managed-placeholder" {
			t.Fatalf("ESF_CODEX_API_KEY = %q", cmd.Env["ESF_CODEX_API_KEY"])
		}
		for name, value := range cmd.Env {
			if strings.Contains(value, token) || strings.Contains(value, account) {
				t.Fatalf("env %s holds a credential", name)
			}
		}
		for _, arg := range cmd.Argv {
			if strings.Contains(arg, token) || strings.Contains(arg, account) {
				t.Fatalf("argument holds a credential: %q", arg)
			}
		}
		if !slices.Contains(cmd.Argv, `model_providers.esf.base_url="https://chatgpt.com/backend-api/codex"`) {
			t.Fatalf("argv = %q", cmd.Argv)
		}
		return sandbox.Execution{Stdout: `{"type":"turn.completed"}`}, nil
	}
	sb, _ := fake.Create(ctx, sandbox.Spec{})
	if err := h.ConfigureModelEndpoint(ctx, sb, ModelEndpoint{}); err != nil {
		t.Fatal(err)
	}
	task := Task{Prompt: "fix", RepositoryDir: "/repo", Env: map[string]string{"OPENAI_API_KEY": token, "CHATGPT_ACCOUNT_ID": account}}
	if _, err := h.Run(ctx, sb, task); err != nil {
		t.Fatal(err)
	}
	if !seen {
		t.Fatal("agent not run")
	}
}
