"""
QuantumBilling LiteLLM key provisioning sync (story_20).
Syncs API keys from the keys-api to LiteLLM's VerificationToken table.

Called by keys-api on create/revoke to upsert/block tokens in LiteLLM's Postgres.
Uses LiteLLM's internal Postgres (litellm-postgres in compose) directly.
"""
import os
import json
import logging
import hashlib
from typing import Optional

import psycopg2
from psycopg2.extras import RealDictCursor

logger = logging.getLogger("quantumbilling.key_sync")

# LiteLLM Postgres connection (compose profile: gateway)
LITELLM_DATABASE_URL = os.environ.get(
    "LITELLM_DATABASE_URL",
    "postgresql://llmproxy:dbpassword9090@litellm-postgres:5432/litellm"
)


def _get_conn():
    """Get a connection to LiteLLM's Postgres."""
    return psycopg2.connect(LITELLM_DATABASE_URL)


def upsert_verification_token(
    token_hash: str,
    key_id: str,
    org_id: str,
    customer_id: Optional[str],
    source_mode: str,
    budget_limit: Optional[float] = None,
    rate_limit_rpm: Optional[int] = None,
    allowed_models: Optional[list] = None,
) -> None:
    """
    Upsert a VerificationToken in LiteLLM's database (story_20).
    The token_hash is the SHA-256 of the raw API key (same as keys-api).
    """
    metadata = json.dumps({
        "key_id": key_id,
        "org_id": org_id,
        "customer_id": customer_id or "",
        "source_mode": source_mode,
    })

    try:
        conn = _get_conn()
        cur = conn.cursor()
        cur.execute("""
            INSERT INTO "LiteLLM_VerificationToken" (token, key_name, metadata, blocked, created_at)
            VALUES (%s, %s, %s, FALSE, NOW())
            ON CONFLICT (token) DO UPDATE SET
                metadata = EXCLUDED.metadata,
                blocked = FALSE,
                updated_at = NOW()
        """, (token_hash, key_id, metadata))
        conn.commit()
        cur.close()
        conn.close()
        logger.info(f"verification token upserted: key_id={key_id}")
    except Exception as e:
        logger.error(f"verification token upsert failed: {e}")


def block_verification_token(token_hash: str, key_id: str) -> None:
    """
    Block a VerificationToken on key revocation (story_20).
    """
    try:
        conn = _get_conn()
        cur = conn.cursor()
        cur.execute("""
            UPDATE "LiteLLM_VerificationToken"
            SET blocked = TRUE, updated_at = NOW()
            WHERE token = %s
        """, (token_hash,))
        conn.commit()
        cur.close()
        conn.close()
        logger.info(f"verification token blocked: key_id={key_id}")
    except Exception as e:
        logger.error(f"verification token block failed: {e}")


def delete_verification_token(token_hash: str) -> None:
    """Delete a VerificationToken from LiteLLM's database."""
    try:
        conn = _get_conn()
        cur = conn.cursor()
        cur.execute('DELETE FROM "LiteLLM_VerificationToken" WHERE token = %s', (token_hash,))
        conn.commit()
        cur.close()
        conn.close()
        logger.info(f"verification token deleted")
    except Exception as e:
        logger.error(f"verification token delete failed: {e}")
