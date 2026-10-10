package main

import (
	"context"
	"encoding/json"
	"time"

	"github.com/steamwo/qoder-proxy/internal/qoder"
)

func staticModels() ([]byte, error) {
	// Qoder catalogs are account-bound and dynamic; the provider identity is
	// still returned so CPA can bind the executor before auth model discovery.
	return okEnvelope(modelResponse{Provider: pluginID, Models: nil})
}

func modelsForAuth(raw []byte) ([]byte, error) {
	var req authModelRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, err
	}
	cred, err := credentialFromStorage(req.StorageJSON)
	if err != nil {
		return errorEnvelope("invalid_auth", err.Error(), 401), nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	httpClient := newHostHTTPClient(req.HostCallbackID)
	refreshedCred, refreshed, refreshErr := maybeRefreshCredential(ctx, httpClient, cred)
	if refreshErr != nil && cred.ExpiresAt > 0 && time.Now().Unix() >= cred.ExpiresAt {
		return errorEnvelope("auth_refresh_failed", refreshErr.Error(), 401), nil
	}
	if refreshed {
		cred = refreshedCred
	}
	registry := qoder.NewRegistry(httpClient, cred)
	models, err := registry.List(ctx)
	if err != nil {
		return errorEnvelope("model_discovery_failed", err.Error(), 502), nil
	}
	out := make([]modelInfo, 0, len(models))
	for _, m := range models {
		out = append(out, modelInfoFromQoder(m))
	}
	resp := modelResponse{Provider: pluginID, Models: out}
	if refreshed {
		resp.AuthUpdate = authDataFromCredential(cred, "", req.AuthID)
	}
	return okEnvelope(resp)
}

func modelInfoFromQoder(m qoder.Model) modelInfo {
	levels := append([]string(nil), m.SupportedReasoningEfforts()...)
	thinking := (*thinkingSupport)(nil)
	if len(levels) > 0 || m.SupportsReasoningDisabled() {
		thinking = &thinkingSupport{ZeroAllowed: m.SupportsReasoningDisabled(), DynamicAllowed: true, Levels: levels}
	}
	ctxLen := int64(m.MaxInputTokens)
	if m.MaxOutputTokens > 0 {
		ctxLen += int64(m.MaxOutputTokens)
	}
	modalities := []string{"text"}
	if m.IsVL {
		modalities = append(modalities, "image")
	}
	return modelInfo{
		ID:                         m.DisplayName,
		Object:                     "model",
		OwnedBy:                    pluginID,
		DisplayName:                m.DisplayName,
		Name:                       m.DisplayName,
		Description:                "Qoder account model",
		InputTokenLimit:            int64(m.MaxInputTokens),
		OutputTokenLimit:           int64(m.MaxOutputTokens),
		ContextLength:              ctxLen,
		MaxCompletionTokens:        int64(m.MaxOutputTokens),
		SupportedGenerationMethods: []string{"chat"},
		SupportedParameters:        []string{"tools", "reasoning_effort", "max_tokens"},
		SupportedInputModalities:   modalities,
		SupportedOutputModalities:  []string{"text"},
		Thinking:                   thinking,
		UserDefined:                true,
	}
}
