// Package tracing provides OpenTelemetry instrumentation for the ingest pipeline.
//
// A-02 F3: OTel tracing not wired in D-02. This package provides the structural
// scaffolding for distributed tracing (traceparent extraction, span creation).
// Full OTel SDK wiring (go.opentelemetry.io/otel + otlptracegrpc exporter) is
// deferred until `go mod tidy` resolves the dependency in CI.
//
// TODO: After `go mod tidy` fetches OTel SDK:
//
//	import (
//	    "go.opentelemetry.io/otel"
//	    "go.opentelemetry.io/otel/propagation"
//	    "go.opentelemetry.io/otel/trace"
//	)
package tracing

import (
	"context"
	"log/slog"
	"net/http"
)

// TraceContext holds W3C Trace Context propagation fields.
type TraceContext struct {
	TraceID    string
	SpanID     string
	TraceFlags string
}

// ExtractTraceParent extracts the W3C traceparent header from an HTTP request.
// Format: 00-{trace_id}-{span_id}-{trace_flags}
func ExtractTraceParent(r *http.Request) string {
	return r.Header.Get("traceparent")
}

// ParseTraceParent parses a W3C traceparent header value.
// Returns nil if the header is malformed or not present.
func ParseTraceParent(traceparent string) *TraceContext {
	if traceparent == "" || len(traceparent) < 55 {
		return nil
	}
	// Expected: "00-{32hex}-{16hex}-{2hex}"
	if len(traceparent) != 55 || traceparent[0:3] != "00-" || traceparent[35:36] != "-" || traceparent[52:53] != "-" {
		return nil
	}
	return &TraceContext{
		TraceID:    traceparent[3:35],
		SpanID:     traceparent[36:52],
		TraceFlags: traceparent[53:55],
	}
}

// InjectTraceContext adds traceparent to a context for downstream propagation.
// Placeholder until OTel SDK is wired — the real implementation will use
// otel.GetTextMapPropagator().Inject(ctx, propagation.MapCarrier{...}).
func InjectTraceContext(ctx context.Context, tc *TraceContext) context.Context {
	// TODO: Wire OTel SDK:
	//   carrier := propagation.MapCarrier{"traceparent": tc.String()}
	//   otel.GetTextMapPropagator().Inject(ctx, carrier)
	_ = tc
	return ctx
}

// String returns the W3C traceparent header value.
func (tc *TraceContext) String() string {
	if tc == nil {
		return ""
	}
	return "00-" + tc.TraceID + "-" + tc.SpanID + "-" + tc.TraceFlags
}

// Middleware is an HTTP middleware that extracts traceparent and injects it
// into the request context for downstream propagation.
func Middleware(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tp := ExtractTraceParent(r)
			if tp != "" {
				tc := ParseTraceParent(tp)
				if tc != nil {
					// Store trace context in request context for Kafka header propagation
					ctx := context.WithValue(r.Context(), traceContextKey, tc)
					r = r.WithContext(ctx)
					log.Debug("traceparent extracted",
						"trace_id", tc.TraceID,
						"span_id", tc.SpanID,
					)
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

// GetTraceContext retrieves the TraceContext from the request context.
func GetTraceContext(ctx context.Context) *TraceContext {
	tc, _ := ctx.Value(traceContextKey).(*TraceContext)
	return tc
}

type contextKey string

const traceContextKey contextKey = "traceContext"

// StartSpan creates a new span. Placeholder — real implementation uses
// otel.Tracer().Start(ctx, name, trace.WithAttributes(...)).
func StartSpan(ctx context.Context, name string) (context.Context, func()) {
	// TODO: Wire OTel SDK:
	//   tracer := otel.Tracer("quantumbilling/engine")
	//   ctx, span := tracer.Start(ctx, name)
	//   return ctx, func() { span.End() }
	return ctx, func() {}
}

// RecordError records an error on the current span.
func RecordError(ctx context.Context, err error) {
	// TODO: Wire OTel SDK:
	//   span := trace.SpanFromContext(ctx)
	//   span.RecordError(err)
	_ = ctx
	_ = err
}
