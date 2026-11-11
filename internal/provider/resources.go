package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
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
