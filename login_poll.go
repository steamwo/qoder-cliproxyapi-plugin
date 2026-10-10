package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/steamwo/qoder-proxy/internal/credential"
	"github.com/steamwo/qoder-proxy/internal/qoder"
)

func pollLoginV3(raw []byte) ([]byte, error) {
	var req authLoginPollRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, err
	}
	session, err := loginSessionFromPollRequest(req)
	if err != nil {
		return errorEnvelope("invalid_login_state", "invalid Qoder login state", http.StatusBadRequest), nil
	}

	// Perform exactly one Qoder device-token poll per CPA status request. This avoids
	// consuming a successful device token and then cancelling the follow-up userinfo
	// request via the old 1.5s deadline workaround.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cred, pending, err := qoderPollLoginOnce(ctx, newHostHTTPClient(req.HostCallbackID), session)
	if err != nil {
		return okEnvelope(authLoginPollResponse{Status: "error", Message: err.Error()})
	}
	if pending {
		return okEnvelope(authLoginPollResponse{Status: "pending", Message: "waiting for Qoder authorization"})
	}
	return okEnvelope(authLoginPollResponse{
		Status:  "success",
		Message: "Qoder authorization completed",
		Auth:    authDataFromCredential(cred, "", ""),
	})
}

func qoderPollLoginOnce(ctx context.Context, client *http.Client, session qoder.LoginSession) (credential.Credential, bool, error) {
	if client == nil {
		client = http.DefaultClient
	}
	u, err := url.Parse(qoder.PollURL)
	if err != nil {
		return credential.Credential{}, false, err
	}
	query := u.Query()
	query.Set("nonce", session.Nonce)
	query.Set("verifier", session.Verifier)
	query.Set("challenge_method", "S256")
	u.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return credential.Credential{}, false, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", qoder.UserAgent)
	resp, err := client.Do(req)
	if err != nil {
		return credential.Credential{}, false, fmt.Errorf("qoder login poll request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusAccepted || resp.StatusCode == http.StatusNotFound {
		_, _ = io.Copy(io.Discard, resp.Body)
		return credential.Credential{}, true, nil
	}

	var payload map[string]any
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&payload); err != nil {
		if resp.StatusCode/100 != 2 {
			return credential.Credential{}, false, fmt.Errorf("qoder login poll returned HTTP %d", resp.StatusCode)
		}
		return credential.Credential{}, false, fmt.Errorf("decode qoder login response: %w", err)
	}
	if resp.StatusCode/100 != 2 {
		message := qoderFirstString(payload, "message", "error", "error_description")
		if message == "" {
			message = "upstream rejected the device-token poll"
		}
		return credential.Credential{}, false, fmt.Errorf("qoder login poll returned HTTP %d: %s", resp.StatusCode, message)
	}

	root := qoderPayloadRoot(payload)
	token := qoderFirstString(root, "device_token", "token", "access_token")
	if token == "" {
		return credential.Credential{}, false, fmt.Errorf("qoder login response is missing token")
	}

	userInfo, userInfoErr := qoderFetchUserInfo(ctx, client, token)
	userID := qoderFirstString(root, "user_id", "userId")
	if userID == "" {
		userID = qoderFirstString(userInfo, "id", "user_id", "userId")
	}
	if userID == "" {
		if userInfoErr != nil {
			return credential.Credential{}, false, fmt.Errorf("qoder authorization succeeded but userinfo failed: %w", userInfoErr)
		}
		return credential.Credential{}, false, fmt.Errorf("qoder authorization succeeded but user identity could not be resolved")
	}

	expiresAt := qoderFirstInt64(root, "expires_at", "expire_time")
	if expiresAt > 10_000_000_000 {
		expiresAt /= 1000
	}
	if expiresAt == 0 {
		if expiresIn := qoderFirstInt64(root, "expires_in"); expiresIn > 0 {
			expiresAt = time.Now().Unix() + expiresIn
		}
	}

	cred := credential.Credential{
		Token:                 token,
		RefreshToken:          qoderFirstString(root, "refresh_token"),
		RefreshTokenExpiresAt: qoderFirstExpiry(root, []string{"refresh_token_expires_at", "refresh_token_expire_time"}, []string{"refresh_token_expires_in"}),
		UserID:                userID,
		MachineID:             session.MachineID,
		ExpiresAt:             expiresAt,
		CreatedAt:             time.Now().Unix(),
	}
	if userInfoErr == nil {
		cred = qoderApplyUserInfoProfile(cred, userInfo)
		cred, err = qoder.EnsureRuntimeAuthFields(cred)
		if err != nil {
			return credential.Credential{}, false, fmt.Errorf("prepare qoder runtime authentication: %w", err)
		}
	}
	return cred, false, nil
}

func qoderFetchUserInfo(ctx context.Context, client *http.Client, token string) (map[string]any, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, qoder.UserInfoURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", qoder.UserAgent)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil, fmt.Errorf("qoder userinfo returned HTTP %d", resp.StatusCode)
	}
	var payload map[string]any
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&payload); err != nil {
		return nil, err
	}
	return qoderPayloadRoot(payload), nil
}

func qoderPayloadRoot(payload map[string]any) map[string]any {
	current := payload
	for i := 0; i < 3; i++ {
		if qoderFirstString(current, "device_token", "token", "access_token") != "" {
			return current
		}
		var nested map[string]any
		for _, key := range []string{"data", "result", "payload"} {
			if value, ok := current[key].(map[string]any); ok {
				nested = value
				break
			}
		}
		if nested == nil {
			return current
		}
		current = nested
	}
	return current
}

func qoderFirstString(values map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := values[key].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func qoderFirstInt64(values map[string]any, keys ...string) int64 {
	for _, key := range keys {
		switch value := values[key].(type) {
		case float64:
			return int64(value)
		case json.Number:
			n, _ := value.Int64()
			return n
		}
	}
	return 0
}

func qoderFirstBoolPresent(values map[string]any, keys ...string) (bool, bool) {
	for _, key := range keys {
		if value, ok := values[key].(bool); ok {
			return value, true
		}
	}
	return false, false
}

func qoderFirstStringSlice(values map[string]any, keys ...string) []string {
	for _, key := range keys {
		switch list := values[key].(type) {
		case []string:
			return append([]string(nil), list...)
		case []any:
			out := make([]string, 0, len(list))
			for _, item := range list {
				if text, ok := item.(string); ok && strings.TrimSpace(text) != "" {
					out = append(out, strings.TrimSpace(text))
				}
			}
			return out
		}
	}
	return nil
}

func qoderFirstExpiry(values map[string]any, absoluteKeys, durationKeys []string) int64 {
	for _, key := range absoluteKeys {
		switch value := values[key].(type) {
		case float64:
			n := int64(value)
			if n > 10_000_000_000 {
				n /= 1000
			}
			if n > 0 {
				return n
			}
		case json.Number:
			if n, err := value.Int64(); err == nil && n > 0 {
				if n > 10_000_000_000 {
					n /= 1000
				}
				return n
			}
		case string:
			text := strings.TrimSpace(value)
			if parsed, err := time.Parse(time.RFC3339, text); err == nil {
				return parsed.Unix()
			}
			var n int64
			if _, err := fmt.Sscan(text, &n); err == nil && n > 0 {
				if n > 10_000_000_000 {
					n /= 1000
				}
				return n
			}
		}
	}
	if seconds := qoderFirstInt64(values, durationKeys...); seconds > 0 {
		return time.Now().Unix() + seconds
	}
	return 0
}

func qoderApplyUserInfoProfile(cred credential.Credential, userInfo map[string]any) credential.Credential {
	if value := qoderFirstString(userInfo, "id", "user_id", "userId"); value != "" {
		cred.UserID = value
	}
	if value := qoderFirstString(userInfo, "name", "username"); value != "" {
		cred.Name = value
	}
	if value := qoderFirstString(userInfo, "email"); value != "" {
		cred.Email = value
	}
	if value := qoderFirstString(userInfo, "organization_id", "organizationId"); value != "" {
		cred.OrganizationID = value
	}
	if tags := qoderFirstStringSlice(userInfo, "organization_tags", "organizationTags"); tags != nil {
		cred.OrganizationTags = tags
	} else if cred.OrganizationTags == nil {
		cred.OrganizationTags = []string{}
	}
	if value, ok := qoderFirstBoolPresent(userInfo, "data_policy_agreed", "dataPolicyAgreed"); ok {
		cred.DataPolicyAgreed = value
	}
	if value := qoderFirstString(userInfo, "member_id", "memberId"); value != "" {
		cred.MemberID = value
	}
	// The pinned qoder-proxy runtime profile is version 1.
	cred.RuntimeProfileVersion = 1
	return cred
}
