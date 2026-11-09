package agentharness

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/mitkox/esf/internal/sandbox"
)

func TestClaudeSubscriptionTokenUsesOAuthPlaceholder(t *testing.T) {
	ctx := context.Background()
	h, err := NewClaude(NativeCLIOptions{EgressManaged: true, SubscriptionToken: true, Timeout: time.Minute})
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
		if cmd.Env["CLAUDE_CODE_OAUTH_TOKEN"] != "cube-egress-managed-placeholder" {
			t.Fatalf("oauth env = %q", cmd.Env["CLAUDE_CODE_OAUTH_TOKEN"])
		}
		if _, ok := cmd.Env["ANTHROPIC_API_KEY"]; ok {
			t.Fatal("ANTHROPIC_API_KEY set with a subscription token")
		}
		return sandbox.Execution{Stdout: `{"type":"result","subtype":"success","is_error":false}`}, nil
	}
	sb, _ := fake.Create(ctx, sandbox.Spec{})
	if err := h.ConfigureModelEndpoint(ctx, sb, ModelEndpoint{}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Run(ctx, sb, Task{Prompt: "fix", RepositoryDir: "/repo"}); err != nil {
		t.Fatal(err)
	}
	if !seen {
		t.Fatal("agent not run")
	}
}

func TestSubscriptionTokenRequiresEgress(t *testing.T) {
	if _, err := NewClaude(NativeCLIOptions{SubscriptionToken: true, Timeout: time.Minute}); err == nil {
		t.Fatal("accepted a subscription token without egress management")
	}
	if _, err := NewCodex(NativeCLIOptions{SubscriptionToken: true, Timeout: time.Minute}); err == nil {
		t.Fatal("accepted a codex subscription token without egress management")
	}
}
