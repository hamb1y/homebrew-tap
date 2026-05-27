class Router < Formula
  desc "OpenAI-compatible provider abstractor with smart/dumb model routing"
  homepage "https://github.com/hamb1y/homebrew-tap"
  url "https://github.com/hamb1y/homebrew-tap.git", branch: "main"
  version "0.1.0"
  license :cannot_represent

  depends_on "go" => :build

  def install
    system "go", "build", "-trimpath", "-ldflags=-s -w", "-o", bin/"router", "src/router/router.go"
  end

  def post_install
    config_dir = Pathname(Dir.home)/".config/router"
    auth_dir = config_dir/"auth"

    auth_dir.mkpath
    (config_dir/"router.json").write(router_config_template) unless (config_dir/"router.json").exist?
    (config_dir/"keys.json").write(keys_config_template) unless (config_dir/"keys.json").exist?
    (config_dir/"README.md").write(config_readme)
  end

  def caveats
    <<~EOS
      Config templates were initialized in ~/.config/router:
        ~/.config/router/router.json
        ~/.config/router/keys.json
        ~/.config/router/auth/
        ~/.config/router/README.md

      Edit the config and keys, then run:
        router
    EOS
  end

  test do
    assert_match "router config file", shell_output("#{bin}/router -h 2>&1")
  end

  def router_config_template
    <<~JSON
      {
        "listen": "127.0.0.1:8080",
        "timeout_ms": 120000,
        "providers": {
          "openai": {
            "type": "openai_api",
            "enabled": true,
            "api_key_ref": "openai",
            "models": {
              "smart": {
                "enabled": true,
                "upstream_model": "gpt-4.1",
                "class": "smart"
              },
              "dumb": {
                "enabled": true,
                "upstream_model": "gpt-4.1-mini",
                "class": "dumb"
              }
            }
          },
          "deepseek": {
            "type": "deepseek",
            "enabled": false,
            "api_key_ref": "deepseek",
            "models": {
              "deepseek-chat": {
                "enabled": true,
                "class": "dumb"
              },
              "deepseek-reasoner": {
                "enabled": true,
                "class": "smart"
              }
            }
          },
          "openrouter": {
            "type": "openrouter",
            "enabled": false,
            "api_key_ref": "openrouter",
            "headers": {
              "HTTP-Referer": "http://localhost:8080",
              "X-Title": "local-router"
            },
            "models": {
              "openrouter-smart": {
                "enabled": true,
                "upstream_model": "anthropic/claude-sonnet-4",
                "class": "smart"
              }
            }
          },
          "openai-compatible": {
            "type": "openai_compatible",
            "enabled": false,
            "base_url": "http://127.0.0.1:11434/v1",
            "api_key_ref": "local-compatible",
            "models": {
              "local-dumb": {
                "enabled": true,
                "upstream_model": "llama3.2",
                "class": "dumb"
              }
            }
          },
          "anthropic": {
            "type": "anthropic_api",
            "enabled": false,
            "api_key_ref": "anthropic",
            "models": {
              "claude-smart": {
                "enabled": true,
                "upstream_model": "claude-sonnet-4-20250514",
                "class": "smart"
              }
            }
          },
          "google-vertex": {
            "type": "google_vertex",
            "enabled": false,
            "project_id": "your-gcp-project",
            "location": "us-central1",
            "auth_file": "vertex.json",
            "models": {
              "vertex-smart": {
                "enabled": true,
                "upstream_model": "google/gemini-2.5-pro",
                "class": "smart"
              }
            }
          },
          "codex": {
            "type": "codex_oauth2",
            "enabled": false,
            "base_url": "https://example.invalid/v1",
            "auth_file": "codex.json",
            "models": {
              "codex-smart": {
                "enabled": true,
                "upstream_model": "gpt-5",
                "class": "smart"
              }
            }
          },
          "google-antigravity": {
            "type": "google_antigravity_oauth2",
            "enabled": false,
            "base_url": "https://example.invalid/v1",
            "auth_file": "antigravity.json",
            "models": {
              "antigravity-smart": {
                "enabled": true,
                "upstream_model": "gemini-2.5-pro",
                "class": "smart"
              }
            }
          }
        }
      }
    JSON
  end

  def keys_config_template
    <<~JSON
      {
        "keys": {
          "openai": "sk-...",
          "deepseek": "...",
          "openrouter": "...",
          "anthropic": "...",
          "local-compatible": "not-used"
        }
      }
    JSON
  end

  def config_readme
    <<~MARKDOWN
      # router config

      Homebrew initializes this directory for router.

      Files:

      - `router.json`: provider, model, and smart/dumb routing config.
      - `keys.json`: API keys referenced by `api_key_ref`.
      - `auth/`: manually managed OAuth2 JSON files.

      Run:

      ```sh
      router
      ```

      Runtime endpoints:

      - `GET http://127.0.0.1:8080/status`
      - `GET http://127.0.0.1:8080/v1/models`
      - `POST http://127.0.0.1:8080/v1/router/model`
      - `POST http://127.0.0.1:8080/v1/chat/completions`

      Switch active smart/dumb model:

      ```sh
      curl -X POST http://127.0.0.1:8080/v1/router/model \\
        -H 'content-type: application/json' \\
        -d '{"class":"smart","model":"smart"}'
      ```

      OAuth2 auth files are read from `auth/` by default. For example, a provider
      with `"auth_file": "codex.json"` reads `~/.config/router/auth/codex.json`.
      The file must contain one of `access_token`, `accessToken`, `id_token`,
      `token`, or `credentials.access_token`.
    MARKDOWN
  end
end
