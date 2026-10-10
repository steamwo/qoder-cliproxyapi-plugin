package main

/*
#include "bridge.h"
*/
import "C"

import (
	"encoding/json"
	"unsafe"
)

func main() {}

//export cliproxy_plugin_init
func cliproxy_plugin_init(host *C.cliproxy_host_api, plugin *C.cliproxy_plugin_api) C.int {
	if plugin == nil {
		return 1
	}
	C.cliproxy_store_host(host)
	plugin.abi_version = C.uint32_t(abiVersion)
	plugin.call = C.cliproxy_plugin_call_fn(C.cliproxyPluginCall)
	plugin.free_buffer = C.cliproxy_plugin_free_fn(C.cliproxyPluginFree)
	plugin.shutdown = C.cliproxy_plugin_shutdown_fn(C.cliproxyPluginShutdown)
	currentConfig.Store(defaultPluginConfig())
	return 0
}

//export cliproxyPluginCall
func cliproxyPluginCall(method *C.char, request *C.uint8_t, requestLen C.size_t, response *C.cliproxy_buffer) C.int {
	if response != nil {
		response.ptr = nil
		response.len = 0
	}
	if method == nil {
		writeResponse(response, errorEnvelope("invalid_method", "method is required", 400))
		return 1
	}
	var requestBytes []byte
	if request != nil && requestLen > 0 {
		requestBytes = C.GoBytes(unsafe.Pointer(request), C.int(requestLen))
	}
	raw, err := handleMethod(C.GoString(method), requestBytes)
	if err != nil {
		writeResponse(response, errorEnvelope("plugin_error", err.Error(), 500))
		return 1
	}
	writeResponse(response, raw)
	return 0
}

//export cliproxyPluginFree
func cliproxyPluginFree(ptr unsafe.Pointer, _ C.size_t) {
	if ptr != nil {
		C.free(ptr)
	}
}

//export cliproxyPluginShutdown
func cliproxyPluginShutdown() {}

func handleMethod(method string, request []byte) ([]byte, error) {
	switch method {
	case methodPluginRegister, methodPluginReconfigure:
		var req lifecycleRequest
		if len(request) > 0 {
			if err := json.Unmarshal(request, &req); err != nil {
				return nil, err
			}
		}
		currentConfig.Store(parseConfig(req.ConfigYAML))
		return okEnvelope(pluginRegistration())
	case methodAuthIdentifier:
		return authIdentifier()
	case methodAuthParse:
		return parseAuth(request)
	case methodAuthLoginStart:
		return startLogin(request)
	case methodAuthLoginPoll:
		return pollLoginV3(request)
	case methodAuthRefresh:
		return refreshAuth(request)
	case methodModelStatic:
		return staticModels()
	case methodModelForAuth:
		return modelsForAuth(request)
	case methodExecutorIdentifier:
		return executorIdentifier()
	case methodExecutorExecute:
		return execute(request)
	case methodExecutorExecuteStream:
		return executeStream(request)
	case methodExecutorCountTokens:
		return countTokens(request)
	case methodExecutorHTTPRequest:
		return executorHTTPRequest(request)
	default:
		return errorEnvelope("unknown_method", "unknown method: "+method, 400), nil
	}
}

func pluginRegistration() registration {
	return registration{
		SchemaVersion: schemaVersion,
		Metadata: metadata{
			Name: "Qoder", Version: "0.0.3", Author: "steamwo",
			GitHubRepository: "https://github.com/steamwo/qoder-cliproxyapi-plugin",
			ConfigFields: []configField{
				{Name: "enabled", Type: "boolean", Description: "Enable the Qoder provider."},
				{Name: "queue_retries", Type: "integer", Description: "Maximum retries for Qoder free-plan queue responses."},
				{Name: "queue_max_wait", Type: "string", Description: "Maximum total queue wait, for example 10m."},
			},
		},
		Capabilities: registrationCapabilities{
			ModelProvider: true, AuthProvider: true, Executor: true, ExecutorModelScope: "oauth",
			ExecutorInputFormats: []string{"chat-completions"}, ExecutorOutputFormats: []string{"chat-completions"},
		},
	}
}

func okEnvelope(v any) ([]byte, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return json.Marshal(envelope{OK: true, Result: raw})
}

func errorEnvelope(code, message string, httpStatus int) []byte {
	raw, _ := json.Marshal(envelope{OK: false, Error: &envelopeError{Code: code, Message: message, HTTPStatus: httpStatus}})
	return raw
}

func writeResponse(response *C.cliproxy_buffer, raw []byte) {
	if response == nil || len(raw) == 0 {
		return
	}
	ptr := C.CBytes(raw)
	if ptr == nil {
		return
	}
	response.ptr = ptr
	response.len = C.size_t(len(raw))
}
