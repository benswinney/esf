package agentharness

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/mitkox/esf/internal/sandbox"
)

const (
	DefaultClaudeBinary = "/opt/esf/agents/claude"
	DefaultCodexBinary  = "/opt/esf/agents/codex"
)

// NativeCLIOptions describes an operator-installed standalone CLI. Node/npm
// launchers must be installed, with their dependencies, in a pinned template.
type NativeCLIOptions struct {
	Name          string
	Binary        string
	BinarySHA256  string
	Preinstalled  bool
	Model         string
	BaseURL       string
	APIKeyEnv     string
	EgressManaged bool
	// SubscriptionToken selects a subscription token in place of an API key: a
	// Claude token from `claude setup-token`, or a ChatGPT access token for
	// codex. It requires EgressManaged.
	SubscriptionToken bool
	Timeout           time.Duration
	Packages          []string
}

// NativeCLIHarness keeps per-run endpoint configuration in the sandbox rather
// than mutable worker state, so concurrent runs can use different endpoints.
type NativeCLIHarness struct {
	*GenericCommandHarness
	kind              string
	endpoint          ModelEndpoint
	egressManaged     bool
	subscriptionToken bool
	lookupEnv         func(string) (string, bool)
}

func NewClaude(opts NativeCLIOptions) (*NativeCLIHarness, error) {
	return newNativeCLI("claude", opts)
}

func NewCodex(opts NativeCLIOptions) (*NativeCLIHarness, error) {
	return newNativeCLI("codex", opts)
}

func newNativeCLI(kind string, opts NativeCLIOptions) (*NativeCLIHarness, error) {
	if opts.SubscriptionToken && !opts.EgressManaged {
		return nil, fmt.Errorf("%s: a subscription token requires cube_egress", kind)
	}
	if opts.Name == "" {
		opts.Name = kind
	}
	if opts.Binary == "" {
		if kind == "claude" {
			opts.Binary = DefaultClaudeBinary
		} else {
			opts.Binary = DefaultCodexBinary
		}
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 30 * time.Minute
	}
	provider, baseURL, keyEnv := "openai", "https://api.openai.com/v1", "OPENAI_API_KEY"
	if kind == "claude" {
		provider, baseURL, keyEnv = "anthropic", "https://api.anthropic.com", "ANTHROPIC_API_KEY"
	}
	if opts.BaseURL != "" {
		baseURL = strings.TrimSpace(opts.BaseURL)
	}
	if opts.APIKeyEnv != "" {
		keyEnv = strings.TrimSpace(opts.APIKeyEnv)
	}
	h := &NativeCLIHarness{
		kind: kind, egressManaged: opts.EgressManaged, subscriptionToken: opts.SubscriptionToken, lookupEnv: os.LookupEnv,
		endpoint: ModelEndpoint{Provider: provider, BaseURL: baseURL, APIKeyEnv: keyEnv},
	}
	if err := h.ValidateModelEndpoint(h.endpoint); err != nil {
		return nil, err
	}
	if err := validateNativeCLIModel(opts.Model); err != nil {
		return nil, err
	}
	data, _ := json.Marshal(h.endpoint)
	files := map[string]string{h.endpointPath(): string(data)}
	if kind == "codex" {
		// CODEX_HOME must exist before the CLI starts. An empty factory-owned
		// config also keeps worker-host configuration out of the sandbox.
		files["/workspace/.factory/codex/config.toml"] = ""
	}
	executable, source := "/usr/local/bin/"+kind, opts.Binary
	if opts.Preinstalled {
		executable, source = opts.Binary, ""
	}
	generic, err := NewGeneric(Spec{
		Name: opts.Name, Executable: executable, Model: opts.Model,
		ModelFlag: "--model", Timeout: opts.Timeout, PromptMode: PromptStdinFile,
		Provision: Provision{
			BinarySource: source, BinaryDest: executable, BinarySHA256: opts.BinarySHA256,
			Preinstalled: opts.Preinstalled, Packages: opts.Packages,
			Files:      files,
			VerifyArgs: []string{executable, "--version"},
		},
	})
	if err != nil {
		return nil, err
	}
	generic.validateModel = validateNativeCLIModel
	h.GenericCommandHarness = generic
	return h, nil
}

func validateNativeCLIModel(model string) error {
	if strings.ContainsAny(model, "\x00\r\n") || strings.HasPrefix(model, "-") {
		return fmt.Errorf("agentharness: invalid native CLI model %q", model)
	}
	return nil
}

func (h *NativeCLIHarness) endpointPath() string {
	return "/workspace/.factory/" + h.kind + "-endpoint.json"
}

func (h *NativeCLIHarness) ValidateModelEndpoint(endpoint ModelEndpoint) error {
	if endpoint.Provider != "" && !strings.EqualFold(endpoint.Provider, h.endpoint.Provider) {
		return fmt.Errorf("agentharness: %s requires a %s-compatible endpoint", h.kind, h.endpoint.Provider)
	}
	if endpoint.BaseURL != "" {
		u, err := url.Parse(endpoint.BaseURL)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return fmt.Errorf("agentharness: %s base_url must be HTTP(S) without credentials, query, or fragment", h.kind)
		}
		if h.egressManaged && endpoint.BaseURL != h.endpoint.BaseURL {
			return fmt.Errorf("agentharness: %s resolved model endpoint must match the cube_egress base_url", h.kind)
		}
		if h.egressManaged && u.Scheme != "https" {
			return fmt.Errorf("agentharness: %s cube_egress requires HTTPS", h.kind)
		}
	}
	if endpoint.APIKeyEnv != "" {
		if err := ValidateEnvironmentName(endpoint.APIKeyEnv); err != nil {
			return err
		}
		if h.egressManaged && endpoint.APIKeyEnv != h.endpoint.APIKeyEnv {
			return fmt.Errorf("agentharness: %s resolved credential must match the cube_egress credential", h.kind)
		}
	}
	return validateNativeCLIModel(endpoint.Model)
}

func (h *NativeCLIHarness) ConfigureModelEndpoint(ctx context.Context, sb sandbox.Sandbox, endpoint ModelEndpoint) error {
	if err := h.ValidateModelEndpoint(endpoint); err != nil {
		return err
	}
	resolved := h.endpoint
	if endpoint.BaseURL != "" {
		resolved.BaseURL = endpoint.BaseURL
	}
	if endpoint.APIKeyEnv != "" {
		resolved.APIKeyEnv = endpoint.APIKeyEnv
	}
	data, _ := json.Marshal(resolved)
	return sb.WriteFile(ctx, h.endpointPath(), data)
}

func (h *NativeCLIHarness) Run(ctx context.Context, sb sandbox.Sandbox, task Task) (Result, error) {
	data, err := sb.ReadFile(ctx, h.endpointPath())
	if err != nil {
		return Result{}, fmt.Errorf("%s: read model endpoint: %w", h.Name(), err)
	}
	var endpoint ModelEndpoint
	if err := json.Unmarshal(data, &endpoint); err != nil {
		return Result{}, fmt.Errorf("%s: decode model endpoint: %w", h.Name(), err)
	}
	if endpoint.BaseURL == "" || endpoint.APIKeyEnv == "" {
		return Result{}, fmt.Errorf("%s: incomplete model endpoint", h.Name())
	}
	if err := h.ValidateModelEndpoint(endpoint); err != nil {
		return Result{}, err
	}
	if err := validateNativeCLIModel(task.Model); err != nil {
		return Result{}, err
	}
	key := "cube-egress-managed-placeholder"
	if !h.egressManaged {
		key = task.Env[endpoint.APIKeyEnv]
		if key == "" {
			key, _ = h.lookupEnv(endpoint.APIKeyEnv)
		}
		if key == "" {
			return Result{}, fmt.Errorf("%s: credential environment variable %s is empty", h.Name(), endpoint.APIKeyEnv)
		}
	}
	// Copy the spec and environment for this invocation; shared harness state
	// never changes. Only the configured credential enters the CLI environment.
	run := *h.GenericCommandHarness
	run.spec.Env = map[string]string{}
	if !h.egressManaged {
		for name, value := range task.Env {
			run.spec.Env[name] = value
		}
	}
	delete(run.spec.Env, endpoint.APIKeyEnv)
	task.Env = nil
	if h.kind == "claude" {
		// Cube provides the isolation boundary, including on root-based
		// development templates. Claude otherwise refuses permission bypass.
		run.spec.Env["IS_SANDBOX"] = "1"
		if h.subscriptionToken {
			run.spec.Env["CLAUDE_CODE_OAUTH_TOKEN"] = key
		} else {
			run.spec.Env["ANTHROPIC_API_KEY"] = key
		}
		run.spec.Env["ANTHROPIC_BASE_URL"] = endpoint.BaseURL
		run.spec.Args = []string{
			"--print", ModelArgsPlaceholder, "--verbose", "--output-format", "stream-json",
			"--dangerously-skip-permissions", "--setting-sources", "",
			"--strict-mcp-config", "--mcp-config", `{"mcpServers":{}}`,
		}
	} else {
		run.spec.Env["CODEX_HOME"] = "/workspace/.factory/codex"
		run.spec.Env["ESF_CODEX_API_KEY"] = key
		quote := func(s string) string { b, _ := json.Marshal(s); return string(b) }
		run.spec.Args = []string{
			"exec", ModelArgsPlaceholder, "--json", "--dangerously-bypass-approvals-and-sandbox",
			"-c", `model_provider="esf"`,
			"-c", `model_providers.esf.name="ESF"`,
			"-c", "model_providers.esf.base_url=" + quote(endpoint.BaseURL),
			"-c", `model_providers.esf.env_key="ESF_CODEX_API_KEY"`,
			"-c", `model_providers.esf.wire_api="responses"`,
			"-c", "model_providers.esf.requires_openai_auth=false", "-",
		}
	}
	result, err := run.Run(ctx, sb, task)
	if err != nil {
		return result, err
	}
	stream := scanNativeCLIStream(h.kind, result.Stdout)
	result.FinalMessage = stream.finalMessage
	result.TokensIn, result.TokensOut, result.CostUSD = stream.tokensIn, stream.tokensOut, stream.costUSD
	if result.ExitCode == 0 && !result.TimedOut && !stream.success {
		// A CLI can report an API/turn failure in JSON and still exit zero.
		// Missing terminal output (including truncated output) fails closed.
		result.ExitCode = 1
	}
	return result, nil
}

// nativeCLIStream is what the factory keeps from the JSON event stream of a
// native CLI: the success decision and, when the stream reports them, the
// final message and the usage of the agent.
type nativeCLIStream struct {
	success      bool
	finalMessage string
	tokensIn     *int64
	tokensOut    *int64
	costUSD      *float64
}

// scanNativeCLIStream reads the stream on stdout once. The success decision
// uses the event type and status only. The final message and the usage are
// decoded separately, so a missing or malformed value never changes the
// decision.
func scanNativeCLIStream(kind, output string) nativeCLIStream {
	var stream nativeCLIStream
	success := false
	scanner := bufio.NewScanner(strings.NewReader(output))
	scanner.Buffer(make([]byte, 4096), 1<<20)
	for scanner.Scan() {
		var event struct {
			Type         string          `json:"type"`
			Subtype      string          `json:"subtype"`
			IsError      bool            `json:"is_error"`
			Result       json.RawMessage `json:"result"`
			Usage        json.RawMessage `json:"usage"`
			TotalCostUSD json.RawMessage `json:"total_cost_usd"`
			Item         json.RawMessage `json:"item"`
		}
		if json.Unmarshal(scanner.Bytes(), &event) != nil {
			return stream
		}
		if kind == "claude" && event.Type == "result" {
			stream.recordClaudeResult(event.Result, event.Usage, event.TotalCostUSD)
			if event.IsError || event.Subtype != "success" {
				return stream
			}
			success = true
		}
		if kind == "codex" {
			switch event.Type {
			case "item.completed":
				stream.recordCodexItem(event.Item)
			case "turn.completed":
				stream.recordCodexUsage(event.Usage)
				success = true
			case "turn.failed", "error":
				return stream
			}
		}
	}
	stream.success = scanner.Err() == nil && success
	return stream
}

// recordClaudeResult keeps the final message and the usage of a Claude
// "result" event. A later result event replaces an earlier one.
func (s *nativeCLIStream) recordClaudeResult(result, usage, cost json.RawMessage) {
	s.finalMessage = ""
	_ = json.Unmarshal(result, &s.finalMessage)
	s.tokensIn, s.tokensOut, s.costUSD = nil, nil, nil
	var u struct {
		Input         *int64 `json:"input_tokens"`
		CacheCreation *int64 `json:"cache_creation_input_tokens"`
		CacheRead     *int64 `json:"cache_read_input_tokens"`
		Output        *int64 `json:"output_tokens"`
	}
	if json.Unmarshal(usage, &u) == nil {
		if u.Input != nil {
			// Every input token counts, including cache writes and cache reads.
			total := *u.Input
			for _, part := range []*int64{u.CacheCreation, u.CacheRead} {
				if part != nil {
					total += *part
				}
			}
			s.tokensIn = &total
		}
		s.tokensOut = u.Output
	}
	var usd float64
	if json.Unmarshal(cost, &usd) == nil && len(cost) > 0 && string(cost) != "null" {
		s.costUSD = &usd
	}
}

// recordCodexItem keeps the text of a completed Codex agent_message item. The
// last one is the final message.
func (s *nativeCLIStream) recordCodexItem(item json.RawMessage) {
	var i struct {
		Type string  `json:"type"`
		Text *string `json:"text"`
	}
	if json.Unmarshal(item, &i) == nil && i.Type == "agent_message" && i.Text != nil {
		s.finalMessage = *i.Text
	}
}

// recordCodexUsage keeps the usage of a Codex turn.completed event. Codex
// counts cached input inside input_tokens, so input_tokens is already every
// input token. Codex reports no cost.
func (s *nativeCLIStream) recordCodexUsage(usage json.RawMessage) {
	s.tokensIn, s.tokensOut = nil, nil
	var u struct {
		Input  *int64 `json:"input_tokens"`
		Output *int64 `json:"output_tokens"`
	}
	if json.Unmarshal(usage, &u) == nil {
		s.tokensIn, s.tokensOut = u.Input, u.Output
	}
}

var _ Harness = (*NativeCLIHarness)(nil)
var _ ModelEndpointConfigurer = (*NativeCLIHarness)(nil)
var _ ModelEndpointValidator = (*NativeCLIHarness)(nil)
