package provider

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"go.opentelemetry.io/otel/codes"

	configv1 "github.com/jacobalberty/beenfar/api/gen/go/beenfar/config/v1"
	convgen "github.com/jacobalberty/terraform-provider-beenfar/internal/conv/gen"
	"github.com/jacobalberty/terraform-provider-beenfar/internal/model"
)

// macPattern is the controller's canonical MAC form, so that the configured
// value and the reported one never differ in spelling.
var macPattern = regexp.MustCompile(`^[0-9a-f]{2}(:[0-9a-f]{2}){5}$`)

// pollInterval is how often Create looks for a device that has not
// contacted the controller yet.
var pollInterval = 2 * time.Second

// deviceResource adopts a device: create adopts it, destroy forgets it.
type deviceResource struct {
	client *Client
}

var (
	_ resource.ResourceWithImportState = (*deviceResource)(nil)
	_ resource.ResourceWithModifyPlan  = (*deviceResource)(nil)
)

func newDeviceResource() resource.Resource { return &deviceResource{} }

func (r *deviceResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_device"
}

func (r *deviceResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	computed := func(desc string) schema.StringAttribute {
		return schema.StringAttribute{MarkdownDescription: desc, Computed: true}
	}
	resp.Schema = schema.Schema{
		MarkdownDescription: "A device adopted by the controller. Creating it adopts the device, which must " +
			"have contacted the controller (it is then listed by `beenfar_pending_devices`); destroying it " +
			"forgets the device.\n\n" +
			"A device that was factory reset comes back awaiting re-adoption. Terraform then plans to " +
			"create the resource again, and applying accepts the re-adoption: the device keeps its id and " +
			"its switch port settings. Creating the resource for a device that is already adopted fails; " +
			"import it instead (by id or MAC).",
		Attributes: map[string]schema.Attribute{
			"id": idAttribute(),
			"mac": schema.StringAttribute{
				MarkdownDescription: "MAC address, lower case and colon-separated (`aa:bb:cc:dd:ee:ff`).",
				Required:            true,
				Validators:          []validator.String{stringvalidator.RegexMatches(macPattern, "must be lower case and colon-separated, e.g. aa:bb:cc:dd:ee:ff")},
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"wait_for_device": schema.StringAttribute{
				MarkdownDescription: "How long create waits for the device to contact the controller, as a Go " +
					"duration (e.g. `5m`). Unset or `0s`: the device must already be known.",
				Optional: true,
				Validators: []validator.String{stringvalidator.RegexMatches(regexp.MustCompile(`^([0-9]+(\.[0-9]+)?(ns|us|µs|ms|s|m|h))+$`),
					"must be a Go duration such as 30s or 5m")},
			},
			"adapter":  computed("Adapter that manages the device, e.g. `unifi`."),
			"model":    computed("Model the device reported."),
			"ip":       computed("IP address the device reported."),
			"firmware": computed("Firmware version the device reported."),
		},
	}
}

func (r *deviceResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = clientFrom(req.ProviderData, &resp.Diagnostics)
}

// findByMAC returns the device with mac, or nil.
func (r *deviceResource) findByMAC(ctx context.Context, mac string) (*configv1.Device, error) {
	res, err := r.client.Devices.ListDevices(ctx, &configv1.ListDevicesRequest{})
	if err != nil {
		return nil, err
	}
	for _, d := range res.GetDevices() {
		if strings.EqualFold(d.GetMac(), mac) {
			return d, nil
		}
	}
	return nil, nil
}

func (r *deviceResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	if notConfigured(r.client, &resp.Diagnostics) {
		return
	}
	ctx, span := r.client.span(ctx, "beenfar_device.create")
	defer span.End()
	var plan model.AdoptedDevice
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	mac := plan.Mac.ValueString()
	var wait time.Duration
	if s := plan.WaitForDevice.ValueString(); s != "" {
		var err error
		if wait, err = time.ParseDuration(s); err != nil {
			resp.Diagnostics.AddAttributeError(path.Root("wait_for_device"), "Invalid duration", err.Error())
			return
		}
	}

	deadline := time.Now().Add(wait)
	var d *configv1.Device
	for {
		var err error
		if d, err = r.findByMAC(ctx, mac); err != nil {
			resp.Diagnostics.AddError("Cannot list devices", apiError(err))
			return
		}
		if d != nil || !time.Now().Before(deadline) {
			break
		}
		select {
		case <-ctx.Done():
			resp.Diagnostics.AddError("Cancelled", ctx.Err().Error())
			return
		case <-time.After(pollInterval):
		}
	}
	if d == nil {
		resp.Diagnostics.AddAttributeError(path.Root("mac"), "Device not found",
			fmt.Sprintf("No device with MAC %s has contacted the controller. Point it at the controller "+
				"(for UniFi, its /inform URL) and try again, or set wait_for_device.", mac))
		return
	}
	if err := adoptable(d); err != nil {
		resp.Diagnostics.AddAttributeError(path.Root("mac"), "Device cannot be adopted", err.Error())
		return
	}
	res, err := r.client.Devices.AdoptDevice(ctx, &configv1.AdoptDeviceRequest{Device: &configv1.AdoptDeviceRequest_Id{Id: d.GetId()}})
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		resp.Diagnostics.AddError("Cannot adopt the device", apiError(err))
		return
	}
	state := convgen.AdoptedDeviceToModel(res.GetDevice())
	state.WaitForDevice = plan.WaitForDevice
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// ModifyPlan plans the id of a device that is about to be adopted when the
// controller already knows it. Adoption keeps a device's id, so resources
// that refer to it (such as switch ports) are not replaced when a reset
// device is adopted again.
func (r *deviceResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if r.client == nil || !req.State.Raw.IsNull() || req.Plan.Raw.IsNull() {
		return // not a create, or the provider is not configured yet
	}
	var mac types.String
	resp.Diagnostics.Append(req.Plan.GetAttribute(ctx, path.Root("mac"), &mac)...)
	if mac.IsNull() || mac.IsUnknown() {
		return
	}
	d, err := r.findByMAC(ctx, mac.ValueString())
	if err != nil || d == nil {
		return // Create reports it
	}
	resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("id"), d.GetId())...)
	resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("adapter"), d.GetAdapter())...)
}

// adoptable explains why d cannot be adopted by creating the resource. A
// pending device can, and so can an adopted one that was reset and awaits
// re-adoption: adopting it accepts the re-adoption.
func adoptable(d *configv1.Device) error {
	switch {
	case d.GetState() == configv1.DeviceState_DEVICE_STATE_PENDING:
		return nil
	case d.GetReadoption() == configv1.ReadoptionState_READOPTION_STATE_AWAITING_ACCEPTANCE:
		return nil
	}
	return fmt.Errorf("device %s (%s) is already adopted. To manage it with Terraform, import it: "+
		"terraform import <address> %s", d.GetMac(), d.GetId(), d.GetId())
}

func (r *deviceResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	if notConfigured(r.client, &resp.Diagnostics) {
		return
	}
	ctx, span := r.client.span(ctx, "beenfar_device.read")
	defer span.End()
	var prior model.AdoptedDevice
	resp.Diagnostics.Append(req.State.Get(ctx, &prior)...)
	if resp.Diagnostics.HasError() {
		return
	}
	res, err := r.client.Devices.GetDevice(ctx, &configv1.GetDeviceRequest{Id: prior.ID.ValueString()})
	if connect.CodeOf(err) == connect.CodeNotFound {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		resp.Diagnostics.AddError("Cannot read the device", apiError(err))
		return
	}
	d := res.GetDevice()
	if d.GetState() != configv1.DeviceState_DEVICE_STATE_ADOPTED ||
		d.GetReadoption() == configv1.ReadoptionState_READOPTION_STATE_AWAITING_ACCEPTANCE {
		// A reset device awaits re-adoption: plan to create the resource
		// again, which accepts it.
		resp.Diagnostics.AddWarning("Device needs adopting",
			fmt.Sprintf("Device %s (%s) is no longer adopted or was reset and awaits re-adoption; "+
				"the next apply adopts it again.", d.GetMac(), d.GetId()))
		resp.State.RemoveResource(ctx)
		return
	}
	state := convgen.AdoptedDeviceToModel(d)
	state.WaitForDevice = prior.WaitForDevice
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// Update only sees changes to wait_for_device; mac forces replacement.
func (r *deviceResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	if notConfigured(r.client, &resp.Diagnostics) {
		return
	}
	var plan, state model.AdoptedDevice
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	state.WaitForDevice = plan.WaitForDevice
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *deviceResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	if notConfigured(r.client, &resp.Diagnostics) {
		return
	}
	ctx, span := r.client.span(ctx, "beenfar_device.delete")
	defer span.End()
	var state model.AdoptedDevice
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	_, err := r.client.Devices.ForgetDevice(ctx, &configv1.ForgetDeviceRequest{Id: state.ID.ValueString()})
	if err != nil && connect.CodeOf(err) != connect.CodeNotFound {
		span.SetStatus(codes.Error, err.Error())
		resp.Diagnostics.AddError("Cannot forget the device", apiError(err))
	}
}

// ImportState accepts a device id or a MAC address.
func (r *deviceResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if notConfigured(r.client, &resp.Diagnostics) {
		return
	}
	id := req.ID
	if strings.Contains(id, ":") {
		d, err := r.findByMAC(ctx, strings.ToLower(id))
		if err != nil {
			resp.Diagnostics.AddError("Cannot list devices", apiError(err))
			return
		}
		if d == nil {
			resp.Diagnostics.AddError("Device not found", "No device has MAC "+id+".")
			return
		}
		id = d.GetId()
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), id)...)
}
