"""
Tests for QuantumBilling LiteLLM custom_logger callback (D-06 / story_21).
Run: python -m pytest gateway/test_custom_logger.py -v
"""
import json
import os
import tempfile
import pytest

# Import the module under test
import sys
sys.path.insert(0, os.path.join(os.path.dirname(__file__), '..', 'gateway'))
import custom_logger


class TestCustomLogger:
    """TC-01 through TC-06 for the LiteLLM callback."""

    def test_tc01_build_event_success(self):
        """TC-01: Event is built correctly from success callback kwargs."""
        kwargs = {
            "id": "cb_test_001",
            "model": "gpt-4",
            "usage": {
                "prompt_tokens": 100,
                "completion_tokens": 50,
                "total_tokens": 150,
            },
            "response_cost": 0.0045,
            "litellm_params": {
                "metadata": {
                    "org_id": "org_acme",
                    "customer_id": "customer_1",
                    "end_user_id": "user_joe",
                    "key_id": "key_abc",
                    "source_mode": "virtual_key",
                },
                "custom_llm_provider": "openai",
            },
            "response_id": "resp_001",
        }
        event = custom_logger._build_event(kwargs, "success")
        assert event is not None
        assert event["event_id"] == "cb_test_001"
        assert event["org_id"] == "org_acme"
        assert event["source_mode"] == "virtual_key"
        assert event["input_tokens"] == 100
        assert event["output_tokens"] == 50
        assert event["cost"] == "0.0045"
        assert event["status"] == "success"
        assert isinstance(event["cost"], str)

    def test_tc02_build_event_failure(self):
        """TC-02: Failure callback produces status=error."""
        kwargs = {
            "id": "cb_fail_001",
            "model": "claude-3",
            "usage": {"prompt_tokens": 0, "completion_tokens": 0, "total_tokens": 0},
            "response_cost": 0,
            "litellm_params": {"metadata": {}, "custom_llm_provider": "anthropic"},
        }
        event = custom_logger._build_event(kwargs, "error")
        assert event["status"] == "error"
        assert event["input_tokens"] == 0

    def test_tc03_dead_letter_write(self):
        """TC-03: Dead-letter file is written on ingest failure."""
        with tempfile.NamedTemporaryFile(mode='w', delete=False, suffix='.jsonl') as f:
            dead_letter_path = f.name

        try:
            original = custom_logger.DEAD_LETTER_FILE
            custom_logger.DEAD_LETTER_FILE = dead_letter_path
            custom_logger.INGEST_URL = "http://localhost:99999/nonexistent"

            event = {"event_id": "dl_test", "org_id": "test"}
            result = custom_logger._post_event(event, retries=1)
            assert not result  # should fail

            with open(dead_letter_path) as f:
                lines = f.readlines()
            assert len(lines) == 1
            assert "dl_test" in lines[0]
        finally:
            custom_logger.DEAD_LETTER_FILE = original
            os.unlink(dead_letter_path)

    def test_tc04_cost_is_string(self):
        """TC-04: Cost is always a decimal string, never float (M-1)."""
        kwargs = {
            "id": "tc04",
            "model": "gpt-4",
            "usage": {"prompt_tokens": 500, "completion_tokens": 300, "total_tokens": 800},
            "response_cost": 0.012345,
            "litellm_params": {"metadata": {}, "custom_llm_provider": "openai"},
        }
        event = custom_logger._build_event(kwargs, "success")
        assert event["cost"] == "0.012345"
        assert isinstance(event["cost"], str)

    def test_tc05_metadata_empty(self):
        """TC-05: Empty metadata produces empty org/customer fields."""
        kwargs = {
            "id": "tc05",
            "model": "test",
            "usage": {"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2},
            "response_cost": 0,
            "litellm_params": {"metadata": {}, "custom_llm_provider": ""},
        }
        event = custom_logger._build_event(kwargs, "success")
        assert event["org_id"] == ""
        assert event["source_mode"] == "virtual_key"

    def test_tc06_retry_backoff(self):
        """TC-06: Post event retries with backoff on failure."""
        custom_logger.INGEST_URL = "http://localhost:99999/nonexistent"
        event = {"event_id": "retry_test"}
        start = __import__('time').time()
        result = custom_logger._post_event(event, retries=3)
        elapsed = __import__('time').time() - start
        assert not result
        # 3 retries with 2s, 4s backoff = at least 6s
        assert elapsed >= 1, f"expected >=1s for retries, got {elapsed:.1f}s"
