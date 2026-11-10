package factory

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mitkox/esf/internal/agentharness"
	artifacts "github.com/mitkox/esf/internal/factoryartifacts"
	"github.com/mitkox/esf/internal/repository"
	"github.com/mitkox/esf/internal/sandbox"
	"github.com/mitkox/esf/internal/tomlx"
)

// A run of a native CLI records the final message in agent/result.json and
// the usage in run.json. The final message is redacted before it reaches
// workflow history or evidence.
func TestNativeCLIRunRecordsFinalMessageAndUsage(t *testing.T) {
	for _, item := range []struct {
		kind, fixture, message string
		tokensIn, tokensOut    int64
		cost                   *float64
	}{
		{"claude", "claude-2.1.319-stream.jsonl", `"result":"ok"`, 2 + 9750 + 24352, 4, ptr(0.083083)},
		{"codex", "codex-0.167.0-exec.jsonl", `"text":"ok"`, 25480, 5, nil},
	} {
		t.Run(item.kind, func(t *testing.T) {
			ctx := context.Background()
			const secret = "synthetic-secret-in-the-final-message"
			t.Setenv("TEST_NATIVE_KEY", secret)
			recorded, err := os.ReadFile("../agentharness/testdata/" + item.fixture)
			if err != nil {
				t.Fatal(err)
			}
			stdout := strings.Replace(string(recorded), item.message, strings.TrimSuffix(item.message, `"`)+" "+secret+`"`, 1)
			cfg := testValidConfigForCredentialPolicy()
			base := "https://api.openai.com/v1"
			if item.kind == "claude" {
				base = "https://api.anthropic.com"
			}
			cfg.Harnesses[item.kind] = HarnessConfig{Type: item.kind, BaseURL: base, APIKeyEnv: "TEST_NATIVE_KEY", CredentialMode: "cube_egress", Timeout: tomlx.FromStd(time.Minute)}
			reg, err := cfg.BuildHarnesses()
			if err != nil {
				t.Fatal(err)
			}
			fake := sandbox.NewFake()
			sb, _ := fake.Create(ctx, sandbox.Spec{})
			h, _ := reg.Resolve(item.kind)
			if err := h.(agentharness.ModelEndpointConfigurer).ConfigureModelEndpoint(ctx, sb, agentharness.ModelEndpoint{}); err != nil {
				t.Fatal(err)
			}
			fake.ExecuteFunc = func(sandbox.Command) (sandbox.Execution, error) {
				return sandbox.Execution{Stdout: stdout}, nil
			}
			store, err := artifacts.NewLocal(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			acts, err := NewActivities(ActivitiesOptions{Config: cfg, Provider: fake, Harnesses: reg, Repositories: repository.New(nil), ArtifactFactory: store, Redactor: NewRedactor(collectSecrets(cfg, reg)...)})
			if err != nil {
				t.Fatal(err)
			}
			const runID = "native-result"
			out, err := acts.RunAgent(ctx, RunAgentInput{RunID: runID, SandboxID: sb.ID(), Harness: item.kind, Prompt: "fix", RepositoryDir: "/repo", ModelAPIKeyEnv: "TEST_NATIVE_KEY"})
			if err != nil || out.Result.ExitCode != 0 {
				t.Fatalf("result=%+v err=%v", out, err)
			}
			want := "ok " + RedactionMask
			if out.Result.FinalMessage != want {
				t.Fatalf("final message in workflow history = %q", out.Result.FinalMessage)
			}

			data, err := ReadArtifact(store, runID, ArtifactAgentResult)
			if err != nil {
				t.Fatal(err)
			}
			var envelope struct {
				Result struct {
					FinalMessage string `json:"final_message"`
				} `json:"result"`
				Attempt int `json:"attempt"`
			}
			if err := json.Unmarshal(data, &envelope); err != nil {
				t.Fatal(err)
			}
			if envelope.Result.FinalMessage != want || envelope.Attempt != 1 {
				t.Fatalf("agent/result.json = %s", data)
			}

			if _, err := acts.FinalizeResult(ctx, FinalizeInput{Request: RunRequest{RunID: runID}, AgentResult: out.Result}); err != nil {
				t.Fatal(err)
			}
			manifest, err := ReadArtifact(store, runID, ArtifactRun)
			if err != nil {
				t.Fatal(err)
			}
			var usage struct {
				TokensIn      *int64   `json:"tokens_in"`
				TokensOut     *int64   `json:"tokens_out"`
				InferenceCost *float64 `json:"inference_cost"`
			}
			if err := json.Unmarshal(manifest, &usage); err != nil {
				t.Fatal(err)
			}
			if usage.TokensIn == nil || *usage.TokensIn != item.tokensIn || usage.TokensOut == nil || *usage.TokensOut != item.tokensOut {
				t.Fatalf("run.json usage = %+v", usage)
			}
			if (item.cost == nil) != (usage.InferenceCost == nil) || (item.cost != nil && *item.cost != *usage.InferenceCost) {
				t.Fatalf("run.json inference_cost = %v", usage.InferenceCost)
			}
		})
	}
}

func ptr[T any](v T) *T { return &v }
