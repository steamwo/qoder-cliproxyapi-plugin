package main

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/steamwo/qoder-proxy/internal/qoder"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func testHTTPResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func TestQoderPollLoginOncePendingDoesNotRetry(t *testing.T) {
	calls := 0
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		if req.URL.String()[:len(qoder.PollURL)] != qoder.PollURL {
			t.Fatalf("unexpected URL: %s", req.URL)
		}
		return testHTTPResponse(http.StatusAccepted, ""), nil
	})}

	_, pending, err := qoderPollLoginOnce(context.Background(), client, qoder.LoginSession{
		Verifier: "verifier", Nonce: "nonce", MachineID: "machine",
	})
	if err != nil {
		t.Fatalf("poll failed: %v", err)
	}
	if !pending {
		t.Fatal("expected pending result")
	}
	if calls != 1 {
		t.Fatalf("expected exactly one HTTP request, got %d", calls)
	}
}

func TestQoderPollLoginOnceCompletesUserInfoBeforeSuccess(t *testing.T) {
	calls := 0
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		switch req.URL.Host {
		case "openapi.qoder.sh":
			if strings.Contains(req.URL.Path, "/deviceToken/poll") {
				return testHTTPResponse(http.StatusOK, `{"device_token":"access","refresh_token":"refresh","user_id":"u-1","expires_in":3600}`), nil
			}
			if strings.Contains(req.URL.Path, "/userinfo") {
				return testHTTPResponse(http.StatusOK, `{"id":"u-1","email":"user@example.com","organization_tags":[]}`), nil
			}
		}
		t.Fatalf("unexpected request: %s", req.URL)
		return nil, nil
	})}

	cred, pending, err := qoderPollLoginOnce(context.Background(), client, qoder.LoginSession{
		Verifier: "verifier", Nonce: "nonce", MachineID: "machine",
	})
	if err != nil {
		t.Fatalf("poll failed: %v", err)
	}
	if pending {
		t.Fatal("successful authorization reported pending")
	}
	if !cred.Valid() {
		t.Fatalf("credential is incomplete: %#v", cred)
	}
	if cred.Email != "user@example.com" {
		t.Fatalf("email = %q", cred.Email)
	}
	if cred.CosyKey == "" || cred.EncryptUserInfo == "" {
		t.Fatal("runtime authentication fields were not prepared")
	}
	if calls != 2 {
		t.Fatalf("expected poll + userinfo requests, got %d", calls)
	}
}
