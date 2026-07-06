// Package security provides security audit logging (story_14).
package security

import (
	"context"
	"database/sql"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"
)

// Violation types per story_14 / ERD C-7.
const (
	ViolationInvalidKey      = "invalid_key"
	ViolationBudgetExhausted = "budget_exhausted"
	ViolationRateLimit       = "rate_limit"
	ViolationGuardrailBlock  = "guardrail_blocked"
)

// AuditLogger writes security audit log entries to audit.security_audit_logs.
type AuditLogger struct {
	PG  *sql.DB
	Log *slog.Logger
}

// NewAuditLogger creates a security audit logger.
func NewAuditLogger(pg *sql.DB, log *slog.Logger) *AuditLogger {
	return &AuditLogger{PG: pg, Log: log}
}

// LogViolation writes a security audit entry synchronously (story_14 AC 1-6).
// orgID may be empty/nil when the org cannot be resolved (ERD C-25).
func (al *AuditLogger) LogViolation(ctx context.Context, violationType, orgID, keyPrefix, detail string, r *http.Request) {
	if al.PG == nil {
		al.Log.Warn("security audit: postgres not available, skipping", "violation", violationType)
		return
	}

	ip := extractIP(r)
	detail = truncate(detail, 1000)

	ctx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()

	var orgIDPtr *string
	if orgID != "" {
		orgIDPtr = &orgID
	}

	_, err := al.PG.ExecContext(ctx,
		`INSERT INTO audit.security_audit_logs (org_id, violation_type, ip_address, details, triggered_by, created_at)
		 VALUES ($1, $2, $3, $4, $5, NOW())`,
		orgIDPtr, violationType, ip, detail, keyPrefix,
	)
	if err != nil {
		al.Log.Warn("security audit write failed", "violation", violationType, "error", err)
	}
}

// extractIP parses the client IP from X-Forwarded-For or RemoteAddr (story_14 AC 5).
func extractIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		if len(parts) > 0 {
			return strings.TrimSpace(parts[0])
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// truncate limits a string to maxLen characters (story_14 AC 6).
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-14] + "... (truncated)"
}

// ValidViolationTypes returns the valid violation_type values.
func ValidViolationTypes() []string {
	return []string{ViolationInvalidKey, ViolationBudgetExhausted, ViolationRateLimit, ViolationGuardrailBlock}
}

// IsValidViolationType checks if a type string is valid.
func IsValidViolationType(t string) bool {
	for _, v := range ValidViolationTypes() {
		if v == t {
			return true
		}
	}
	return false
}
