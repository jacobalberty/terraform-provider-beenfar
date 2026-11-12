package provider

import (
	"context"
	"os"
	"strconv"
	"sync"

	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

// Telemetry is how the provider traces its calls to beenfar.
type Telemetry struct {
	TracerProvider trace.TracerProvider
	Propagator     propagation.TextMapPropagator
	// parent returns ctx carrying the trace context from the environment
	// (TRACEPARENT and TRACESTATE), if any.
	parent func(context.Context) context.Context
}

var (
	telemetryOnce sync.Once
	telemetry     Telemetry
)

// defaultTelemetry sets up tracing once per provider process, from the
// standard OpenTelemetry environment variables:
//
//   - Spans are always created, so every call to beenfar carries a W3C
//     traceparent header and the controller's spans for one Terraform
//     operation share a trace id.
//   - They are exported over OTLP/HTTP only when OTEL_EXPORTER_OTLP_ENDPOINT
//     or OTEL_EXPORTER_OTLP_TRACES_ENDPOINT is set. Export is synchronous,
//     since Terraform stops the provider process without warning.
//   - OTEL_SDK_DISABLED=true turns tracing off entirely.
//   - TRACEPARENT (and TRACESTATE), as set by CI systems and wrappers that
//     follow the OpenTelemetry environment-carrier convention, make the
//     provider's spans children of that trace.
func defaultTelemetry(ctx context.Context) Telemetry {
	telemetryOnce.Do(func() { telemetry = newTelemetry(ctx) })
	return telemetry
}

func newTelemetry(ctx context.Context) Telemetry {
	prop := propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{})
	t := Telemetry{Propagator: prop, parent: func(ctx context.Context) context.Context {
		return prop.Extract(ctx, propagation.MapCarrier{
			"traceparent": os.Getenv("TRACEPARENT"),
			"tracestate":  os.Getenv("TRACESTATE"),
		})
	}}
	if disabled, _ := strconv.ParseBool(os.Getenv("OTEL_SDK_DISABLED")); disabled {
		t.TracerProvider = noop.NewTracerProvider()
		return t
	}
	// OTEL_SERVICE_NAME and OTEL_RESOURCE_ATTRIBUTES override the default
	// service name.
	res, err := resource.Merge(
		resource.NewWithAttributes(semconv.SchemaURL, semconv.ServiceName("terraform-provider-beenfar")),
		resource.Environment())
	if err != nil {
		res = resource.Default()
	}
	opts := []sdktrace.TracerProviderOption{
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.AlwaysSample())),
		sdktrace.WithResource(res),
	}
	if os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") != "" || os.Getenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT") != "" {
		if exp, err := otlptracehttp.New(ctx); err == nil {
			opts = append(opts, sdktrace.WithSyncer(exp))
		}
	}
	t.TracerProvider = sdktrace.NewTracerProvider(opts...)
	return t
}
