// Package postgres provides read-only fallback queries against the canonical
// control-plane tables (ADR-001 §2.1). The engine NEVER writes to these tables
// (one-writer rule). Used when Redis existence caches miss.
package postgres

import (
	"context"
	"database/sql"
	"fmt"
)

// OrgExists checks the canonical identity.organizations table.
// Returns true if the org exists and is ACTIVE.
func OrgExists(ctx context.Context, db *sql.DB, orgID string) (bool, error) {
	var exists bool
	err := db.QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM identity.organizations WHERE id = $1 AND status = 'ACTIVE')`,
		orgID,
	).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("org lookup: %w", err)
	}
	return exists, nil
}

// EndUserInOrg checks that the end_user belongs to the given org.
func EndUserInOrg(ctx context.Context, db *sql.DB, orgID, endUserID string) (bool, error) {
	var exists bool
	err := db.QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM customer.end_users WHERE id = $1 AND org_id = $2)`,
		endUserID, orgID,
	).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("end-user lookup: %w", err)
	}
	return exists, nil
}
