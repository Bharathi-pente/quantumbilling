// Package fixture provides deterministic event generators for testing.
// A-04 F4: Deterministic event fixture generator per TEST_PLAN G5.
//
// All generators accept a seed (int64) and produce reproducible sequences.
// Use these for integration tests, load tests, and volume benchmarks.
package fixture

import (
	"fmt"
	"math/rand"
	"time"

	"github.com/pente/quantumbilling/engine/internal/models"
)

// Generator creates deterministic UsageEvent sequences for testing.
// Seeded from an int64 — same seed always produces the same events.
type Generator struct {
	rng  *rand.Rand
	seed int64
}

// NewGenerator creates a deterministic event generator with the given seed.
// Use a fixed seed for reproducible tests (e.g., 42), or time.Now().UnixNano()
// for varied load-test data.
func NewGenerator(seed int64) *Generator {
	return &Generator{
		rng:  rand.New(rand.NewSource(seed)),
		seed: seed,
	}
}

// Seed returns the generator's seed for recording in test output.
func (g *Generator) Seed() int64 { return g.seed }

// Generate produces n unique UsageEvents with deterministic pseudo-random values.
// orgID and customerID are fixed per-call; endUserIDs cycle through a pool.
func (g *Generator) Generate(n int, orgID, customerID string) []models.UsageEvent {
	models_ := []string{"gpt-4", "gpt-3.5-turbo", "claude-3-opus", "claude-3-sonnet", "gemini-pro"}
	eventTypes := []string{"chat.completion", "embedding", "image.generation"}
	statuses := []string{"success", "success", "success", "error"} // 75% success
	endUserPool := []string{
		"eu-00000000-0000-4000-a000-000000000001",
		"eu-00000000-0000-4000-a000-000000000002",
		"eu-00000000-0000-4000-a000-000000000003",
	}

	events := make([]models.UsageEvent, n)
	for i := 0; i < n; i++ {
		endUserID := endUserPool[g.rng.Intn(len(endUserPool))]
		model := models_[g.rng.Intn(len(models_))]
		inputTokens := int32(50 + g.rng.Intn(2000))
		outputTokens := int32(20 + g.rng.Intn(1000))
		thinkingTokens := int32(0)
		if g.rng.Intn(3) == 0 { // 33% have thinking tokens
			thinkingTokens = int32(g.rng.Intn(500))
		}

		cost := fmt.Sprintf("%.9f", float64(inputTokens+outputTokens)*0.000001+g.rng.Float64()*0.001)

		events[i] = models.UsageEvent{
			EventID:        fmt.Sprintf("evt-%016d-%08x", g.seed, i),
			OrgID:          orgID,
			CustomerID:     customerID,
			EndUserID:      endUserID,
			SourceMode:     models.SourceModeDirectIngest,
			KeyID:          "key-dev-000000000000",
			EventType:      eventTypes[g.rng.Intn(len(eventTypes))],
			Model:          model,
			InputTokens:    inputTokens,
			OutputTokens:   outputTokens,
			ThinkingTokens: thinkingTokens,
			TotalTokens:    float64(inputTokens + outputTokens + thinkingTokens),
			Cost:           cost,
			Status:         statuses[g.rng.Intn(len(statuses))],
			Service:        "openai",
			TimestampMs:    time.Now().UnixMilli() - int64(g.rng.Intn(3600*1000)), // within last hour
		}
	}
	return events
}

// GenerateBatch produces a batch of exactly 50000 events for load testing (TEST_PLAN G5).
// This is the D-03 done-criterion target: 50k events accepted and produced to Kafka.
func (g *Generator) GenerateBatch(orgID, customerID string) []models.UsageEvent {
	return g.Generate(50000, orgID, customerID)
}

// GenerateVolume produces a large volume for ClickHouse ingestion benchmarks.
func (g *Generator) GenerateVolume(n int, orgID, customerID string) []models.UsageEvent {
	return g.Generate(n, orgID, customerID)
}

// MultiTenant produces events spread across multiple orgs for multi-tenant tests.
func (g *Generator) MultiTenant(orgs, eventsPerOrg int) map[string][]models.UsageEvent {
	result := make(map[string][]models.UsageEvent, orgs)
	for i := 0; i < orgs; i++ {
		orgID := fmt.Sprintf("org-%08x-%04x-4%03x-a%03x-%012x",
			g.seed, i, g.rng.Intn(0xFFF), g.rng.Intn(0xFFF), g.rng.Int63())
		custID := fmt.Sprintf("cust-%08x-%04x-4%03x-a%03x-%012x",
			g.seed+1, i, g.rng.Intn(0xFFF), g.rng.Intn(0xFFF), g.rng.Int63())
		result[orgID] = g.Generate(eventsPerOrg, orgID, custID)
	}
	return result
}
