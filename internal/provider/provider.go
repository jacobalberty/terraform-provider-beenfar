// Package provider implements the beenfar Terraform provider.
package provider

import (
	"context"
	"net/http"
	"net/url"
	"os"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ provider.Provider = (*beenfarProvider)(nil)

type beenfarProvider struct {
	version string
	// httpClient, when set, replaces http.DefaultClient (tests).
	httpClient *http.Client
}

// New returns a constructor for the provider.
func New(version string) func() provider.Provider {
	return func() provider.Provider { return &beenfarProvider{version: version} }
}

type providerModel struct {
	URL   types.String `tfsdk:"url"`
	Token types.String `tfsdk:"token"`
}

func (p *beenfarProvider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "beenfar"
	resp.Version = p.version
}

func (p *beenfarProvider) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages a [beenfar](https://github.com/jacobalberty/beenfar) network controller: " +
			"networks, wifi networks, RADIUS servers, switch ports and device adoption.\n\n" +
			"Resources only manage the attributes set in their configuration. Attributes left out are " +
			"neither changed nor reported as drift, so the same resource can be managed partly by " +
			"Terraform and partly by the beenfar UI.",
		Attributes: map[string]schema.Attribute{
			"url": schema.StringAttribute{
				MarkdownDescription: "Base URL of the controller, e.g. `https://beenfar.example:8080`. " +
					"Defaults to the `BEENFAR_URL` environment variable.",
				Optional: true,
			},
			"token": schema.StringAttribute{
				MarkdownDescription: "API token, sent as `Authorization: Bearer`. Create one with " +
					"`beenfard admin token`. Defaults to the `BEENFAR_TOKEN` environment variable. " +
					"The `beenfar_adapters` data source needs an admin's token.",
				Optional:  true,
				Sensitive: true,
			},
		},
	}
}

func (p *beenfarProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var cfg providerModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if cfg.URL.IsUnknown() || cfg.Token.IsUnknown() {
		// Known only after apply; resources are configured on a later pass.
		return
	}
	baseURL := cfg.URL.ValueString()
	if baseURL == "" {
		baseURL = os.Getenv("BEENFAR_URL")
	}
	token := cfg.Token.ValueString()
	if token == "" {
		token = os.Getenv("BEENFAR_TOKEN")
	}
	if baseURL == "" {
		resp.Diagnostics.AddAttributeError(path.Root("url"), "Missing beenfar URL",
			"Set the provider's url attribute or the BEENFAR_URL environment variable.")
	} else if u, err := url.Parse(baseURL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		resp.Diagnostics.AddAttributeError(path.Root("url"), "Invalid beenfar URL",
			"The URL must be an absolute http or https URL, e.g. https://beenfar.example:8080.")
	}
	if token == "" {
		resp.Diagnostics.AddAttributeError(path.Root("token"), "Missing beenfar API token",
			"Set the provider's token attribute or the BEENFAR_TOKEN environment variable. "+
				"Create a token with `beenfard admin token`.")
	}
	if resp.Diagnostics.HasError() {
		return
	}
	httpClient := p.httpClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	client, err := NewClient(httpClient, baseURL, token, defaultTelemetry(ctx))
	if err != nil {
		resp.Diagnostics.AddError("Cannot create the beenfar client", err.Error())
		return
	}
	resp.ResourceData = client
	resp.DataSourceData = client
}

func (p *beenfarProvider) Resources(context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		newNetworkResource,
		newWifiNetworkResource,
	}
}

func (p *beenfarProvider) DataSources(context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{}
}
