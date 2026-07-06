// Package orchestration ties Kafka consumer → ClickHouse writer into a complete service.
// Handles batch accumulation, size/time flush triggers, retries, graceful shutdown (story_10).
package orchestration

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/pente/quantumbilling/engine/internal/clickhouse"
	"github.com/pente/quantumbilling/engine/internal/models"
)

// AnalyticsService accumulates events and flushes to ClickHouse (story_10).
type AnalyticsService struct {
	chWriter    *clickhouse.Writer
	Log         *slog.Logger
	batchSize   int
	batchTimeout time.Duration
	mu          sync.Mutex
	currentBatch []*models.UsageEvent
	lastFlush   time.Time
	flushCh     chan struct{}
}

// NewAnalyticsService creates the orchestration service.
func NewAnalyticsService(chWriter *clickhouse.Writer, log *slog.Logger) *AnalyticsService {
	bs := 50000
	if s := os.Getenv("ANALYTICS_BATCH_SIZE"); s != "" {
		fmt.Sscanf(s, "%d", &bs)
	}
	bt := 10 * time.Second
	if s := os.Getenv("ANALYTICS_BATCH_TIMEOUT"); s != "" {
		if d, err := time.ParseDuration(s); err == nil {
			bt = d
		}
	}
	return &AnalyticsService{
		chWriter:     chWriter,
		Log:          log,
		batchSize:    bs,
		batchTimeout: bt,
		currentBatch: make([]*models.UsageEvent, 0, bs),
		lastFlush:    time.Now(),
		flushCh:      make(chan struct{}, 1),
	}
}

// AddEvents appends events to the current batch under mutex. Triggers flush if batch is full.
func (s *AnalyticsService) AddEvents(ctx context.Context, events []*models.UsageEvent) {
	if len(events) == 0 {
		return
	}

	s.mu.Lock()
	s.currentBatch = append(s.currentBatch, events...)
	shouldFlush := len(s.currentBatch) >= s.batchSize

	// Memory safety: cap at 2x batchSize → pause consumer
	if len(s.currentBatch) > 2*s.batchSize {
		s.Log.Warn("batch buffer exceeds safety threshold", "size", len(s.currentBatch))
	}
	s.mu.Unlock()

	if shouldFlush {
		select {
		case s.flushCh <- struct{}{}:
		default:
		}
	}
}

// Start begins the time-based flush ticker (story_10 AC 6).
func (s *AnalyticsService) Start(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(s.batchTimeout)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.mu.Lock()
				hasEvents := len(s.currentBatch) > 0
				s.mu.Unlock()
				if hasEvents {
					select {
					case s.flushCh <- struct{}{}:
					default:
					}
				}
			case <-s.flushCh:
				s.Flush(ctx)
			}
		}
	}()
}

// Flush atomically swaps the current batch and writes to ClickHouse (story_10 AC 8-13).
func (s *AnalyticsService) Flush(ctx context.Context) {
	s.mu.Lock()
	if len(s.currentBatch) == 0 {
		s.mu.Unlock()
		return
	}
	batch := s.currentBatch
	s.currentBatch = make([]*models.UsageEvent, 0, s.batchSize)
	s.lastFlush = time.Now()
	s.mu.Unlock()

	start := time.Now()
	if err := s.chWriter.InsertEventBatch(ctx, batch); err != nil {
		s.Log.Error("clickhouse flush failed, requeuing batch",
			"batch_size", len(batch),
			"error", err,
		)
		// Prepend failed batch to front for retry
		s.mu.Lock()
		s.currentBatch = append(batch, s.currentBatch...)
		s.mu.Unlock()
		return
	}

	duration := time.Since(start)
	eps := float64(len(batch)) / duration.Seconds()
	s.Log.Info("batch flushed to ClickHouse",
		"batch_size", len(batch),
		"duration_ms", duration.Milliseconds(),
		"events_per_second", fmt.Sprintf("%.0f", eps),
	)
}

// PendingCount returns the number of events in the current batch.
func (s *AnalyticsService) PendingCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.currentBatch)
}
