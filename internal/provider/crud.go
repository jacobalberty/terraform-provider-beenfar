package provider

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"connectrpc.com/connect/v2"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"go.opentelemetry.io/otel/codes"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
)

// Private state keys.
const (
	privEtag      = "etag"      // the resource's etag, as a JSON string
	privImporting = "importing" // set by ImportState until the next Read
)

// privateState is the part of the framework's private state API that
// requests and responses share.
type privateState interface {
	GetKey(ctx context.Context, key string) ([]byte, diag.Diagnostics)
	SetKey(ctx context.Context, key string, value []byte) diag.Diagnostics
}

func getEtag(ctx context.Context, p privateState) (string, diag.Diagnostics) {
	b, diags := p.GetKey(ctx, privEtag)
	if len(b) == 0 || diags.HasError() {
		return "", diags
	}
	s, err := strconv.Unquote(string(b))
	if err != nil {
		return "", diags
	}
	return s, diags
}

func setEtag(ctx context.Context, p privateState, etag string) diag.Diagnostics {
	return p.SetKey(ctx, privEtag, []byte(strconv.Quote(etag)))
}

// crud is a resource over one beenfar config collection whose Update takes
// an update_mask. M is the Terraform model, P the API message.
type crud[M any, P any] struct {
	typeName string // e.g. "_network"
	what     string // e.g. "network", for messages
	schema   schema.Schema
	client   *Client

	toModel   func(P) M
	fromModel func(M) (P, error)
	idOf      func(M) string
	setID     func(P, string)
	etagOf    func(P) string

	create func(context.Context, *Client, P) (P, error)
	get    func(context.Context, *Client, string) (P, error)
	update func(ctx context.Context, c *Client, msg P, etag string, mask *fieldmaskpb.FieldMask) (P, error)
	del    func(context.Context, *Client, string) error

	// afterRead, when set, adjusts the refreshed state given the prior
	// state and the controller's view (e.g. to notice a replaced secret).
	afterRead func(prior M, server M, out *M)
}

var (
	_ resource.ResourceWithConfigure   = (*crud[struct{}, struct{}])(nil)
	_ resource.ResourceWithImportState = (*crud[struct{}, struct{}])(nil)
)

func (r *crud[M, P]) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + r.typeName
}

func (r *crud[M, P]) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = r.schema
}

func (r *crud[M, P]) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = clientFrom(req.ProviderData, &resp.Diagnostics)
}

func (r *crud[M, P]) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	if notConfigured(r.client, &resp.Diagnostics) {
		return
	}
	ctx, span := r.client.span(ctx, "beenfar"+r.typeName+".create")
	defer span.End()
	var plan M
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	msg, err := r.fromModel(plan)
	if err != nil {
		resp.Diagnostics.AddError("Invalid "+r.what, err.Error())
		return
	}
	out, err := r.create(ctx, r.client, msg)
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		resp.Diagnostics.AddError("Cannot create "+r.what, apiError(err))
		return
	}
	state := refresh(ctx, plan, r.toModel(out), false)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
	resp.Diagnostics.Append(setEtag(ctx, resp.Private, r.etagOf(out))...)
}

func (r *crud[M, P]) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	if notConfigured(r.client, &resp.Diagnostics) {
		return
	}
	ctx, span := r.client.span(ctx, "beenfar"+r.typeName+".read")
	defer span.End()
	var prior M
	resp.Diagnostics.Append(req.State.Get(ctx, &prior)...)
	if resp.Diagnostics.HasError() {
		return
	}
	importing, diags := req.Private.GetKey(ctx, privImporting)
	resp.Diagnostics.Append(diags...)
	out, err := r.get(ctx, r.client, r.idOf(prior))
	if connect.CodeOf(err) == connect.CodeNotFound {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		resp.Diagnostics.AddError("Cannot read "+r.what, apiError(err))
		return
	}
	server := r.toModel(out)
	state := refresh(ctx, prior, server, len(importing) > 0)
	if r.afterRead != nil {
		r.afterRead(prior, server, &state)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
	resp.Diagnostics.Append(setEtag(ctx, resp.Private, r.etagOf(out))...)
	resp.Diagnostics.Append(resp.Private.SetKey(ctx, privImporting, nil)...)
}

// Update sends the managed attributes that changed, guarded by the etag of
// the last read. If the etag is stale because someone else changed the
// resource, the update is retried with the fresh etag as long as the
// attributes Terraform manages are as Terraform last saw them; otherwise it
// fails and asks for a new plan.
func (r *crud[M, P]) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	if notConfigured(r.client, &resp.Diagnostics) {
		return
	}
	ctx, span := r.client.span(ctx, "beenfar"+r.typeName+".update")
	defer span.End()
	var plan, state M
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	etag, diags := getEtag(ctx, req.Private)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	id := r.idOf(state)
	paths := updateMask(plan, state)

	var out P
	var err error
	if len(paths) == 0 {
		// Only attributes Terraform stopped managing changed: nothing to
		// send. Read the resource back for its current etag.
		out, err = r.get(ctx, r.client, id)
	} else {
		var msg P
		msg, err = r.fromModel(plan)
		if err != nil {
			resp.Diagnostics.AddError("Invalid "+r.what, err.Error())
			return
		}
		r.setID(msg, id)
		mask := &fieldmaskpb.FieldMask{Paths: paths}
		out, err = r.update(ctx, r.client, msg, etag, mask)
		if connect.CodeOf(err) == connect.CodeAborted {
			out, err = r.retry(ctx, state, msg, mask)
		}
	}
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		resp.Diagnostics.AddError("Cannot update "+r.what, apiError(err))
		return
	}
	newState := refresh(ctx, plan, r.toModel(out), false)
	resp.Diagnostics.Append(resp.State.Set(ctx, &newState)...)
	resp.Diagnostics.Append(setEtag(ctx, resp.Private, r.etagOf(out))...)
}

func (r *crud[M, P]) retry(ctx context.Context, state M, msg P, mask *fieldmaskpb.FieldMask) (P, error) {
	var zero P
	cur, err := r.get(ctx, r.client, r.idOf(state))
	if err != nil {
		return zero, err
	}
	if drift := managedDrift(state, r.toModel(cur)); len(drift) > 0 {
		return zero, conflictError(r.what, drift)
	}
	return r.update(ctx, r.client, msg, r.etagOf(cur), mask)
}

func (r *crud[M, P]) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	if notConfigured(r.client, &resp.Diagnostics) {
		return
	}
	ctx, span := r.client.span(ctx, "beenfar"+r.typeName+".delete")
	defer span.End()
	var state M
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	// Destroying deletes the whole resource, including attributes others
	// manage, so it is not guarded by the etag.
	err := r.del(ctx, r.client, r.idOf(state))
	if err != nil && connect.CodeOf(err) != connect.CodeNotFound {
		span.SetStatus(codes.Error, err.Error())
		resp.Diagnostics.AddError("Cannot delete "+r.what, apiError(err))
	}
}

func (r *crud[M, P]) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
	resp.Diagnostics.Append(resp.Private.SetKey(ctx, privImporting, []byte("true"))...)
}

// notConfigured reports a call made before the provider had a client, which
// happens when its url or token depend on values not known yet.
func notConfigured(c *Client, diags *diag.Diagnostics) bool {
	if c != nil {
		return false
	}
	diags.AddError("Provider not configured",
		"The beenfar provider has no url and token yet. If they come from other resources, apply those first.")
	return true
}

// clientFrom extracts the provider's client from ProviderData. It is nil
// before the provider is configured.
func clientFrom(data any, diags *diag.Diagnostics) *Client {
	if data == nil {
		return nil
	}
	c, ok := data.(*Client)
	if !ok {
		diags.AddError("Unexpected provider data", fmt.Sprintf("expected *provider.Client, got %T", data))
		return nil
	}
	return c
}

// apiError renders an API error for a diagnostic.
func apiError(err error) string {
	if ce, ok := errors.AsType[*connect.Error](err); ok {
		return fmt.Sprintf("%s (%s)", ce.Message(), ce.Code())
	}
	return err.Error()
}
