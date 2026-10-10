# Qoder plugin for CLIProxyAPI

A native CLIProxyAPI provider plugin that uses a Qoder account directly inside CPA. It reuses the Qoder protocol/signing implementation from `steamwo/qoder-proxy`; no standalone `qoder-proxy` HTTP service is required.

## Current scope

- Qoder PKCE/device authorization through CPA's plugin auth lifecycle
- Refresh-token rotation through CPA auth refresh
- Per-account live model discovery
- OpenAI Chat Completions execution
- Streaming and non-streaming responses
- Function/tool calls
- Qoder free-plan queue retry with SSE heartbeat comments
- Qoder business error to CPA HTTP status mapping
- CPA host HTTP bridge for all upstream traffic, preserving CPA proxy/request-log policy

CPA can translate other supported frontend protocols to/from `chat-completions` before and after the plugin executor.

## Compatibility

The project is designed against:

- `steamwo/qoder-proxy` commit `8c5dcd3b6cb8520d61e496040edffc6d6cc3081e`
- CLIProxyAPI plugin ABI v1 / schema v6 as of `router-for-me/CLIProxyAPI` commit `d318bcc3afb9ea8782862f5e8cdb541afdc9dfa5`

The Go module path is intentionally a child of `github.com/steamwo/qoder-proxy` so Go's `internal/` visibility rules allow the plugin to reuse qoder-proxy's internal provider implementation while remaining a separate repository.

## Build

Requirements: Go 1.23+ and a C compiler.

```bash
make test
make build
```

Artifacts:

```text
dist/linux/amd64/qoder.so
dist/darwin/arm64/qoder.dylib
dist/windows/amd64/qoder.dll
```

Cross-compiling a c-shared plugin requires an appropriate cross C compiler. Building on each target OS/architecture is the simplest option.

## Install in CLIProxyAPI

Copy the shared library into CPA's plugin directory, for example:

```text
plugins/linux/amd64/qoder.so
```

Enable plugins:

```yaml
plugins:
  enabled: true
  dir: "plugins"
  configs:
    qoder:
      enabled: true
      priority: 1
      queue_retries: 20
      queue_max_wait: "10m"
```

The plugin ID is derived from the shared-library basename, so keep the filename `qoder.so`, `qoder.dylib`, or `qoder.dll`.

## Authentication

Use CPA's plugin/provider login flow for provider `qoder`. The plugin starts Qoder's PKCE/device authorization URL, polls for completion, and returns a CPA auth record containing Qoder's token, refresh token, runtime signing material, and account identity.

Existing `qoder-proxy` credential JSON can also be recognized when imported as a Qoder auth file.

## Architecture

```text
client
  -> CLIProxyAPI protocol translator
  -> qoder plugin executor (chat-completions)
  -> CPA host.http.do_stream bridge
  -> Qoder COSY signed inference
  -> Qoder SSE parser
  -> CPA stream / response translator
  -> client
```

All Qoder upstream traffic is executed by CPA's host HTTP bridge rather than a private `http.Transport`, so CPA's configured proxy and request logging remain in the path.

## Notes

This is an unofficial compatibility plugin. Qoder service behavior, account policy, model availability and quotas can change. Use it only in ways permitted by the relevant service terms and policies.
