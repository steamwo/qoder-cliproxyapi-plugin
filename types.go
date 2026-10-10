package main

import (
	"encoding/json"
	"net/http"
	"net/url"
	"time"
)

const (
	abiVersion    uint32 = 1
	schemaVersion uint32 = 6
	pluginID             = "qoder"
)

const (
	methodPluginRegister        = "plugin.register"
	methodPluginReconfigure     = "plugin.reconfigure"
	methodModelStatic           = "model.static"
	methodModelForAuth          = "model.for_auth"
	methodAuthIdentifier        = "auth.identifier"
	methodAuthParse             = "auth.parse"
	methodAuthLoginStart        = "auth.login.start"
	methodAuthLoginPoll         = "auth.login.poll"
	methodAuthRefresh           = "auth.refresh"
	methodExecutorIdentifier    = "executor.identifier"
	methodExecutorExecute       = "executor.execute"
	methodExecutorExecuteStream = "executor.execute_stream"
	methodExecutorCountTokens   = "executor.count_tokens"
	methodExecutorHTTPRequest   = "executor.http_request"
	methodHostHTTPDo            = "host.http.do"
	methodHostHTTPDoStream      = "host.http.do_stream"
	methodHostHTTPStreamRead    = "host.http.stream_read"
	methodHostHTTPStreamClose   = "host.http.stream_close"
	methodHostStreamEmit        = "host.stream.emit"
	methodHostStreamClose       = "host.stream.close"
)

type envelope struct {
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *envelopeError  `json:"error,omitempty"`
}

type envelopeError struct {
	Code       string `json:"code"`
	Message    string `json:"message"`
	Retryable  bool   `json:"retryable,omitempty"`
	HTTPStatus int    `json:"http_status,omitempty"`
}

type lifecycleRequest struct {
	ConfigYAML []byte `json:"config_yaml"`
}

type metadata struct {
	Name             string
	Version          string
	Author           string
	GitHubRepository string
	Logo             string
	ConfigFields     []configField
}

type configField struct {
	Name        string
	Type        string
	EnumValues  []string
	Description string
}

type registration struct {
	SchemaVersion uint32                   `json:"schema_version"`
	Metadata      metadata                 `json:"metadata"`
	Capabilities  registrationCapabilities `json:"capabilities"`
}

type registrationCapabilities struct {
	ModelProvider         bool     `json:"model_provider"`
	AuthProvider          bool     `json:"auth_provider"`
	Executor              bool     `json:"executor"`
	ExecutorModelScope    string   `json:"executor_model_scope"`
	ExecutorInputFormats  []string `json:"executor_input_formats,omitempty"`
	ExecutorOutputFormats []string `json:"executor_output_formats,omitempty"`
}

type hostConfigSummary struct {
	AuthDir          string
	ProxyURL         string
	ForceModelPrefix bool
}

type authData struct {
	Provider         string
	ID               string
	FileName         string
	Label            string
	Prefix           string
	ProxyURL         string
	Disabled         bool
	StorageJSON      []byte
	Metadata         map[string]any
	Attributes       map[string]string
	NextRefreshAfter time.Time
}

type authParseRequest struct {
	Provider string
	Path     string
	FileName string
	RawJSON  []byte
	Host     hostConfigSummary
}

type authParseResponse struct {
	Handled bool
	Auth    authData
	Auths   []authData
}

type authLoginStartRequest struct {
	Provider       string
	BaseURL        string
	Host           hostConfigSummary
	Metadata       map[string]any
	HostCallbackID string `json:"host_callback_id,omitempty"`
}

type authLoginStartResponse struct {
	Provider  string
	URL       string
	State     string
	ExpiresAt time.Time
	Metadata  map[string]any
}

type authLoginPollRequest struct {
	Provider       string
	State          string
	Host           hostConfigSummary
	Metadata       map[string]any
	HostCallbackID string `json:"host_callback_id,omitempty"`
}

type authLoginPollResponse struct {
	Status  string
	Message string
	Auth    authData
	Auths   []authData
}

type authRefreshRequest struct {
	AuthID         string
	AuthProvider   string
	StorageJSON    []byte
	Metadata       map[string]any
	Attributes     map[string]string
	Host           hostConfigSummary
	HostCallbackID string `json:"host_callback_id,omitempty"`
}

type authRefreshResponse struct {
	Auth             authData
	NextRefreshAfter time.Time
}

type modelInfo struct {
	ID                         string
	Object                     string
	Created                    int64
	OwnedBy                    string
	Type                       string
	DisplayName                string
	Name                       string
	Version                    string
	Description                string
	InputTokenLimit            int64
	OutputTokenLimit           int64
	SupportedGenerationMethods []string
	ContextLength              int64
	MaxCompletionTokens        int64
	SupportedParameters        []string
	SupportedInputModalities   []string
	SupportedOutputModalities  []string
	Thinking                   *thinkingSupport
	UserDefined                bool
}

type thinkingSupport struct {
	Min            int
	Max            int
	ZeroAllowed    bool
	DynamicAllowed bool
	Levels         []string
}

type modelResponse struct {
	Provider   string
	Models     []modelInfo
	AuthUpdate authData
}

type authModelRequest struct {
	Plugin         metadata
	AuthID         string
	AuthProvider   string
	StorageJSON    []byte
	Metadata       map[string]any
	Attributes     map[string]string
	Host           hostConfigSummary
	HostCallbackID string `json:"host_callback_id,omitempty"`
}

type executorRequest struct {
	AuthID          string
	AuthProvider    string
	Model           string
	Format          string
	Stream          bool
	Alt             string
	Headers         http.Header
	Query           url.Values
	OriginalRequest []byte
	SourceFormat    string
	Payload         []byte
	Metadata        map[string]any
	StorageJSON     []byte
	AuthMetadata    map[string]any
	AuthAttributes  map[string]string
	StreamID        string `json:"stream_id,omitempty"`
	HostCallbackID  string `json:"host_callback_id,omitempty"`
}

type executorResponse struct {
	Payload  []byte
	Headers  http.Header
	Metadata map[string]any
}

type executorHTTPResponse struct {
	StatusCode int
	Headers    http.Header
	Body       []byte
}

type streamResponse struct {
	Headers http.Header `json:"headers,omitempty"`
}

type httpWireProfile struct {
	HTTP1Only              bool     `json:"http1_only,omitempty"`
	DisableAutoCompression bool     `json:"disable_auto_compression,omitempty"`
	HeaderProfile          []string `json:"header_profile,omitempty"`
}

type hostHTTPRequest struct {
	HostCallbackID string           `json:"host_callback_id,omitempty"`
	Method         string           `json:"method,omitempty"`
	URL            string           `json:"url,omitempty"`
	Headers        http.Header      `json:"headers,omitempty"`
	Body           []byte           `json:"body,omitempty"`
	WireProfile    *httpWireProfile `json:"wire_profile,omitempty"`
}

type hostHTTPResponse struct {
	StatusCode int
	Headers    http.Header
	Body       []byte
}

type hostHTTPStreamResponse struct {
	StatusCode int         `json:"status_code"`
	Headers    http.Header `json:"headers,omitempty"`
	StreamID   string      `json:"stream_id,omitempty"`
}

type hostHTTPStreamReadRequest struct {
	StreamID string `json:"stream_id"`
}

type hostHTTPStreamReadResponse struct {
	Payload []byte `json:"payload,omitempty"`
	Error   string `json:"error,omitempty"`
	Done    bool   `json:"done,omitempty"`
}

type hostHTTPStreamCloseRequest struct {
	StreamID string `json:"stream_id"`
}

type hostStreamEmitRequest struct {
	StreamID string `json:"stream_id"`
	Payload  []byte `json:"payload,omitempty"`
	Error    string `json:"error,omitempty"`
}

type hostStreamCloseRequest struct {
	StreamID string `json:"stream_id"`
	Error    string `json:"error,omitempty"`
}
