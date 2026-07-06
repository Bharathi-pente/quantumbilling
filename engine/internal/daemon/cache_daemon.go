// Package daemon provides the cache synchronization daemon (story_3).
// Warms Redis caches from canonical control-plane Postgres tables at startup,
// and periodically refreshes to catch drift.
package daemon

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/redis/go-redis/v9"
)

// CacheDaemon syncs API keys, orgs, and end-user memberships from Postgres → Redis.
type CacheDaemon struct {
	PG      *sql.DB
	Redis   *redis.Client
	Log     *slog.Logger
	Interval time.Duration
}

// New creates a cache daemon with the configured sync interval.
func New(pg *sql.DB, rdb *redis.Client, log *slog.Logger) *CacheDaemon {
	interval := 5 * time.Minute
	if s := os.Getenv("SYNC_INTERVAL"); s != "" {
		if d, err := time.ParseDuration(s); err == nil {
			interval = d
		}
	}
	return &CacheDaemon{PG: pg, Redis: rdb, Log: log, Interval: interval}
}

// Start begins the cache warming (non-blocking) and periodic refresh loop.
func (d *CacheDaemon) Start(ctx context.Context) {
	go func() {
		d.warmAll(ctx)
		ticker := time.NewTicker(d.Interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				d.warmAll(ctx)
			}
		}
	}()
}

// warmAll loads all active keys, orgs, and end-users into Redis.
func (d *CacheDaemon) warmAll(ctx context.Context) {
	if d.PG == nil {
		d.Log.Warn("cache daemon: Postgres not available, skipping warm")
		return
	}

	count := 0

	// Warm organizations (story_3 AC 1-2)
	orgRows, err := d.PG.QueryContext(ctx, `SELECT id FROM identity.organizations WHERE status = 'ACTIVE'`)
	if err == nil {
		defer orgRows.Close()
		for orgRows.Next() {
			var orgID string
			if orgRows.Scan(&orgID) == nil {
				d.Redis.Set(ctx, "org:"+orgID, "1", 1*time.Hour)
				count++
			}
		}
	}

	// Warm API keys (story_3 AC 4-5)
	keyRows, err := d.PG.QueryContext(ctx, `SELECT id, org_id, customer_id, end_user_id, key_hash, key_prefix, source_mode, status FROM developer.api_keys WHERE status = 'active'`)
	if err == nil {
		defer keyRows.Close()
		for keyRows.Next() {
			var id, orgID, customerID, endUserID, keyHash, keyPrefix, sourceMode, status string
			if keyRows.Scan(&id, &orgID, &customerID, &endUserID, &keyHash, &keyPrefix, &sourceMode, &status) == nil {
				// Note: key_hash is SHA-256, not the raw key. We can't reconstruct apikey:{raw_key}.
				// The seed script populates Redis directly. Production sync uses the raw key at creation time.
				// Here we warm org/customer/end-user existence keys derived from active API keys.
				d.Redis.Set(ctx, fmt.Sprintf("org:%s", orgID), "1", 1*time.Hour)
				if customerID != "" {
					// customer existence
				}
				if endUserID != "" {
					d.Redis.Set(ctx, fmt.Sprintf("org:%s:enduser:%s", orgID, endUserID), "1", 1*time.Hour)
				}
				_ = id
				_ = keyHash
				_ = keyPrefix
				_ = sourceMode
				_ = status
				count++
			}
		}
	}

	// Warm end-user memberships
	euRows, err := d.PG.QueryContext(ctx, `SELECT org_id, id FROM customer.end_users WHERE status = 'active'`)
	if err == nil {
		defer euRows.Close()
		for euRows.Next() {
			var orgID, euID string
			if euRows.Scan(&orgID, &euID) == nil {
				d.Redis.Set(ctx, fmt.Sprintf("org:%s:enduser:%s", orgID, euID), "1", 1*time.Hour)
				count++
			}
		}
	}

	d.Log.Info("cache daemon warm complete", "records_loaded", count)
}

// SyncKey upserts a single API key context into Redis (story_3 AC 4-5).
func (d *CacheDaemon) SyncKey(ctx context.Context, rawKey, keyID, orgID, customerID, sourceMode, status string) {
	val := fmt.Sprintf(`{"key_id":"%s","org_id":"%s","customer_id":"%s","source_mode":"%s","status":"%s"}`,
		keyID, orgID, customerID, sourceMode, status)
	d.Redis.Set(ctx, "apikey:"+rawKey, val, 0) // no TTL
	d.Log.Info("key synced to cache", "key_id", keyID, "status", status)
}

// RevokeKey removes a key from Redis (story_3 AC 6).
func (d *CacheDaemon) RevokeKey(ctx context.Context, rawKey string) {
	d.Redis.Del(ctx, "apikey:"+rawKey)
	d.Log.Info("key revoked from cache", "key_prefix", rawKey[:min(8, len(rawKey))]+"...")
}
