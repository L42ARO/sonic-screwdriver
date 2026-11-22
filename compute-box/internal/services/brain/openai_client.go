package brain

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// responsesRequest is the subset of POST /v1/responses we use.
type responsesRequest struct {
	Model             string          `json:"model"`
	Input             []any           `json:"input"`
	Tools             []any           `json:"tools"`
	ParallelToolCalls bool            `json:"parallel_tool_calls"`
	Store             bool            `json:"store"`
	Include           []string        `json:"include,omitempty"`
	PromptCacheKey    string          `json:"prompt_cache_key,omitempty"`
	Reasoning         *reasoning      `json:"reasoning,omitempty"`
	MaxOutputTokens   int             `json:"max_output_tokens,omitempty"`
	Text              json.RawMessage `json:"text,omitempty"`
}

type reasoning struct {
	Effort string `json:"effort,omitempty"`
}

type responsesResponse struct {
	ID                string            `json:"id"`
	Status            string            `json:"status"`
	Output            []json.RawMessage `json:"output"`
	Usage             usage             `json:"usage"`
	Error             *apiError         `json:"error"`
	IncompleteDetails *struct {
		Reason string `json:"reason"`
	} `json:"incomplete_details"`
}

type usage struct {
	InputTokens        int `json:"input_tokens"`
	InputTokensDetails struct {
		CachedTokens     int `json:"cached_tokens"`
		CacheWriteTokens int `json:"cache_write_tokens"`
	} `json:"input_tokens_details"`
	OutputTokens        int `json:"output_tokens"`
	OutputTokensDetails struct {
		ReasoningTokens int `json:"reasoning_tokens"`
	} `json:"output_tokens_details"`
}

type apiError struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Code    any    `json:"code"`
}

// outputItem is the part of a response output item we read.
type outputItem struct {
	Type      string `json:"type"`
	CallID    string `json:"call_id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
	Content   []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
}

type openAIClient struct {
	key  string
	http *http.Client
}

// create calls POST /v1/responses, retrying rate limits and server errors.
func (c *openAIClient) create(ctx context.Context, req responsesRequest) (*responsesResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	var lastErr error
	for attempt := 0; attempt < 4; attempt++ {
		if attempt > 0 {
			select {
			case <-time.After(time.Duration(1<<attempt) * time.Second):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.openai.com/v1/responses", bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		httpReq.Header.Set("Authorization", "Bearer "+c.key)
		httpReq.Header.Set("Content-Type", "application/json")
		resp, err := c.http.Do(httpReq)
		if err != nil {
			lastErr = err
			continue
		}
		data, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
			lastErr = fmt.Errorf("openai %d: %s", resp.StatusCode, truncate(string(data), 300))
			continue
		}
		var out responsesResponse
		if err := json.Unmarshal(data, &out); err != nil {
			return nil, fmt.Errorf("openai %d: unreadable response: %s", resp.StatusCode, truncate(string(data), 300))
		}
		if resp.StatusCode >= 400 {
			msg := string(data)
			if out.Error != nil {
				msg = out.Error.Message
			}
			return nil, fmt.Errorf("openai %d: %s", resp.StatusCode, truncate(msg, 500))
		}
		return &out, nil
	}
	return nil, lastErr
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}
