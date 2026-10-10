package main

/*
#include "bridge.h"
*/
import "C"

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"unsafe"
)

func callHost(method string, payload any) (json.RawMessage, error) {
	rawPayload, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal host callback %s: %w", method, err)
	}
	cMethod := C.CString(method)
	defer C.free(unsafe.Pointer(cMethod))
	var response C.cliproxy_buffer
	var requestPtr *C.uint8_t
	if len(rawPayload) > 0 {
		ptr := C.CBytes(rawPayload)
		if ptr == nil {
			return nil, fmt.Errorf("allocate host callback payload")
		}
		defer C.free(ptr)
		requestPtr = (*C.uint8_t)(ptr)
	}
	code := C.cliproxy_call_host(cMethod, requestPtr, C.size_t(len(rawPayload)), &response)
	var raw []byte
	if response.ptr != nil && response.len > 0 {
		raw = C.GoBytes(response.ptr, C.int(response.len))
	}
	if response.ptr != nil {
		C.cliproxy_free_host_buffer(response.ptr, response.len)
	}
	if len(raw) == 0 {
		return nil, fmt.Errorf("host callback %s returned no response (code=%d)", method, int(code))
	}
	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("decode host callback %s: %w", method, err)
	}
	if !env.OK {
		if env.Error != nil {
			return nil, fmt.Errorf("%s: %s", env.Error.Code, env.Error.Message)
		}
		return nil, fmt.Errorf("host callback %s failed", method)
	}
	if code != 0 {
		return nil, fmt.Errorf("host callback %s returned code=%d", method, int(code))
	}
	return append(json.RawMessage(nil), env.Result...), nil
}

type hostTransport struct {
	callbackID string
}

func newHostHTTPClient(callbackID string) *http.Client {
	return &http.Client{Transport: &hostTransport{callbackID: callbackID}}
}

func (t *hostTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req == nil || req.URL == nil {
		return nil, fmt.Errorf("invalid HTTP request")
	}
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	if req.Body != nil {
		_ = req.Body.Close()
	}
	raw, err := callHost(methodHostHTTPDoStream, hostHTTPRequest{
		HostCallbackID: t.callbackID,
		Method:         req.Method,
		URL:            req.URL.String(),
		Headers:        req.Header.Clone(),
		Body:           body,
		WireProfile:    &httpWireProfile{DisableAutoCompression: true},
	})
	if err != nil {
		return nil, err
	}
	var upstream hostHTTPStreamResponse
	if err := json.Unmarshal(raw, &upstream); err != nil {
		return nil, err
	}
	if strings.TrimSpace(upstream.StreamID) == "" {
		return nil, fmt.Errorf("host HTTP bridge returned empty stream_id")
	}
	return &http.Response{
		StatusCode: upstream.StatusCode,
		Status:     fmt.Sprintf("%d %s", upstream.StatusCode, http.StatusText(upstream.StatusCode)),
		Header:     upstream.Headers.Clone(),
		Body:       &hostStreamBody{streamID: upstream.StreamID},
		Request:    req,
	}, nil
}

type hostStreamBody struct {
	mu       sync.Mutex
	streamID string
	buf      bytes.Reader
	done     bool
	closed   bool
}

func (b *hostStreamBody) Read(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for {
		if b.buf.Len() > 0 {
			return b.buf.Read(p)
		}
		if b.done || b.closed {
			return 0, io.EOF
		}
		raw, err := callHost(methodHostHTTPStreamRead, hostHTTPStreamReadRequest{StreamID: b.streamID})
		if err != nil {
			return 0, err
		}
		var chunk hostHTTPStreamReadResponse
		if err := json.Unmarshal(raw, &chunk); err != nil {
			return 0, err
		}
		if chunk.Error != "" {
			return 0, fmt.Errorf("upstream stream: %s", chunk.Error)
		}
		if len(chunk.Payload) > 0 {
			b.buf.Reset(chunk.Payload)
		}
		if chunk.Done {
			b.done = true
		}
		if b.buf.Len() == 0 && b.done {
			return 0, io.EOF
		}
	}
}

func (b *hostStreamBody) Close() error {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return nil
	}
	b.closed = true
	streamID := b.streamID
	b.mu.Unlock()
	_, err := callHost(methodHostHTTPStreamClose, hostHTTPStreamCloseRequest{StreamID: streamID})
	return err
}

func emitPluginStream(streamID string, payload []byte) error {
	_, err := callHost(methodHostStreamEmit, hostStreamEmitRequest{StreamID: streamID, Payload: payload})
	return err
}

func closePluginStream(streamID string, errMsg string) {
	_, _ = callHost(methodHostStreamClose, hostStreamCloseRequest{StreamID: streamID, Error: strings.TrimSpace(errMsg)})
}
