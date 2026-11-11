package provider

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"connectrpc.com/otelconnect"
	"go.opentelemetry.io/otel/trace"

	"github.com/jacobalberty/beenfar/api/gen/go/beenfar/admin/v1/adminv1connect"
	"github.com/jacobalberty/beenfar/api/gen/go/beenfar/config/v1/configv1connect"
)

// Client holds the Connect clients of the beenfar services the provider uses.
type Client struct {
	Networks    configv1connect.NetworkServiceClient
	Wifi        configv1connect.WifiNetworkServiceClient
	Radius      configv1connect.RadiusServerServiceClient
	SwitchPorts configv1connect.SwitchPortServiceClient
	Devices     configv1connect.DeviceServiceClient
	Adapters    adminv1connect.AdapterServiceClient
	tracer      trace.Tracer
	parent      func(context.Context) context.Context
}

// NewClient returns a client for the controller at baseURL that
// authenticates with an API token. Every call goes through an otelconnect
// interceptor, so the controller's spans join the provider's traces.
func NewClient(httpClient *http.Client, baseURL, token string, tel Telemetry) (*Client, error) {
	tp, prop := tel.TracerProvider, tel.Propagator
	otelInterceptor, err := otelconnect.NewClientInterceptor(
		otelconnect.WithTracerProvider(tp),
		otelconnect.WithPropagator(prop),
	)
	if err != nil {
		return nil, fmt.Errorf("otelconnect: %w", err)
	}
	bearer := func(next connect.ClientFunc) connect.ClientFunc {
		return func(ctx context.Context, spec connect.Spec) (connect.ClientStream, error) {
			if info, ok := connect.CallInfoForClientContext(ctx); ok {
				info.RequestHeader().Set("Authorization", "Bearer "+token)
			}
			return next(ctx, spec)
		}
	}
	cc := connect.NewClient(connecthttp.NewTransport(httpClient, strings.TrimRight(baseURL, "/")), bearer, otelInterceptor)
	return &Client{
		Networks:    configv1connect.NewNetworkServiceClient(cc),
		Wifi:        configv1connect.NewWifiNetworkServiceClient(cc),
		Radius:      configv1connect.NewRadiusServerServiceClient(cc),
		SwitchPorts: configv1connect.NewSwitchPortServiceClient(cc),
		Devices:     configv1connect.NewDeviceServiceClient(cc),
		Adapters:    adminv1connect.NewAdapterServiceClient(cc),
		tracer:      tp.Tracer("github.com/jacobalberty/terraform-provider-beenfar"),
		parent:      tel.parent,
	}, nil
}

// span starts the span that groups the calls of one Terraform operation
// (for instance beenfar_network.update). When the environment carries a
// trace context (TRACEPARENT), the span continues that trace.
func (c *Client) span(ctx context.Context, name string) (context.Context, trace.Span) {
	if c.parent != nil {
		ctx = c.parent(ctx)
	}
	return c.tracer.Start(ctx, name)
}
