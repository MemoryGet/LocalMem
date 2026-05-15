package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// ModelInfoProvider is an optional interface for LLM providers that can expose
// their connection info, enabling API-based and pattern-based context window detection.
type ModelInfoProvider interface {
	ProviderBaseURL() string
	ProviderModel() string
}

// DetectContextWindow returns the recommended batch token threshold for the given provider.
//
// Detection cascade (stops at first hit):
//
//	Layer 1: EXTRACT_BATCH_THRESHOLD env override — instant, zero network
//	Layer 2: GET /v1/models API → context_window field — zero inference tokens
//	Layer 3: model name pattern table — zero network, offline
//	Layer 4: ask model ("how many tokens is your context window?") — one tiny chat call
//	Layer 5: conservative default 4000
//
// The returned value is batchRatio (60%) of the detected context window, leaving
// headroom for prompt templates and system messages.
func DetectContextWindow(ctx context.Context, provider Provider) int {
	const (
		defaultContextWindow = 12000 // 保守兜底上下文窗口 / Conservative fallback context window
		batchRatio           = 0.60  // batch content 占上下文窗口比例 / Fraction of context window for batch content
		apiProbeTimeout      = 5 * time.Second
		askModelTimeout      = 15 * time.Second
	)

	// Layer 1: env override — highest priority, skips all detection.
	if v := os.Getenv("EXTRACT_BATCH_THRESHOLD"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			fmt.Printf("  DetectContextWindow: EXTRACT_BATCH_THRESHOLD=%d (env override)\n", n)
			return n
		}
	}

	var baseURL, modelName string
	if info, ok := provider.(ModelInfoProvider); ok {
		baseURL = info.ProviderBaseURL()
		modelName = info.ProviderModel()
	}

	// Layer 2: query /v1/models API for context_window metadata.
	if baseURL != "" {
		if cw := queryContextWindowFromAPI(ctx, baseURL, modelName, apiProbeTimeout); cw > 0 {
			t := int(float64(cw) * batchRatio)
			fmt.Printf("  DetectContextWindow: API probe context=%d → threshold=%d (%.0f%%)\n", cw, t, batchRatio*100)
			return t
		}
	}

	// Layer 3: model name pattern table — offline lookup.
	if modelName != "" {
		if cw := lookupModelContextWindow(modelName); cw > 0 {
			t := int(float64(cw) * batchRatio)
			fmt.Printf("  DetectContextWindow: name pattern [%s] context=%d → threshold=%d (%.0f%%)\n", modelName, cw, t, batchRatio*100)
			return t
		}
	}

	// Layer 4: ask the model (one minimal chat request).
	qctx, cancel := context.WithTimeout(ctx, askModelTimeout)
	defer cancel()
	resp, err := provider.Chat(qctx, &ChatRequest{
		Messages: []ChatMessage{
			{Role: "system", Content: "You must respond with ONLY a single number. No words, no units, just digits."},
			{Role: "user", Content: "How many tokens can you process in a single request (your context window size)?"},
		},
		MaxTokens: 20,
	})
	if err == nil {
		if cw := parseTokenCount(resp.Content); cw > 0 {
			t := int(float64(cw) * batchRatio)
			fmt.Printf("  DetectContextWindow: model reply context=%d → threshold=%d (%.0f%%)\n", cw, t, batchRatio*100)
			return t
		}
		fmt.Printf("  DetectContextWindow: unparseable reply %q, using default\n", resp.Content)
	} else {
		fmt.Printf("  DetectContextWindow: ask model failed (%v), using default\n", err)
	}

	// Layer 5: conservative default.
	t := int(float64(defaultContextWindow) * batchRatio)
	fmt.Printf("  DetectContextWindow: default context=%d → threshold=%d\n", defaultContextWindow, t)
	return t
}

// queryContextWindowFromAPI queries the provider's /models endpoint for the model's
// context window size. Returns 0 if unavailable or the API doesn't expose the field.
func queryContextWindowFromAPI(ctx context.Context, baseURL, modelName string, timeout time.Duration) int {
	apiURL := strings.TrimRight(baseURL, "/") + "/models"
	qctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(qctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return 0
	}

	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		return 0
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return 0
	}

	// Parse OpenAI-compatible model list response.
	// Different APIs use different field names for context window size.
	var result struct {
		Data []struct {
			ID               string `json:"id"`
			ContextWindow    int    `json:"context_window"`
			ContextLength    int    `json:"context_length"`
			MaxContextLength int    `json:"max_context_length"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return 0
	}

	lowerModel := strings.ToLower(modelName)
	for _, m := range result.Data {
		// Match by exact ID or by model name prefix (handles "qwen3:8b" vs "qwen3-8b" variants).
		mID := strings.ToLower(m.ID)
		if mID == lowerModel || strings.HasPrefix(mID, lowerModel) || strings.HasPrefix(lowerModel, mID) {
			if m.ContextWindow > 0 {
				return m.ContextWindow
			}
			if m.ContextLength > 0 {
				return m.ContextLength
			}
			if m.MaxContextLength > 0 {
				return m.MaxContextLength
			}
		}
	}
	return 0
}

// modelContextTable maps lowercase model name substrings to known context window sizes.
// More specific patterns MUST appear before less specific ones (first match wins).
var modelContextTable = []struct {
	pattern string
	tokens  int
}{
	// Qwen3 series
	{"qwen3-235b", 131072},
	{"qwen3-32b", 131072},
	{"qwen3-14b", 131072},
	{"qwen3-8b", 32768},
	{"qwen3", 32768},
	// Qwen2.5 series
	{"qwen2.5-72b", 131072},
	{"qwen2.5-32b", 131072},
	{"qwen2.5", 32768},
	// DeepSeek series
	{"deepseek-v3", 65536},
	{"deepseek-r1-671b", 65536},
	{"deepseek-r1", 65536},
	{"deepseek", 32768},
	// OpenAI
	{"gpt-4o-mini", 128000},
	{"gpt-4o", 128000},
	{"gpt-4-turbo", 128000},
	{"gpt-4", 8192},
	{"gpt-3.5-turbo-16k", 16384},
	{"gpt-3.5", 4096},
	// Anthropic Claude
	{"claude-3-5-sonnet", 200000},
	{"claude-3-7", 200000},
	{"claude-3", 200000},
	{"claude", 100000},
	// Llama series
	{"llama-3.3", 131072},
	{"llama-3.2", 131072},
	{"llama-3.1", 131072},
	{"llama3.3", 131072},
	{"llama3.2", 131072},
	{"llama3.1", 131072},
	{"llama3", 8192},
	{"llama2", 4096},
	// Gemma
	{"gemma-2", 8192},
	{"gemma2", 8192},
	{"gemma", 8192},
	// Mistral / Mixtral
	{"mistral-large", 131072},
	{"mistral-7b", 32768},
	{"mixtral", 32768},
	{"mistral", 32768},
	// Phi
	{"phi-4", 16384},
	{"phi-3", 4096},
	{"phi3", 4096},
	// Gemini
	{"gemini-1.5-pro", 2097152},
	{"gemini-1.5-flash", 1048576},
	{"gemini-pro", 32768},
	{"gemini", 32768},
}

// lookupModelContextWindow returns the known context window for a model by name pattern.
// Returns 0 if no pattern matches.
func lookupModelContextWindow(modelName string) int {
	lower := strings.ToLower(modelName)
	for _, entry := range modelContextTable {
		if strings.Contains(lower, entry.pattern) {
			return entry.tokens
		}
	}
	return 0
}

// parseTokenCount parses flexible token count formats: "128k", "128K", "128,000", "128000".
// Returns 0 if the string cannot be parsed as a positive integer.
func parseTokenCount(s string) int {
	raw := strings.ToLower(strings.TrimSpace(s))
	raw = strings.ReplaceAll(raw, ",", "")
	raw = strings.ReplaceAll(raw, "_", "")
	raw = strings.ReplaceAll(raw, " ", "")
	multiplier := 1
	if strings.HasSuffix(raw, "k") {
		multiplier = 1000
		raw = strings.TrimSuffix(raw, "k")
	} else if strings.HasSuffix(raw, "m") {
		multiplier = 1_000_000
		raw = strings.TrimSuffix(raw, "m")
	}
	// Trim to first digit-only run to tolerate extra model output.
	for i, c := range raw {
		if c < '0' || c > '9' {
			raw = raw[:i]
			break
		}
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return 0
	}
	return n * multiplier
}
