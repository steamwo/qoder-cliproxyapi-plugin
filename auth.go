package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/steamwo/qoder-proxy/internal/credential"
	"github.com/steamwo/qoder-proxy/internal/qoder"
)

const qoderLoginSessionMetadataKey = "qoder_login_session"

func authIdentifier() ([]byte, error) {
	return okEnvelope(map[string]string{"identifier": pluginID})
}

func parseAuth(raw []byte) ([]byte, error) {
	var req authParseRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, err
	}
	var cred credential.Credential
	if err := json.Unmarshal(req.RawJSON, &cred); err != nil || !cred.Valid() {
		return okEnvelope(authParseResponse{Handled: false})
	}
	nameHint := strings.ToLower(req.FileName + " " + req.Path + " " + req.Provider)
	if !strings.Contains(nameHint, "qoder") && cred.RuntimeProfileVersion == 0 && cred.CosyKey == "" && cred.EncryptUserInfo == "" {
		return okEnvelope(authParseResponse{Handled: false})
	}
	return okEnvelope(authParseResponse{Handled: true, Auth: authDataFromCredential(cred, req.FileName, "")})
}

func newOAuthState() (string, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func encodeLoginSession(session qoder.LoginSession) (string, error) {
	raw, err := json.Marshal(session)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func decodeLoginSession(encoded string) (qoder.LoginSession, error) {
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(encoded))
	if err != nil {
		return qoder.LoginSession{}, err
	}
	var session qoder.LoginSession
	if err := json.Unmarshal(raw, &session); err != nil {
		return qoder.LoginSession{}, err
	}
	if strings.TrimSpace(session.Verifier) == "" || strings.TrimSpace(session.Nonce) == "" || strings.TrimSpace(session.MachineID) == "" {
		return qoder.LoginSession{}, fmt.Errorf("Qoder login session is incomplete")
	}
	return session, nil
}

func loginSessionFromPollRequest(req authLoginPollRequest) (qoder.LoginSession, error) {
	if req.Metadata != nil {
		if encoded, ok := req.Metadata[qoderLoginSessionMetadataKey].(string); ok && strings.TrimSpace(encoded) != "" {
			return decodeLoginSession(encoded)
		}
	}
	// Compatibility fallback for direct callers of the old plugin contract.
	return decodeLoginSession(req.State)
}

func startLogin(raw []byte) ([]byte, error) {
	var req authLoginStartRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, err
	}
	session, err := qoder.StartLogin()
	if err != nil {
		return errorEnvelope("login_start_failed", err.Error(), 500), nil
	}
	state, err := newOAuthState()
	if err != nil {
		return errorEnvelope("login_start_failed", err.Error(), 500), nil
	}
	encodedSession, err := encodeLoginSession(session)
	if err != nil {
		return errorEnvelope("login_start_failed", err.Error(), 500), nil
	}
	return okEnvelope(authLoginStartResponse{
		Provider:  pluginID,
		URL:       session.URL,
		State:     state,
		ExpiresAt: time.Now().Add(10 * time.Minute).UTC(),
		Metadata: map[string]any{
			qoderLoginSessionMetadataKey: encodedSession,
		},
	})
}

func pollLogin(raw []byte) ([]byte, error) {
	var req authLoginPollRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, err
	}
	session, err := loginSessionFromPollRequest(req)
	if err != nil {
		return errorEnvelope("invalid_login_state", "invalid Qoder login state", 400), nil
	}

	// PollLogin performs an immediate poll before its 2s ticker. A short deadline
	// gives CPA one provider poll per call without needing qoder's private poll helper.
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	cred, err := qoder.PollLogin(ctx, newHostHTTPClient(req.HostCallbackID), session)
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return okEnvelope(authLoginPollResponse{Status: "pending", Message: "waiting for Qoder authorization"})
	}
	if err != nil {
		return okEnvelope(authLoginPollResponse{Status: "error", Message: err.Error()})
	}
	return okEnvelope(authLoginPollResponse{
		Status:  "success",
		Message: "Qoder authorization completed",
		Auth:    authDataFromCredential(cred, "", ""),
	})
}

func refreshAuth(raw []byte) ([]byte, error) {
	var req authRefreshRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, err
	}
	cred, err := credentialFromStorage(req.StorageJSON)
	if err != nil {
		return errorEnvelope("invalid_auth", err.Error(), 401), nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	next, err := qoder.RefreshCredential(ctx, newHostHTTPClient(req.HostCallbackID), cred)
	if err != nil {
		return errorEnvelope("auth_refresh_failed", err.Error(), 401), nil
	}
	ad := authDataFromCredential(next, "", req.AuthID)
	return okEnvelope(authRefreshResponse{Auth: ad, NextRefreshAfter: ad.NextRefreshAfter})
}

func maybeRefreshCredential(ctx context.Context, client *http.Client, cred credential.Credential) (credential.Credential, bool, error) {
	if cred.ExpiresAt <= 0 || strings.TrimSpace(cred.RefreshToken) == "" {
		return cred, false, nil
	}
	if time.Unix(cred.ExpiresAt, 0).After(time.Now().Add(70 * time.Minute)) {
		return cred, false, nil
	}
	next, err := qoder.RefreshCredential(ctx, client, cred)
	if err != nil {
		return cred, false, err
	}
	return next, true, nil
}

func credentialFromStorage(raw []byte) (credential.Credential, error) {
	var cred credential.Credential
	if err := json.Unmarshal(raw, &cred); err != nil {
		return cred, fmt.Errorf("decode Qoder credential: %w", err)
	}
	if !cred.Valid() {
		return cred, fmt.Errorf("Qoder credential is incomplete")
	}
	return cred, nil
}

func authDataFromCredential(cred credential.Credential, fileName, authID string) authData {
	storage, _ := json.Marshal(cred)
	if strings.TrimSpace(fileName) == "" {
		base := strings.TrimSpace(cred.Email)
		if base == "" {
			base = strings.TrimSpace(cred.UserID)
		}
		base = sanitizeFilePart(base)
		if base == "" {
			base = "account"
		}
		fileName = "qoder-" + base + ".json"
	}
	if strings.TrimSpace(authID) == "" {
		idPart := sanitizeFilePart(cred.UserID)
		if idPart == "" {
			idPart = sanitizeFilePart(cred.MachineID)
		}
		if idPart != "" {
			authID = "qoder-" + idPart
		}
	}
	label := strings.TrimSpace(cred.Email)
	if label == "" {
		label = strings.TrimSpace(cred.Name)
	}
	if label == "" {
		label = "Qoder"
	}
	next := nextRefreshTime(cred)
	return authData{
		Provider:    pluginID,
		ID:          authID,
		FileName:    filepath.Base(fileName),
		Label:       label,
		StorageJSON: storage,
		Metadata: map[string]any{
			"type":          pluginID,
			"email":         cred.Email,
			"user_id":       cred.UserID,
			"token":         cred.Token,
			"refresh_token": cred.RefreshToken,
			"expires_at":    cred.ExpiresAt,
		},
		Attributes:       map[string]string{"auth_kind": "oauth"},
		NextRefreshAfter: next,
	}
}

func nextRefreshTime(cred credential.Credential) time.Time {
	if cred.ExpiresAt <= 0 {
		return time.Now().Add(30 * time.Minute).UTC()
	}
	t := time.Unix(cred.ExpiresAt, 0).Add(-time.Hour)
	if t.Before(time.Now().Add(time.Minute)) {
		t = time.Now().Add(time.Minute)
	}
	return t.UTC()
}

func sanitizeFilePart(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-', r == '_', r == '.', r == '@':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	return strings.Trim(b.String(), "-.")
}
