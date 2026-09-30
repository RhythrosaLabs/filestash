package plg_widget_ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// minimal client for the OpenAI compatible chat completion API which is what
// Ollama, DeepSeek, OpenRouter, LM Studio, vLLM, ... all expose

type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

type ToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type ToolDef struct {
	Type     string       `json:"type"`
	Function ToolFunction `json:"function"`
}

type ToolFunction struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

type LLM struct {
	BaseURL string
	Model   string
	APIKey  string
	Client  *http.Client
}

var httpClient = &http.Client{Timeout: 5 * time.Minute}

func (this LLM) Chat(ctx context.Context, messages []Message, tools []ToolDef) (Message, error) {
	body, _ := json.Marshal(map[string]any{
		"model":       this.Model,
		"messages":    messages,
		"tools":       tools,
		"temperature": 0.2,
		"stream":      false,
	})
	req, err := http.NewRequestWithContext(ctx, "POST", strings.TrimSuffix(this.BaseURL, "/")+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return Message{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	if this.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+this.APIKey)
	}
	client := this.Client
	if client == nil {
		client = httpClient
	}
	res, err := client.Do(req)
	if err != nil {
		return Message{}, fmt.Errorf("cannot reach the model at %s: %w", this.BaseURL, err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(res.Body, 10<<20))
	if res.StatusCode != http.StatusOK {
		return Message{}, fmt.Errorf("model returned %d: %s", res.StatusCode, truncate(string(b), 300))
	}
	var out struct {
		Choices []struct {
			Message Message `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(b, &out); err != nil || len(out.Choices) == 0 {
		return Message{}, fmt.Errorf("invalid model response: %s", truncate(string(b), 300))
	}
	msg := out.Choices[0].Message
	msg.Role = "assistant"
	msg.Content = stripThinking(msg.Content)
	return msg, nil
}

// reasoning models (qwen3, deepseek-r1) may leak their chain of thought in the content
func stripThinking(s string) string {
	for {
		start := strings.Index(s, "<think>")
		if start == -1 {
			return strings.TrimSpace(s)
		}
		end := strings.Index(s, "</think>")
		if end == -1 || end < start {
			return strings.TrimSpace(s[:start])
		}
		s = s[:start] + s[end+len("</think>"):]
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "... [truncated]"
}
