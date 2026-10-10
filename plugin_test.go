package main

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/steamwo/qoder-proxy/internal/credential"
	"github.com/steamwo/qoder-proxy/internal/protocol"
)

func TestRegistrationDeclaresProviderSurface(t *testing.T) {
	r := pluginRegistration()
	if r.SchemaVersion != schemaVersion || !r.Capabilities.AuthProvider || !r.Capabilities.ModelProvider || !r.Capabilities.Executor || !r.Capabilities.ManagementAPI {
		t.Fatalf("unexpected registration: %#v", r)
	}
	if r.Capabilities.QuotaProvider {
		t.Fatal("generic quota provider must stay disabled; Qoder quota is exposed through the plugin management panel")
	}
	if len(r.Capabilities.ExecutorInputFormats) != 1 || r.Capabilities.ExecutorInputFormats[0] != "chat-completions" {
		t.Fatalf("unexpected input formats: %#v", r.Capabilities.ExecutorInputFormats)
	}
	if r.Metadata.Logo != qoderOfficialLogoURL {
		t.Fatalf("unexpected Qoder logo: %q", r.Metadata.Logo)
	}
}

func TestLoginStateIsCPACompatible(t *testing.T) {
	raw, err := startLogin([]byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatal(err)
	}
	if !env.OK {
		t.Fatalf("start login failed: %#v", env.Error)
	}
	var resp authLoginStartResponse
	if err := json.Unmarshal(env.Result, &resp); err != nil {
		t.Fatal(err)
	}
	if resp.State == "" || len(resp.State) > 128 {
		t.Fatalf("invalid CPA oauth state length: %d", len(resp.State))
	}
	for _, r := range resp.State {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.') {
			t.Fatalf("invalid CPA oauth state character %q", r)
		}
	}
	encoded, ok := resp.Metadata[qoderLoginSessionMetadataKey].(string)
	if !ok || encoded == "" {
		t.Fatal("Qoder login session metadata is missing")
	}
	session, err := decodeLoginSession(encoded)
	if err != nil {
		t.Fatalf("decode login session: %v", err)
	}
	if session.Verifier == "" || session.Nonce == "" || session.MachineID == "" || resp.URL == "" {
		t.Fatalf("incomplete login session: %#v", session)
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
	if got := a.Attributes["auth_index_seed"]; got != "qoder:u-1" {
		t.Fatalf("auth_index_seed = %q", got)
	}
	if a.NextRefreshAfter.IsZero() {
		t.Fatal("next refresh was not scheduled")
	}
}

func TestAuthIndexSeedFallsBackWithoutUserID(t *testing.T) {
	cred := credential.Credential{Email: "user@example.com", MachineID: "m-1"}
	if got := authIndexSeed(cred); got != "qoder:user@example.com" {
		t.Fatalf("authIndexSeed() = %q", got)
	}
	cred.Email = ""
	if got := authIndexSeed(cred); got != "qoder:m-1" {
		t.Fatalf("authIndexSeed() machine fallback = %q", got)
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
