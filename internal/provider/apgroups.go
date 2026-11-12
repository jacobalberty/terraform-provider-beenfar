package provider

import (
	"context"

	"connectrpc.com/connect/v2"
	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"go.opentelemetry.io/otel/codes"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	configv1 "github.com/jacobalberty/beenfar/api/gen/go/beenfar/config/v1"
	"github.com/jacobalberty/terraform-provider-beenfar/internal/conv"
	"github.com/jacobalberty/terraform-provider-beenfar/internal/model"
)

// wifiAPGroupsResource manages only the ap_groups field of a wifi network,
// through the same masked UpdateWifiNetwork the UI uses. It lets one
// configuration own a wifi network's access point associations while
// another (or the UI) owns the rest of it.
type wifiAPGroupsResource struct {
	client *Client
}

var _ resource.ResourceWithImportState = (*wifiAPGroupsResource)(nil)

func newWifiNetworkAPGroupsResource() resource.Resource { return &wifiAPGroupsResource{} }

func (r *wifiAPGroupsResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_wifi_network_ap_groups"
}

func (r *wifiAPGroupsResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "The access point groups that broadcast a wifi network, managed apart from the " +
			"network itself. Use it when the `beenfar_wifi_network` is managed elsewhere (another " +
			"configuration, or the UI); do not also set `ap_groups` on that resource.\n\n" +
			"Destroying it clears the groups, so the network is broadcast by all access points again; the " +
			"wifi network itself is not touched.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Same as `wifi_network_id`. Import uses it.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"wifi_network_id": schema.StringAttribute{
				MarkdownDescription: "Id of the wifi network.",
				Required:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"ap_groups": schema.SetAttribute{
				MarkdownDescription: "Access point groups that broadcast the network; empty means all.",
				Required:            true,
				ElementType:         types.StringType,
				Validators:          []validator.Set{setvalidator.ValueStringsAre(stringvalidator.LengthAtLeast(1))},
			},
		},
	}
}

func (r *wifiAPGroupsResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = clientFrom(req.ProviderData, &resp.Diagnostics)
}

// set writes the groups with a mask naming only ap_groups. It is not guarded
// by an etag: this resource owns that one field, whatever else changes.
func (r *wifiAPGroupsResource) set(ctx context.Context, id string, groups []string) (*configv1.WifiNetwork, error) {
	res, err := r.client.Wifi.UpdateWifiNetwork(ctx, &configv1.UpdateWifiNetworkRequest{
		WifiNetwork: &configv1.WifiNetwork{Id: id, ApGroups: groups},
		UpdateMask:  &fieldmaskpb.FieldMask{Paths: []string{"ap_groups"}},
	})
	return res.GetWifiNetwork(), err
}

func (r *wifiAPGroupsResource) write(ctx context.Context, op string, plan model.WifiNetworkAPGroups) (model.WifiNetworkAPGroups, error) {
	ctx, span := r.client.span(ctx, "beenfar_wifi_network_ap_groups."+op)
	defer span.End()
	w, err := r.set(ctx, plan.WifiNetworkID.ValueString(), conv.FromStringSet(plan.ApGroups))
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		return plan, err
	}
	return model.WifiNetworkAPGroups{
		ID:            types.StringValue(w.GetId()),
		WifiNetworkID: plan.WifiNetworkID,
		ApGroups:      conv.StringSet(w.GetApGroups()),
	}, nil
}

func (r *wifiAPGroupsResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	if notConfigured(r.client, &resp.Diagnostics) {
		return
	}
	var plan model.WifiNetworkAPGroups
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	state, err := r.write(ctx, "create", plan)
	if err != nil {
		resp.Diagnostics.AddError("Cannot set the wifi network's AP groups", apiError(err))
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *wifiAPGroupsResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	if notConfigured(r.client, &resp.Diagnostics) {
		return
	}
	var plan model.WifiNetworkAPGroups
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	state, err := r.write(ctx, "update", plan)
	if err != nil {
		resp.Diagnostics.AddError("Cannot set the wifi network's AP groups", apiError(err))
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *wifiAPGroupsResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	if notConfigured(r.client, &resp.Diagnostics) {
		return
	}
	ctx, span := r.client.span(ctx, "beenfar_wifi_network_ap_groups.read")
	defer span.End()
	var state model.WifiNetworkAPGroups
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id := state.WifiNetworkID.ValueString()
	if id == "" {
		id = state.ID.ValueString() // just imported
	}
	res, err := r.client.Wifi.GetWifiNetwork(ctx, &configv1.GetWifiNetworkRequest{Id: id})
	if connect.CodeOf(err) == connect.CodeNotFound {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		resp.Diagnostics.AddError("Cannot read the wifi network", apiError(err))
		return
	}
	w := res.GetWifiNetwork()
	state = model.WifiNetworkAPGroups{
		ID:            types.StringValue(w.GetId()),
		WifiNetworkID: types.StringValue(w.GetId()),
		ApGroups:      conv.StringSet(w.GetApGroups()),
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *wifiAPGroupsResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	if notConfigured(r.client, &resp.Diagnostics) {
		return
	}
	ctx, span := r.client.span(ctx, "beenfar_wifi_network_ap_groups.delete")
	defer span.End()
	var state model.WifiNetworkAPGroups
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	_, err := r.set(ctx, state.WifiNetworkID.ValueString(), nil)
	if err != nil && connect.CodeOf(err) != connect.CodeNotFound {
		span.SetStatus(codes.Error, err.Error())
		resp.Diagnostics.AddError("Cannot clear the wifi network's AP groups", apiError(err))
	}
}

func (r *wifiAPGroupsResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}
