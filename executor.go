package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	qopenai "github.com/steamwo/qoder-proxy/internal/openai"
	"github.com/steamwo/qoder-proxy/internal/protocol"
	"github.com/steamwo/qoder-proxy/internal/qoder"
)

func executorIdentifier() ([]byte, error) {
	return okEnvelope(map[string]string{"identifier": pluginID})
}

func execute(raw []byte) ([]byte, error) {
	var req executorRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, err
	}
	if !loadedConfig().Enabled {
		return errorEnvelope("provider_disabled", "Qoder plugin is disabled", 503), nil
	}
	payload, headers, err := executeNonStream(req)
	if err != nil {
		return errorForExecution(err), nil
	}
	return okEnvelope(executorResponse{Payload: payload, Headers: headers})
}

func executeStream(raw []byte) ([]byte, error) {
	var req executorRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, err
	}
	if strings.TrimSpace(req.StreamID) == "" {
		return errorEnvelope("invalid_request", "stream_id is required", 400), nil
	}
	if !loadedConfig().Enabled {
		return errorEnvelope("provider_disabled", "Qoder plugin is disabled", 503), nil
	}
	go func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				closePluginStream(req.StreamID, fmt.Sprintf("Qoder stream panic: %v", recovered))
			}
		}()
		err := runStream(req)
		if err != nil {
			closePluginStream(req.StreamID, err.Error())
			return
		}
		closePluginStream(req.StreamID, "")
	}()
	return okEnvelope(streamResponse{Headers: http.Header{"Content-Type": []string{"text/event-stream; charset=utf-8"}}})
}

func executeNonStream(req executorRequest) ([]byte, http.Header, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	chatReq, model, client, err := prepareExecution(ctx, req)
	if err != nil {
		return nil, nil, err
	}
	preq, err := qopenai.NormalizeChat(chatReq, model)
	if err != nil {
		return nil, nil, clientError{status: 400, err: err}
	}
	resp, err := client.ChatWithQueue(ctx, preq, nil)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	agg, err := collectQoderEvents(resp)
	if err != nil {
		return nil, nil, err
	}
	result := buildChatCompletion(chatReq.Model, agg)
	body, err := json.Marshal(result)
	if err != nil {
		return nil, nil, err
	}
	return body, http.Header{"Content-Type": []string{"application/json"}}, nil
}

func runStream(req executorRequest) error {
	ctx := context.Background()
	chatReq, model, client, err := prepareExecution(ctx, req)
	if err != nil {
		return err
	}
	chatReq.Stream = true
	preq, err := qopenai.NormalizeChat(chatReq, model)
	if err != nil {
		return clientError{status: 400, err: err}
	}
	onQueue := func(info qoder.QueueInfo) error {
		comment := fmt.Sprintf(": qoder queued queue_type=%s queue_count=%d retry_after=%ds wait_time=%ds\n\n", info.QueueType, info.QueueCount, info.RetryAfterSeconds, info.WaitTime)
		return emitPluginStream(req.StreamID, []byte(comment))
	}
	resp, err := client.ChatWithQueue(ctx, preq, onQueue)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return qoder.RelayChatStream(resp.Body, chatReq.Model, func(data string) error {
		return emitPluginStream(req.StreamID, []byte("data: "+data+"\n\n"))
	})
}

func prepareExecution(ctx context.Context, req executorRequest) (qopenai.ChatRequest, qoder.Model, *qoder.Client, error) {
	format := strings.ToLower(strings.TrimSpace(req.Format))
	if format != "" && format != "chat-completions" && format != "openai" {
		return qopenai.ChatRequest{}, qoder.Model{}, nil, clientError{status: 400, err: fmt.Errorf("unsupported executor format %q", req.Format)}
	}
	cred, err := credentialFromStorage(req.StorageJSON)
	if err != nil {
		return qopenai.ChatRequest{}, qoder.Model{}, nil, clientError{status: 401, err: err}
	}
	var chatReq qopenai.ChatRequest
	if err := json.Unmarshal(req.Payload, &chatReq); err != nil {
		return qopenai.ChatRequest{}, qoder.Model{}, nil, clientError{status: 400, err: fmt.Errorf("decode chat-completions payload: %w", err)}
	}
	if strings.TrimSpace(chatReq.Model) == "" {
		chatReq.Model = req.Model
	}
	if strings.TrimSpace(chatReq.Model) == "" {
		return qopenai.ChatRequest{}, qoder.Model{}, nil, clientError{status: 400, err: fmt.Errorf("model is required")}
	}
	httpClient := newHostHTTPClient(req.HostCallbackID)
	registry := qoder.NewRegistry(httpClient, cred)
	model, err := registry.Resolve(ctx, chatReq.Model)
	if err != nil {
		return qopenai.ChatRequest{}, qoder.Model{}, nil, clientError{status: 404, err: err}
	}
	// CPA owns credential persistence. Avoid qoder.NewClient here because its
	// in-memory AuthState can rotate a refresh token without committing it back
	// to CPA. A 401 is surfaced so CPA can invoke auth.refresh and persist it.
	client := &qoder.Client{HTTP: httpClient, Cred: cred}
	cfg := loadedConfig()
	client.QueueRetry = qoder.QueueRetryPolicy{MaxRetries: cfg.QueueRetries, MaxWait: cfg.QueueMaxWait}
	return chatReq, model, client, nil
}

type aggregate struct {
	Text   strings.Builder
	Tools  map[int]*toolState
	Usage  protocol.Usage
	Finish string
}

type toolState struct {
	ID    string
	Name  string
	Args  string
	Index int
}

func collectQoderEvents(resp *http.Response) (*aggregate, error) {
	a := &aggregate{Tools: map[int]*toolState{}}
	err := qoder.ParseStream(resp.Body, func(ev protocol.Event) error {
		switch ev.Kind {
		case protocol.EventTextDelta:
			a.Text.WriteString(ev.Text)
		case protocol.EventToolDelta:
			t := a.Tools[ev.ToolIndex]
			if t == nil {
				t = &toolState{Index: ev.ToolIndex}
				a.Tools[ev.ToolIndex] = t
			}
			if ev.ToolID != "" {
				t.ID = ev.ToolID
			}
			if ev.ToolName != "" {
				t.Name = ev.ToolName
			}
			t.Args += ev.ToolArguments
		case protocol.EventUsage:
			a.Usage = ev.Usage
		case protocol.EventFinish:
			a.Finish = ev.FinishReason
		case protocol.EventError:
			return fmt.Errorf("%s", ev.Error)
		}
		return nil
	})
	return a, err
}

func buildChatCompletion(model string, a *aggregate) map[string]any {
	message := map[string]any{"role": "assistant", "content": a.Text.String()}
	if len(a.Tools) > 0 {
		message["content"] = nil
		message["tool_calls"] = aggregateToolCalls(a.Tools)
	}
	finish := a.Finish
	if finish == "" {
		if len(a.Tools) > 0 {
			finish = "tool_calls"
		} else {
			finish = "stop"
		}
	}
	return map[string]any{
		"id": "chatcmpl-" + randomHex(12), "object": "chat.completion", "created": time.Now().Unix(), "model": model,
		"choices": []any{map[string]any{"index": 0, "message": message, "finish_reason": finish}},
		"usage": map[string]any{
			"prompt_tokens": a.Usage.InputTokens, "completion_tokens": a.Usage.OutputTokens, "total_tokens": a.Usage.TotalTokens,
			"prompt_tokens_details": map[string]any{"cached_tokens": a.Usage.CachedTokens},
		},
	}
}

func aggregateToolCalls(tools map[int]*toolState) []any {
	keys := make([]int, 0, len(tools))
	for k := range tools {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	out := make([]any, 0, len(keys))
	for _, k := range keys {
		t := tools[k]
		id := t.ID
		if id == "" {
			id = "call_" + randomHex(12)
		}
		out = append(out, map[string]any{"id": id, "type": "function", "function": map[string]any{"name": t.Name, "arguments": t.Args}})
	}
	return out
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

type clientError struct {
	status int
	err    error
}

func (e clientError) Error() string { return e.err.Error() }

func errorForExecution(err error) []byte {
	var ce clientError
	if errors.As(err, &ce) {
		return errorEnvelope("invalid_request", ce.Error(), ce.status)
	}
	var qe *qoder.UpstreamError
	if errors.As(err, &qe) {
		code := qe.PublicCode
		if code == "" {
			code = "qoder_upstream_error"
		}
		return errorEnvelope(code, qe.Message, qe.HTTPStatus)
	}
	msg := err.Error()
	status := 502
	if strings.Contains(msg, "HTTP 401") || strings.Contains(msg, "status 401") {
		status = 401
	}
	if strings.Contains(msg, "HTTP 403") || strings.Contains(msg, "status 403") {
		status = 403
	}
	if strings.Contains(msg, "HTTP 429") || strings.Contains(msg, "status 429") {
		status = 429
	}
	return errorEnvelope("qoder_upstream_error", msg, status)
}

func countTokens(_ []byte) ([]byte, error) {
	return okEnvelope(executorResponse{Payload: []byte(`{"total_tokens":0}`), Headers: http.Header{"Content-Type": []string{"application/json"}}})
}

func executorHTTPRequest(_ []byte) ([]byte, error) {
	return errorEnvelope("not_supported", "executor.http_request is not implemented for Qoder", http.StatusNotImplemented), nil
}
