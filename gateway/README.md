# QuantumBilling — LiteLLM Gateway

This directory contains the LiteLLM proxy gateway configuration and custom
callback/provider modules.

LiteLLM is deployed via docker compose (`--profile gateway`, enabled at D-06).
The proxy configuration ships at `infra/litellm/proxy_server_config.yaml`.

## Structure (post D-06)

- `proxy_server_config.yaml` — LiteLLM routing config (deployed via infra/)
- `custom_callbacks/` — usage-event callback (story_21)
- `pre_call_hooks/` — budget/rate-limit sync hooks (story_22)
- `byok_provider/` — BYOK decryption provider routing (story_23)

## Dev loop

```bash
# Start gateway profile (after core services are up):
docker compose --profile gateway up -d

# Check health:
curl http://localhost:4000/health/liveliness
```
