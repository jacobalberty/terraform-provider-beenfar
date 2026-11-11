package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	configv1 "github.com/jacobalberty/beenfar/api/gen/go/beenfar/config/v1"
	convgen "github.com/jacobalberty/terraform-provider-beenfar/internal/conv/gen"
	"github.com/jacobalberty/terraform-provider-beenfar/internal/model"
)

const partialNote = "\n\nOnly the attributes set in the configuration are managed: the others are " +
	"neither changed by Terraform nor reported as drift, so they can be managed in the beenfar UI. " +
	"Destroying the resource deletes it entirely."

func idAttribute() schema.StringAttribute {
	return schema.StringAttribute{
		MarkdownDescription: "Server-assigned identifier. Import uses it.",
		Computed:            true,
		PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
	}
}

func optionalString(desc string, v ...validator.String) schema.StringAttribute {
	return schema.StringAttribute{MarkdownDescription: desc, Optional: true, Validators: v}
}

func optionalBool(desc string) schema.BoolAttribute {
	return schema.BoolAttribute{MarkdownDescription: desc, Optional: true}
}

func optionalInt(desc string, v ...validator.Int64) schema.Int64Attribute {
	return schema.Int64Attribute{MarkdownDescription: desc, Optional: true, Validators: v}
}

func optionalStringSet(desc string) schema.SetAttribute {
	return schema.SetAttribute{
		MarkdownDescription: desc, Optional: true, ElementType: types.StringType,
		Validators: []validator.Set{setvalidator.ValueStringsAre(stringvalidator.LengthAtLeast(1))},
	}
}

// --- beenfar_network ---

func newNetworkResource() resource.Resource {
	return &crud[model.Network, *configv1.Network]{
		typeName: "_network",
		what:     "network",
		schema: schema.Schema{
			MarkdownDescription: "A network: a VLAN and its IPv4 subnet." + partialNote,
			Attributes: map[string]schema.Attribute{
				"id":   idAttribute(),
				"name": schema.StringAttribute{MarkdownDescription: "Display name, 1-64 bytes; unique.", Required: true, Validators: []validator.String{stringvalidator.LengthBetween(1, 64)}},
				"vlan_id": optionalInt("802.1Q VLAN id, 1-4094, or 0 for untagged; unique when tagged.",
					int64validator.Between(0, 4094)),
				"subnet":       optionalString("Gateway address and prefix length, e.g. `192.168.10.1/24`."),
				"dhcp_enabled": optionalBool("Serve DHCP; needs `subnet`, `dhcp_start` and `dhcp_stop`."),
				"dhcp_start":   optionalString("First address of the DHCP pool."),
				"dhcp_stop":    optionalString("Last address of the DHCP pool."),
				"domain_name":  optionalString("DNS domain handed to clients."),
			},
		},
		toModel:   convgen.NetworkToModel,
		fromModel: convgen.NetworkFromModel,
		idOf:      func(m model.Network) string { return m.ID.ValueString() },
		setID:     func(p *configv1.Network, id string) { p.Id = id },
		etagOf:    func(p *configv1.Network) string { return p.GetEtag() },
		create: func(ctx context.Context, c *Client, p *configv1.Network) (*configv1.Network, error) {
			r, err := c.Networks.CreateNetwork(ctx, &configv1.CreateNetworkRequest{Network: p})
			return r.GetNetwork(), err
		},
		get: func(ctx context.Context, c *Client, id string) (*configv1.Network, error) {
			r, err := c.Networks.GetNetwork(ctx, &configv1.GetNetworkRequest{Id: id})
			return r.GetNetwork(), err
		},
		update: func(ctx context.Context, c *Client, p *configv1.Network, etag string, mask *fieldmaskpb.FieldMask) (*configv1.Network, error) {
			p.Etag = etag
			r, err := c.Networks.UpdateNetwork(ctx, &configv1.UpdateNetworkRequest{Network: p, UpdateMask: mask})
			return r.GetNetwork(), err
		},
		del: func(ctx context.Context, c *Client, id string) error {
			_, err := c.Networks.DeleteNetwork(ctx, &configv1.DeleteNetworkRequest{Id: id})
			return err
		},
	}
}

// --- beenfar_wifi_network ---

func newWifiNetworkResource() resource.Resource {
	return &crud[model.WifiNetwork, *configv1.WifiNetwork]{
		typeName: "_wifi_network",
		what:     "wifi network",
		schema: schema.Schema{
			MarkdownDescription: "A wireless network (SSID)." + partialNote +
				"\n\nTo leave the access point associations to the UI, do not set `ap_groups`. To manage " +
				"them from another configuration, use `beenfar_wifi_network_ap_groups` there.",
			Attributes: map[string]schema.Attribute{
				"id":   idAttribute(),
				"ssid": schema.StringAttribute{MarkdownDescription: "Network name, 1-32 bytes; unique.", Required: true, Validators: []validator.String{stringvalidator.LengthBetween(1, 32)}},
				"security": schema.StringAttribute{
					MarkdownDescription: "One of `open`, `wep`, `wpa_personal`, `wpa_enterprise`.",
					Required:            true,
					Validators:          []validator.String{stringvalidator.OneOf("open", "wep", "wpa_personal", "wpa_enterprise")},
				},
				"security_key": schema.StringAttribute{
					MarkdownDescription: "Pre-shared key for `wpa_personal` (8-63 characters or 64 hex digits) or `wep`. " +
						"The controller returns it, so a change made in the UI shows as drift.",
					Optional: true, Sensitive: true,
				},
				"band": optionalString("Radio bands: `both` (the controller's default), `2g` or `5g`.",
					stringvalidator.OneOf("both", "2g", "5g")),
				"network_id": optionalString("Id of the `beenfar_network` clients join; empty for the default network."),
				"guest":      optionalBool("Guest network."),
				"hidden":     optionalBool("Do not broadcast the SSID."),
				"ap_groups": optionalStringSet("Access point groups that broadcast the network; empty means all. " +
					"Leave unset to manage the associations in the UI."),
				"default_user_group": optionalString("Default user group."),
				"radius_server_id":   optionalString("Id of the `beenfar_radius_server`; required for `wpa_enterprise`."),
			},
		},
		toModel:   convgen.WifiNetworkToModel,
		fromModel: convgen.WifiNetworkFromModel,
		idOf:      func(m model.WifiNetwork) string { return m.ID.ValueString() },
		setID:     func(p *configv1.WifiNetwork, id string) { p.Id = id },
		etagOf:    func(p *configv1.WifiNetwork) string { return p.GetEtag() },
		create: func(ctx context.Context, c *Client, p *configv1.WifiNetwork) (*configv1.WifiNetwork, error) {
			r, err := c.Wifi.CreateWifiNetwork(ctx, &configv1.CreateWifiNetworkRequest{WifiNetwork: p})
			return r.GetWifiNetwork(), err
		},
		get: func(ctx context.Context, c *Client, id string) (*configv1.WifiNetwork, error) {
			r, err := c.Wifi.GetWifiNetwork(ctx, &configv1.GetWifiNetworkRequest{Id: id})
			return r.GetWifiNetwork(), err
		},
		update: func(ctx context.Context, c *Client, p *configv1.WifiNetwork, etag string, mask *fieldmaskpb.FieldMask) (*configv1.WifiNetwork, error) {
			p.Etag = etag
			r, err := c.Wifi.UpdateWifiNetwork(ctx, &configv1.UpdateWifiNetworkRequest{WifiNetwork: p, UpdateMask: mask})
			return r.GetWifiNetwork(), err
		},
		del: func(ctx context.Context, c *Client, id string) error {
			_, err := c.Wifi.DeleteWifiNetwork(ctx, &configv1.DeleteWifiNetworkRequest{Id: id})
			return err
		},
	}
}
