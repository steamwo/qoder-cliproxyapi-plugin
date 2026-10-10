package main

import (
	"net/http"
	"strings"
	"testing"
)

func TestReadRequestBodyHandlesNilBody(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, "https://example.com/oauth/poll", nil)
	if err != nil {
		t.Fatal(err)
	}
	body, err := readRequestBody(req)
	if err != nil {
		t.Fatalf("readRequestBody() error = %v", err)
	}
	if len(body) != 0 {
		t.Fatalf("readRequestBody() = %q, want empty", body)
	}
}

func TestReadRequestBodyReadsPayload(t *testing.T) {
	req, err := http.NewRequest(http.MethodPost, "https://example.com/token", strings.NewReader("payload"))
	if err != nil {
		t.Fatal(err)
	}
	body, err := readRequestBody(req)
	if err != nil {
		t.Fatalf("readRequestBody() error = %v", err)
	}
	if string(body) != "payload" {
		t.Fatalf("readRequestBody() = %q, want payload", body)
	}
}

func TestUseStreamingHostHTTP(t *testing.T) {
	jsonReq, _ := http.NewRequest(http.MethodGet, "https://example.com/userinfo", nil)
	jsonReq.Header.Set("Accept", "application/json")
	if useStreamingHostHTTP(jsonReq) {
		t.Fatal("application/json request unexpectedly selected streaming host HTTP")
	}

	streamReq, _ := http.NewRequest(http.MethodPost, "https://example.com/chat", nil)
	streamReq.Header.Set("Accept", "text/event-stream")
	if !useStreamingHostHTTP(streamReq) {
		t.Fatal("text/event-stream request did not select streaming host HTTP")
	}
}
