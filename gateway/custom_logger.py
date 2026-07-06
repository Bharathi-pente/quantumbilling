"""
QuantumBilling LiteLLM CustomLogger callback (story_21).
Posts usage events to the Go ingest API on success/failure.

Usage: Set LITELLM_CUSTOM_CALLBACK=quantumbilling_callbacks.custom_logger
or configure in proxy_server_config.yaml under litellm_settings.callbacks.
"""
import json
import os
import time
import logging
from typing import Dict, Any, Optional

import requests

logger = logging.getLogger("quantumbilling.callback")

# Ingest API endpoint
INGEST_URL = os.environ.get(
    "EVENT_ENGINE_INGEST_URL",
    "http://host.docker.internal:8011/v1/events"
)

# Service ingest key (set via env in compose)
SERVICE_INGEST_KEY = os.environ.get("QB_SERVICE_INGEST_KEY", "")

# Dead-letter file for ingest outages
DEAD_LETTER_FILE = os.environ.get("CALLBACK_DEAD_LETTER_FILE", "/tmp/qb_dead_letter.jsonl")

# Timeout and retry
REQUEST_TIMEOUT = int(os.environ.get("CALLBACK_TIMEOUT", "5"))
MAX_RETRIES = int(os.environ.get("CALLBACK_MAX_RETRIES", "3"))


def _build_event(kwargs: Dict[str, Any], status: str) -> Optional[Dict[str, Any]]:
    """Build a UsageEvent from LiteLLM callback kwargs (story_21)."""
    litellm_params = kwargs.get("litellm_params", {})
    metadata = litellm_params.get("metadata", {})

    # Extract key context from metadata (set by key provisioning in story_20)
    org_id = metadata.get("org_id", "")
    customer_id = metadata.get("customer_id", "")
    end_user_id = metadata.get("end_user_id", "")
    key_id = metadata.get("key_id", "")
    source_mode = metadata.get("source_mode", "virtual_key")

    usage = kwargs.get("usage", {})
    model = kwargs.get("model", "")
    response_cost = kwargs.get("response_cost", 0)

    event = {
        "event_id": kwargs.get("id", f"cb_{int(time.time()*1000)}"),
        "org_id": org_id,
        "customer_id": customer_id,
        "end_user_id": end_user_id,
        "source_mode": source_mode,
        "key_id": key_id,
        "event_type": "llm_request",
        "model": model,
        "input_tokens": usage.get("prompt_tokens", 0) or 0,
        "output_tokens": usage.get("completion_tokens", 0) or 0,
        "thinking_tokens": usage.get("completion_tokens_details", {}).get("reasoning_tokens", 0) or 0,
        "total_tokens": usage.get("total_tokens", 0) or 0,
        "unit": "tokens",
        "latency": f"{int((kwargs.get('end_time', time.time()) - kwargs.get('start_time', time.time())) * 1000)}ms",
        "cost": str(response_cost),
        "status": status,
        "service": "chat",
        "timestamp_ms": int(time.time() * 1000),
        "metadata": {
            "provider": litellm_params.get("custom_llm_provider", ""),
            "response_id": kwargs.get("response_id", ""),
        },
    }
    return event


def _post_event(event: Dict[str, Any], retries: int = MAX_RETRIES) -> bool:
    """Post a single event to the ingest API with retry and dead-letter fallback."""
    headers = {
        "Content-Type": "application/json",
        "X-API-Key": SERVICE_INGEST_KEY,
    }
    for attempt in range(retries):
        try:
            resp = requests.post(INGEST_URL, json=event, headers=headers, timeout=REQUEST_TIMEOUT)
            if resp.status_code == 202:
                return True
            logger.warning(f"ingest API returned {resp.status_code}: {resp.text}")
        except requests.RequestException as e:
            logger.error(f"ingest API unreachable (attempt {attempt+1}/{retries}): {e}")
            time.sleep(2 ** attempt)

    # Dead-letter: write to file for later replay
    try:
        with open(DEAD_LETTER_FILE, "a") as f:
            f.write(json.dumps(event) + "\n")
        logger.info(f"event dead-lettered: {event.get('event_id')}")
    except Exception as e:
        logger.critical(f"dead-letter write failed: {e}")

    return False


async def async_log_success_handler(kwargs, response_obj, start_time, end_time):
    """LiteLLM success callback — posts usage event to ingest API."""
    try:
        kwargs["start_time"] = start_time
        kwargs["end_time"] = end_time
        event = _build_event(kwargs, "success")
        if event:
            _post_event(event)
    except Exception as e:
        logger.error(f"success callback failed: {e}")


async def async_log_failure_handler(kwargs, response_obj, start_time, end_time):
    """LiteLLM failure callback — posts error event to ingest API."""
    try:
        kwargs["start_time"] = start_time
        kwargs["end_time"] = end_time
        event = _build_event(kwargs, "error")
        if event:
            _post_event(event)
    except Exception as e:
        logger.error(f"failure callback failed: {e}")


# LiteLLM looks for this module-level variable
custom_logger = type("CustomLogger", (), {
    "async_log_success_handler": staticmethod(async_log_success_handler),
    "async_log_failure_handler": staticmethod(async_log_failure_handler),
})
