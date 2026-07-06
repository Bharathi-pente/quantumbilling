// Package byok handles BYOK credential encryption/decryption (story_13).
// Dev-only: uses BYOK_MASTER_KEY env var for AES-256-GCM.
// Production: KMS envelope encryption (ADR-001 §7).
package byok

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
)

// Service handles BYOK provider key registration and retrieval.
type Service struct {
	PG         *sql.DB
	Log        *slog.Logger
	masterKey  []byte // 32-byte AES-256 key derived from BYOK_MASTER_KEY
}

// NewService creates a BYOK service with AES-256-GCM master key.
func NewService(pg *sql.DB, log *slog.Logger) *Service {
	mk := resolveMasterKey()
	return &Service{PG: pg, Log: log, masterKey: mk}
}

// resolveMasterKey derives a 32-byte AES-256 key from BYOK_MASTER_KEY (story_13 AC 1-2).
// DEV ONLY — ADR-001 §7: use KMS envelope encryption in production.
func resolveMasterKey() []byte {
	raw := os.Getenv("BYOK_MASTER_KEY")
	if raw == "" {
		raw = "default-byok-master-key-fallback-32b"
	}
	h := sha256.Sum256([]byte(raw))
	return h[:] // 32 bytes
}

// RegisterProviderKey encrypts and stores a provider API key (story_13 AC 3-9).
func (s *Service) RegisterProviderKey(ctx context.Context, orgID, provider, apiKey string) error {
	provider = strings.ToLower(provider)
	valid := map[string]bool{"openai": true, "anthropic": true, "google": true, "azure": true, "cohere": true}
	if !valid[provider] {
		return fmt.Errorf("UNSUPPORTED_PROVIDER: %s", provider)
	}

	// AES-256-GCM encrypt with fresh 12-byte IV
	encrypted, iv, err := encrypt(s.masterKey, []byte(apiKey))
	if err != nil {
		return fmt.Errorf("encryption failed: %w", err)
	}

	if s.PG != nil {
		_, err = s.PG.ExecContext(ctx,
			`INSERT INTO security.byok_provider_keys (org_id, provider, encrypted_key, key_iv, created_at, updated_at)
			 VALUES ($1, $2, $3, $4, NOW(), NOW())
			 ON CONFLICT (org_id, provider) DO UPDATE SET encrypted_key = $3, key_iv = $4, updated_at = NOW()`,
			orgID, provider, encrypted, iv,
		)
		if err != nil {
			return fmt.Errorf("postgres insert failed: %w", err)
		}
	}

	s.Log.Info("BYOK key registered", "org_id", orgID, "provider", provider)
	return nil
}

// GetProviderKey decrypts and returns the raw provider API key (story_13 AC 10-13).
func (s *Service) GetProviderKey(ctx context.Context, orgID, provider string) (string, error) {
	if s.PG == nil {
		return "", fmt.Errorf("postgres not available")
	}

	var encrypted, iv []byte
	err := s.PG.QueryRowContext(ctx,
		`SELECT encrypted_key, key_iv FROM security.byok_provider_keys WHERE org_id = $1 AND provider = $2`,
		orgID, provider,
	).Scan(&encrypted, &iv)
	if err == sql.ErrNoRows {
		return "", fmt.Errorf("BYOK key not found for org=%s provider=%s", orgID, provider)
	}
	if err != nil {
		return "", fmt.Errorf("BYOK lookup failed: %w", err)
	}

	raw, err := decrypt(s.masterKey, encrypted, iv)
	if err != nil {
		return "", fmt.Errorf("decryption failed — key may be corrupted or master key changed: %w", err)
	}
	return string(raw), nil
}

// encrypt performs AES-256-GCM encryption with a random 12-byte IV.
func encrypt(key, plaintext []byte) (ciphertext, iv []byte, err error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, nil, err
	}
	iv = make([]byte, gcm.NonceSize()) // 12 bytes
	if _, err := io.ReadFull(rand.Reader, iv); err != nil {
		return nil, nil, err
	}
	ciphertext = gcm.Seal(nil, iv, plaintext, nil)
	return ciphertext, iv, nil
}

// decrypt performs AES-256-GCM decryption with authentication check.
func decrypt(key, ciphertext, iv []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	plaintext, err := gcm.Open(nil, iv, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("GCM authentication failed: %w", err)
	}
	return plaintext, nil
}

// --- Helpers for tests ---

func hexBytes(b []byte) string { return hex.EncodeToString(b) }

var _ = hexBytes
