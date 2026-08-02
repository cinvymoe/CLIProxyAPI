# pi-cliproxy-provider

A [pi](https://pi.dev) package that registers the local CLIProxyAPI (2api)
instance as a provider automatically at startup.

At every pi launch, the extension fetches `GET {baseUrl}/models` from the
running proxy and registers the model catalog as the `2api` provider — no
manual `models.json` editing needed when the proxy's model list changes.

## Install

```bash
pi install /mnt/sdb1/code2api/CLIProxyAPI/pi-cliproxy-provider
```

Uninstall: `pi remove /mnt/sdb1/code2api/CLIProxyAPI/pi-cliproxy-provider`

## Configuration

| Variable | Default | Purpose |
|---|---|---|
| `2API_BASE_URL` | `http://127.0.0.1:8317/v1` | Proxy base URL |
| `2API_API_KEY` | stored `2api` credential in `~/.pi/agent/auth.json`, else `test-api-key` | Bearer token for the proxy |

The provider is registered as `2api` (matching the existing credential in
`~/.pi/agent/auth.json`) with `api: "openai-completions"`.

## Metadata preservation

Hand-tuned metadata in `~/.pi/agent/models.json` for the `2api` provider
(`reasoning`, `thinkingLevelMap`, `cost`, `compat`, `contextWindow`,
`maxTokens`, `input`, `headers`) is merged by model id into the fetched
catalog, so existing tuning survives while new models appear automatically.

Provider-level `compat` from `models.json` is applied to every model as a
default (model-level `compat` wins).

## Verify

```bash
pi --list-models | grep 2api
```

## Notes

- If the proxy is unreachable at startup, the extension logs a warning and
  skips registration — the static `~/.pi/agent/models.json` config still
  applies.
- New models default to `reasoning: true`, `input: ["text"]`, zero cost,
  `contextWindow: 128000`, `maxTokens: 16384`. Override per model by adding
  an entry to `~/.pi/agent/models.json`; the extension picks it up on the
  next launch.
