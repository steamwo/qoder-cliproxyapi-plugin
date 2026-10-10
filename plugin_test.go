package main

import (
	"testing"
	"time"

	"github.com/steamwo/qoder-proxy/internal/credential"
	"github.com/steamwo/qoder-proxy/internal/protocol"
)

func TestRegistrationDeclaresProviderSurface(t *testing.T) {
	r := pluginRegistration()
	if r.SchemaVersion != schemaVersion || !r.Capabilities.AuthProvider || !r.Capabilities.ModelProvider || !r.Capabilities.Executor {
		t.Fatalf("unexpected registration: %#v", r)
	}
	if len(r.Capabilities.ExecutorInputFormats) != 1 || r.Capabilities.ExecutorInputFormats[0] != "chat-completions" {
		t.Fatalf("unexpected input formats: %#v", r.Capabilities.ExecutorInputFormats)
	}
}

func TestAuthDataCarriesRefreshMetadata(t *testing.T) {
	cred := credential.Credential{
		Token: "access", RefreshToken: "refresh", UserID: "u-1", MachineID: "m-1",
		Email: "user@example.com", ExpiresAt: time.Now().Add(3 * time.Hour).Unix(),
	}
	a := authDataFromCredential(cred, "", "")
	if a.Provider != pluginID || a.ID == "" || a.FileName == "" {
		t.Fatalf("unexpected auth data: %#v", a)
	}
	if got, _ := a.Metadata["refresh_token"].(string); got != "refresh" {
		t.Fatalf("refresh token metadata = %q", got)
	}
	if a.NextRefreshAfter.IsZero() {
		t.Fatal("next refresh was not scheduled")
	}
}

func TestBuildChatCompletionWithToolCall(t *testing.T) {
	a := &aggregate{
		Tools: map[int]*toolState{0: {ID: "call_1", Name: "lookup", Args: `{"q":"x"}`}},
		Usage: protocol.Usage{InputTokens: 10, OutputTokens: 5, TotalTokens: 15},
	}
	resp := buildChatCompletion("Model", a)
	choices, ok := resp["choices"].([]any)
	if !ok || len(choices) != 1 {
		t.Fatalf("choices = %#v", resp["choices"])
	}
	choice := choices[0].(map[string]any)
	if choice["finish_reason"] != "tool_calls" {
		t.Fatalf("finish_reason = %#v", choice["finish_reason"])
	}
}
