// Package auth provides Redis-backed API key authentication for the ingest pipeline.
// Implements the Story 2 (Redis Auth Provider) specification.
package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/pente/quantumbilling/engine/internal/models"
	"github.com/redis/go-redis/v9"
)

// ---------------------------------------------------------------------------
// Error types (story_2 AC 14-18)
// ---------------------------------------------------------------------------

// AuthError is the interface all auth errors implement.
type AuthError interface {
	error
	StatusCode() int
	ErrorCode() string
}

type authError struct {
	statusCode int
	errorCode  string
	message    string
}

func (e *authError) Error() string      { return e.message }
func (e *authError) StatusCode() int    { return e.statusCode }
func (e *authError) ErrorCode() string   { return e.errorCode }

var (
	ErrKeyNotFound            = &authError{401, "UNAUTHORIZED", "invalid API key"}
	ErrKeyRevoked             = &authError{401, "KEY_REVOKED", "API key has been revoked"}
	ErrKeyExpired             = &authError{401, "KEY_EXPIRED", "API key has expired"}
	ErrAuthServiceUnavailable = &authError{503, "AUTH_SERVICE_UNAVAILABLE", "authentication service unavailable"}
)

// MaskKey returns the first 8 chars of the key + "...", or "..." for short keys.
func MaskKey(rawKey string) string {
	if len(rawKey) < 8 {
		return "..."
	}
	return rawKey[:8] + "..."
}

// ---------------------------------------------------------------------------
// ValidateAPIKey — story_2 AC 5-13
// ---------------------------------------------------------------------------

// ValidateAPIKey looks up apikey:{rawKey} in Redis and returns a KeyContext.
// Timeout: 2s (story_2 AC 13).
func ValidateAPIKey(ctx context.Context, rdb *redis.Client, rawKey string) (*models.KeyContext, error) {
	timeoutCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	val, err := rdb.Get(timeoutCtx, "apikey:"+rawKey).Result()
	if err == redis.Nil {
		return nil, fmt.Errorf("%w: %s", ErrKeyNotFound, MaskKey(rawKey))
	}
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrAuthServiceUnavailable, err)
	}

	// Try JSON first
	var kc models.KeyContext
	if jsonErr := json.Unmarshal([]byte(val), &kc); jsonErr == nil {
		switch kc.Status {
		case models.KeyStatusActive:
			return &kc, nil
		case models.KeyStatusRevoked:
			return nil, fmt.Errorf("%w: key_id=%s", ErrKeyRevoked, kc.KeyID)
		case models.KeyStatusExpired:
			return nil, fmt.Errorf("%w: key_id=%s", ErrKeyExpired, kc.KeyID)
		default:
			return nil, fmt.Errorf("%w: %s", ErrKeyNotFound, MaskKey(rawKey))
		}
	}

	// Plain-string fallback: treat value as org_id (story_2 AC 4, 12)
	return &models.KeyContext{
		OrgID:      val,
		SourceMode: models.SourceModeDirectIngest,
		Status:     models.KeyStatusActive,
	}, nil
}

// ---------------------------------------------------------------------------
// AuthMiddleware — story_2 AC 19-21
// ---------------------------------------------------------------------------

// AuthMiddleware extracts X-API-Key, validates it, and injects KeyContext
// into the request context.
func AuthMiddleware(rdb *redis.Client, log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rawKey := r.Header.Get("X-API-Key")
			if rawKey == "" {
				writeAuthError(w, 401, "UNAUTHORIZED", "missing X-API-Key header")
				return
			}

			kc, err := ValidateAPIKey(r.Context(), rdb, rawKey)
			if err != nil {
				if ae, ok := err.(AuthError); ok {
					log.Warn("auth failed",
						"key_prefix", MaskKey(rawKey),
						"error_code", ae.ErrorCode(),
					)
					writeAuthError(w, ae.StatusCode(), ae.ErrorCode(), ae.Error())
				} else {
					log.Error("auth unexpected error", "error", err)
					writeAuthError(w, 500, "INTERNAL_ERROR", "internal server error")
				}
				return
			}

			// Inject KeyContext into request context
			ctx := context.WithValue(r.Context(), keyContextKey, kc)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// GetKeyContext retrieves the KeyContext injected by the auth middleware.
func GetKeyContext(ctx context.Context) (*models.KeyContext, bool) {
	kc, ok := ctx.Value(keyContextKey).(*models.KeyContext)
	return kc, ok
}

type contextKey string

const keyContextKey contextKey = "keyContext"

// ---------------------------------------------------------------------------
// Error envelope helper (SCAFFOLD.md §6)
// ---------------------------------------------------------------------------

func writeAuthError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	// Truncate the API key from error messages for security
	safeMsg := message
	if strings.Contains(safeMsg, "sk-") {
		safeMsg = "invalid API key"
	}
	fmt.Fprintf(w, `{"error":{"code":"%s","message":"%s"}}`, code, safeMsg)
}
