package agentharness

import (
	"context"
	"os"
	"slices"
	"testing"

	"github.com/mitkox/esf/internal/sandbox"
)

// runNativeCLIWithStdout runs the harness against a fake sandbox whose agent
// command prints stdout and exits zero.
func runNativeCLIWithStdout(t *testing.T, kind, stdout string) Result {
	t.Helper()
	ctx := context.Background()
	h, err := newNativeCLI(kind, NativeCLIOptions{EgressManaged: true})
	if err != nil {
		t.Fatal(err)
	}
	fake := sandbox.NewFake()
	fake.ExecuteFunc = func(cmd sandbox.Command) (sandbox.Execution, error) {
		if slices.Contains(cmd.Argv, "--version") {
			return sandbox.Execution{}, nil
		}
		return sandbox.Execution{Stdout: stdout}, nil
	}
	sb, _ := fake.Create(ctx, sandbox.Spec{})
	if err := h.ConfigureModelEndpoint(ctx, sb, ModelEndpoint{}); err != nil {
		t.Fatal(err)
	}
	result, err := h.Run(ctx, sb, Task{Prompt: "fix", RepositoryDir: "/repo"})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func readFixture(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func assertCount(t *testing.T, name string, got *int64, want int64) {
	t.Helper()
	if got == nil || *got != want {
		t.Fatalf("%s = %v, want %d", name, got, want)
	}
}

func TestClaudeReportsFinalMessageAndUsage(t *testing.T) {
	// Recorded with Claude Code 2.1.319 (`claude -p --verbose --output-format
	// stream-json`). The terminal event reports input 2, cache creation 9750,
	// cache read 24352, output 4 and total_cost_usd 0.083083.
	result := runNativeCLIWithStdout(t, "claude", readFixture(t, "claude-2.1.319-stream.jsonl"))
	if !result.Succeeded() {
		t.Fatalf("recorded success stream failed: %+v", result)
	}
	if result.FinalMessage != "ok" {
		t.Fatalf("final message = %q", result.FinalMessage)
	}
	assertCount(t, "tokens in", result.TokensIn, 2+9750+24352)
	assertCount(t, "tokens out", result.TokensOut, 4)
	if result.CostUSD == nil || *result.CostUSD != 0.083083 {
		t.Fatalf("cost = %v", result.CostUSD)
	}
}

func TestCodexReportsFinalMessageAndUsage(t *testing.T) {
	// Recorded with Codex CLI 0.167.0 (`codex exec --json`). The final
	// agent_message is "ok". turn.completed reports input_tokens 25480, of
	// which cached_input_tokens 12288 are a part, and output_tokens 5.
	result := runNativeCLIWithStdout(t, "codex", readFixture(t, "codex-0.167.0-exec.jsonl"))
	if !result.Succeeded() {
		t.Fatalf("recorded success stream failed: %+v", result)
	}
	if result.FinalMessage != "ok" {
		t.Fatalf("final message = %q", result.FinalMessage)
	}
	assertCount(t, "tokens in", result.TokensIn, 25480)
	assertCount(t, "tokens out", result.TokensOut, 5)
	if result.CostUSD != nil {
		t.Fatalf("codex reports no cost, got %v", *result.CostUSD)
	}
}

func TestNativeCLIMissingOrBadUsageKeepsTheSuccessDecision(t *testing.T) {
	for _, item := range []struct {
		name, kind, stdout string
		success            bool
	}{
		{"claude without usage", "claude", `{"type":"result","subtype":"success","is_error":false}`, true},
		{"claude with bad usage", "claude", `{"type":"result","subtype":"success","is_error":false,"result":7,"usage":"none","total_cost_usd":"free"}`, true},
		{"claude error with bad usage", "claude", `{"type":"result","subtype":"error_max_turns","is_error":true,"usage":[1]}`, false},
		{"codex without usage", "codex", `{"type":"turn.completed"}`, true},
		{"codex with bad usage", "codex", "{\"type\":\"item.completed\",\"item\":\"bad\"}\n{\"type\":\"turn.completed\",\"usage\":{\"input_tokens\":\"many\"}}", true},
		{"claude with negative usage", "claude", `{"type":"result","subtype":"success","is_error":false,"usage":{"input_tokens":-1,"output_tokens":-1},"total_cost_usd":-0.5}`, true},
		{"codex with negative usage", "codex", `{"type":"turn.completed","usage":{"input_tokens":-1,"output_tokens":-1}}`, true},
		{"codex failed turn", "codex", "{\"type\":\"turn.completed\",\"usage\":{\"input_tokens\":3}}\n{\"type\":\"turn.failed\"}", false},
	} {
		t.Run(item.name, func(t *testing.T) {
			result := runNativeCLIWithStdout(t, item.kind, item.stdout)
			if result.Succeeded() != item.success {
				t.Fatalf("success = %v, want %v", result.Succeeded(), item.success)
			}
			if item.success && (result.TokensIn != nil || result.TokensOut != nil || result.CostUSD != nil || result.FinalMessage != "") {
				t.Fatalf("invented a value: %+v", result)
			}
		})
	}
}

func TestCodexSumsTheUsageOfEveryTurn(t *testing.T) {
	// Codex 0.167.0 reports the usage of each turn, not a running total.
	stdout := `{"type":"item.completed","item":{"id":"item_0","type":"agent_message","text":"first"}}
{"type":"turn.completed","usage":{"input_tokens":100,"cached_input_tokens":40,"output_tokens":10}}
{"type":"item.completed","item":{"id":"item_1","type":"agent_message","text":"second"}}
{"type":"turn.completed","usage":{"input_tokens":30,"cached_input_tokens":20,"output_tokens":3}}`
	result := runNativeCLIWithStdout(t, "codex", stdout)
	if !result.Succeeded() || result.FinalMessage != "second" {
		t.Fatalf("result = %+v", result)
	}
	assertCount(t, "tokens in", result.TokensIn, 130)
	assertCount(t, "tokens out", result.TokensOut, 13)
}
