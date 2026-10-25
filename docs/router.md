# router 0.2.0

Run `router -init` to create private configuration files without overwriting existing files. Paths follow Go's operating-system configuration directory (`$XDG_CONFIG_HOME/router` or `~/.config/router` on Linux; `~/Library/Application Support/router` on macOS). `-config` and `-keys` override these paths. Configure the local model's real name or add providers and API keys, run `router -check`, then `router`.

The default listener is `127.0.0.1:8080`. Router has no client authentication. Bind it to a network interface only behind an authenticated TLS proxy. Provider redirects are returned to callers rather than followed with credentials.

Example configuration for an OpenAI-compatible provider:

```json
{
  "listen": "127.0.0.1:8080",
  "timeout_ms": 120000,
  "providers": {
    "my-provider": {
      "type": "openai_compatible",
      "enabled": true,
      "base_url": "https://provider.example/v1",
      "api_key_ref": "my-provider",
      "models": {
        "primary": {"enabled": true, "upstream_model": "my-model", "class": "smart"},
        "small": {"enabled": true, "upstream_model": "my-small-model", "class": "dumb"}
      }
    }
  }
}
```

Put API credentials in the private keys file: `{"keys":{"my-provider":"YOUR_KEY"}}`. A local `openai_compatible` server can omit `api_key_ref` for unauthenticated access. Do not commit keys. Configuration is read at startup; restart after editing it.

`GET /healthz`, `/status` (also `/v1/status`), `/model`, and `/v1/models` report state. `POST /set-model` accepts `{"model":"primary"}` and selects that model for its class; `POST /set-intelligence` accepts `{"intelligence":"smart"}` or `dumb`.

`POST /v1/chat/completions` accepts an OpenAI-compatible JSON object. Model `smart` or `dumb` chooses a class; an omitted model uses the current class. Candidate order is alphabetical by public model name, with the most recently selected working model first. Class requests fall back on transport errors, 401/402/403/429, and server errors. An explicit public model name stays pinned and returns the upstream response. Each public name must be unique across enabled providers. Avoid `smart` and `dumb` as public model names, since those are class selectors.

Every request field, including tool definitions, tool calls, structured output options and vendor extensions, is preserved for OpenAI-compatible providers. Only `model` is replaced. Streaming SSE responses are flushed as chunks arrive. Requests are limited to 64 MiB and the configured timeout covers the entire upstream response.

Built-in base URLs remain for `openai_api`, `deepseek`, `openrouter`, and `google_vertex` (requires project/location). Other compatible endpoints need `base_url`. OAuth providers require a manually managed auth file with an access token (`access_token`, `accessToken`, `token`, `credentials.access_token`, or `tokens.access_token`); identity tokens are not access tokens. Router does not perform OAuth login/refresh or provide a Codex-specific transport.

The `anthropic_api` adapter handles non-streaming text conversations with `model`, `messages`, `max_tokens`, `temperature` and `top_p`. It rejects unsupported fields, message roles and non-text content instead of silently dropping them. Use an OpenAI-compatible gateway for streaming, tools or multimodal Anthropic requests. This router exposes Chat Completions; it does not expose the Responses API.
