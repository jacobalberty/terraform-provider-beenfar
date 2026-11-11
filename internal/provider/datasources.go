package provider

import (
	"context"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/datasourcevalidator"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"go.opentelemetry.io/otel/codes"

	adminv1 "github.com/jacobalberty/beenfar/api/gen/go/beenfar/admin/v1"
	configv1 "github.com/jacobalberty/beenfar/api/gen/go/beenfar/config/v1"
	convgen "github.com/jacobalberty/terraform-provider-beenfar/internal/conv/gen"
	"github.com/jacobalberty/terraform-provider-beenfar/internal/model"
)

// dataSource is the shared plumbing of the provider's data sources.
type dataSource struct {
	typeName string
	schema   schema.Schema
	client   *Client
	read     func(context.Context, *dataSource, datasource.ReadRequest, *datasource.ReadResponse)
	validate []datasource.ConfigValidator
}

var _ datasource.DataSourceWithConfigValidators = (*dataSource)(nil)

func (d *dataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + d.typeName
}

func (d *dataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = d.schema
}

func (d *dataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = clientFrom(req.ProviderData, &resp.Diagnostics)
}

func (d *dataSource) ConfigValidators(context.Context) []datasource.ConfigValidator {
	return d.validate
}

func (d *dataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	if notConfigured(d.client, &resp.Diagnostics) {
		return
	}
	ctx, span := d.client.span(ctx, "data.beenfar"+d.typeName+".read")
	defer span.End()
	d.read(ctx, d, req, resp)
	if resp.Diagnostics.HasError() {
		span.SetStatus(codes.Error, "read failed")
	}
}

func computedString(desc string) schema.StringAttribute {
	return schema.StringAttribute{MarkdownDescription: desc, Computed: true}
}

func deviceAttributes() map[string]schema.Attribute {
	return map[string]schema.Attribute{
		"id":         computedString("Device id."),
		"mac":        computedString("MAC address."),
		"adapter":    computedString("Adapter that handles the device."),
		"state":      computedString("`pending` or `adopted`."),
		"model":      computedString("Model the device reported."),
		"ip":         computedString("IP address the device reported."),
		"firmware":   computedString("Firmware version the device reported."),
		"locked":     schema.BoolAttribute{MarkdownDescription: "The adapter has no credentials for the device yet.", Computed: true},
		"readoption": computedString("`awaiting_acceptance` or `in_progress` while a reset device is adopted again; empty otherwise."),
	}
}

// --- beenfar_pending_devices ---

type pendingDevicesModel struct {
	Adapter types.String   `tfsdk:"adapter"`
	Devices []model.Device `tfsdk:"devices"`
}

func newPendingDevicesDataSource() datasource.DataSource {
	return &dataSource{
		typeName: "_pending_devices",
		schema: schema.Schema{
			MarkdownDescription: "Devices that contacted the controller and wait to be adopted with `beenfar_device`.",
			Attributes: map[string]schema.Attribute{
				"adapter": schema.StringAttribute{MarkdownDescription: "Only devices of this adapter, e.g. `unifi`.", Optional: true},
				"devices": schema.ListNestedAttribute{
					MarkdownDescription: "The pending devices.",
					Computed:            true,
					NestedObject:        schema.NestedAttributeObject{Attributes: deviceAttributes()},
				},
			},
		},
		read: func(ctx context.Context, d *dataSource, req datasource.ReadRequest, resp *datasource.ReadResponse) {
			var cfg pendingDevicesModel
			resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
			if resp.Diagnostics.HasError() {
				return
			}
			res, err := d.client.Devices.ListPendingDevices(ctx, &configv1.ListPendingDevicesRequest{Adapter: cfg.Adapter.ValueString()})
			if err != nil {
				resp.Diagnostics.AddError("Cannot list pending devices", apiError(err))
				return
			}
			cfg.Devices = convgen.DevicesToModel(res.GetDevices())
			if cfg.Devices == nil {
				cfg.Devices = []model.Device{}
			}
			resp.Diagnostics.Append(resp.State.Set(ctx, &cfg)...)
		},
	}
}

// --- beenfar_adapters ---

type adaptersModel struct {
	Adapters []model.Adapter `tfsdk:"adapters"`
}

func newAdaptersDataSource() datasource.DataSource {
	return &dataSource{
		typeName: "_adapters",
		schema: schema.Schema{
			MarkdownDescription: "The device adapters registered with the controller. Needs an admin's API token.",
			Attributes: map[string]schema.Attribute{
				"adapters": schema.ListNestedAttribute{
					MarkdownDescription: "Registered adapters, by name.",
					Computed:            true,
					NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
						"name":           computedString("Adapter name, e.g. `unifi`."),
						"kind":           computedString("`builtin` or `external`."),
						"version":        computedString("Adapter version."),
						"healthy":        schema.BoolAttribute{MarkdownDescription: "Whether the adapter is connected and healthy.", Computed: true},
						"status_message": computedString("Why the adapter is unhealthy, if it is."),
					}},
				},
			},
		},
		read: func(ctx context.Context, d *dataSource, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
			res, err := d.client.Adapters.ListAdapters(ctx, &adminv1.ListAdaptersRequest{})
			if err != nil {
				resp.Diagnostics.AddError("Cannot list adapters", apiError(err))
				return
			}
			out := adaptersModel{Adapters: []model.Adapter{}}
			for _, a := range res.GetAdapters() {
				out.Adapters = append(out.Adapters, convgen.AdapterToModel(a))
			}
			resp.Diagnostics.Append(resp.State.Set(ctx, &out)...)
		},
	}
}

// --- beenfar_device ---

func newDeviceDataSource() datasource.DataSource {
	attrs := deviceAttributes()
	attrs["id"] = schema.StringAttribute{MarkdownDescription: "Device id. Set exactly one of `id` and `mac`.", Optional: true, Computed: true}
	attrs["mac"] = schema.StringAttribute{MarkdownDescription: "MAC address. Set exactly one of `id` and `mac`.", Optional: true, Computed: true}
	return &dataSource{
		typeName: "_device",
		schema:   schema.Schema{MarkdownDescription: "Looks up a device, pending or adopted, by id or MAC address.", Attributes: attrs},
		validate: []datasource.ConfigValidator{datasourcevalidator.ExactlyOneOf(path.MatchRoot("id"), path.MatchRoot("mac"))},
		read: func(ctx context.Context, d *dataSource, req datasource.ReadRequest, resp *datasource.ReadResponse) {
			var cfg model.Device
			resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
			if resp.Diagnostics.HasError() {
				return
			}
			var found *configv1.Device
			if id := cfg.ID.ValueString(); id != "" {
				res, err := d.client.Devices.GetDevice(ctx, &configv1.GetDeviceRequest{Id: id})
				if err != nil {
					resp.Diagnostics.AddError("Cannot read device "+id, apiError(err))
					return
				}
				found = res.GetDevice()
			} else {
				res, err := d.client.Devices.ListDevices(ctx, &configv1.ListDevicesRequest{})
				if err != nil {
					resp.Diagnostics.AddError("Cannot list devices", apiError(err))
					return
				}
				for _, dev := range res.GetDevices() {
					if strings.EqualFold(dev.GetMac(), cfg.Mac.ValueString()) {
						found = dev
					}
				}
				if found == nil {
					resp.Diagnostics.AddError("Device not found", "No device has MAC "+cfg.Mac.ValueString()+".")
					return
				}
			}
			out := convgen.DeviceToModel(found)
			resp.Diagnostics.Append(resp.State.Set(ctx, &out)...)
		},
	}
}

// --- beenfar_network ---

func newNetworkDataSource() datasource.DataSource {
	return &dataSource{
		typeName: "_network",
		schema: schema.Schema{
			MarkdownDescription: "Looks up a network by id or name, e.g. one managed in the UI.",
			Attributes: map[string]schema.Attribute{
				"id":           schema.StringAttribute{MarkdownDescription: "Network id. Set exactly one of `id` and `name`.", Optional: true, Computed: true},
				"name":         schema.StringAttribute{MarkdownDescription: "Network name. Set exactly one of `id` and `name`.", Optional: true, Computed: true},
				"vlan_id":      schema.Int64Attribute{MarkdownDescription: "802.1Q VLAN id; 0 when untagged.", Computed: true},
				"subnet":       computedString("Gateway address and prefix length."),
				"dhcp_enabled": schema.BoolAttribute{MarkdownDescription: "Whether DHCP is served.", Computed: true},
				"dhcp_start":   computedString("First address of the DHCP pool."),
				"dhcp_stop":    computedString("Last address of the DHCP pool."),
				"domain_name":  computedString("DNS domain handed to clients."),
			},
		},
		validate: []datasource.ConfigValidator{datasourcevalidator.ExactlyOneOf(path.MatchRoot("id"), path.MatchRoot("name"))},
		read: func(ctx context.Context, d *dataSource, req datasource.ReadRequest, resp *datasource.ReadResponse) {
			var cfg model.Network
			resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
			if resp.Diagnostics.HasError() {
				return
			}
			res, err := d.client.Networks.ListNetworks(ctx, &configv1.ListNetworksRequest{})
			if err != nil {
				resp.Diagnostics.AddError("Cannot list networks", apiError(err))
				return
			}
			for _, n := range res.GetNetworks() {
				if n.GetId() == cfg.ID.ValueString() || n.GetName() == cfg.Name.ValueString() {
					out := convgen.NetworkToModel(n)
					resp.Diagnostics.Append(resp.State.Set(ctx, &out)...)
					return
				}
			}
			resp.Diagnostics.AddError("Network not found", "No network has that id or name.")
		},
	}
}

// --- beenfar_radius_server ---

type radiusServerDataModel struct {
	ID                  types.String `tfsdk:"id"`
	Name                types.String `tfsdk:"name"`
	Address             types.String `tfsdk:"address"`
	AuthPort            types.Int64  `tfsdk:"auth_port"`
	AccountingPort      types.Int64  `tfsdk:"accounting_port"`
	SharedSecretVersion types.Int64  `tfsdk:"shared_secret_version"`
}

func newRadiusServerDataSource() datasource.DataSource {
	return &dataSource{
		typeName: "_radius_server",
		schema: schema.Schema{
			MarkdownDescription: "Looks up a RADIUS server by id or name. The shared secret is never returned.",
			Attributes: map[string]schema.Attribute{
				"id":                    schema.StringAttribute{MarkdownDescription: "Server id. Set exactly one of `id` and `name`.", Optional: true, Computed: true},
				"name":                  schema.StringAttribute{MarkdownDescription: "Server name. Set exactly one of `id` and `name`.", Optional: true, Computed: true},
				"address":               computedString("IP address of the server."),
				"auth_port":             schema.Int64Attribute{MarkdownDescription: "Authentication port.", Computed: true},
				"accounting_port":       schema.Int64Attribute{MarkdownDescription: "Accounting port; 0 means no accounting.", Computed: true},
				"shared_secret_version": schema.Int64Attribute{MarkdownDescription: "Increases whenever the shared secret changes.", Computed: true},
			},
		},
		validate: []datasource.ConfigValidator{datasourcevalidator.ExactlyOneOf(path.MatchRoot("id"), path.MatchRoot("name"))},
		read: func(ctx context.Context, d *dataSource, req datasource.ReadRequest, resp *datasource.ReadResponse) {
			var cfg radiusServerDataModel
			resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
			if resp.Diagnostics.HasError() {
				return
			}
			res, err := d.client.Radius.ListRadiusServers(ctx, &configv1.ListRadiusServersRequest{})
			if err != nil {
				resp.Diagnostics.AddError("Cannot list RADIUS servers", apiError(err))
				return
			}
			for _, r := range res.GetRadiusServers() {
				if r.GetId() == cfg.ID.ValueString() || r.GetName() == cfg.Name.ValueString() {
					m := convgen.RadiusServerToModel(r)
					out := radiusServerDataModel{
						ID: m.ID, Name: m.Name, Address: m.Address, AuthPort: m.AuthPort,
						AccountingPort: m.AccountingPort, SharedSecretVersion: m.SharedSecretVersion,
					}
					resp.Diagnostics.Append(resp.State.Set(ctx, &out)...)
					return
				}
			}
			resp.Diagnostics.AddError("RADIUS server not found", "No RADIUS server has that id or name.")
		},
	}
}
