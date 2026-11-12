package provider

import (
	"context"
	"fmt"
	"reflect"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
)

// Partial management.
//
// A resource manages exactly the attributes set in its configuration. The
// rest belong to someone else (usually the beenfar UI), so the provider must
// neither change them nor report them as drift:
//
//   - Updates send an update_mask naming only the managed attributes that
//     changed (updateMask). Attribute names are the API's field names.
//   - Reads keep unmanaged attributes null in state (refresh): an attribute
//     is managed when the prior state has a value for it, which it has
//     exactly when the configuration sets it.
//   - Imports have no configuration to go by, so they record the attributes
//     whose values differ from the API's zero values; the first plan then
//     drops the ones the configuration leaves out, which changes nothing on
//     the controller.
//
// Model fields are classified by their `beenfar` struct tag (see package
// model).

type fieldKind int

const (
	kindOptional fieldKind = iota // managed when set in the configuration
	kindRequired                  // always managed
	kindComputed                  // from the controller only
	kindSecret                    // sent, never returned
	kindLocal                     // provider-only
)

type field struct {
	index int
	name  string // tfsdk name, which is also the API field name
	kind  fieldKind
}

func fieldsOf(t reflect.Type) []field {
	var out []field
	for i := range t.NumField() {
		f := t.Field(i)
		name := f.Tag.Get("tfsdk")
		if name == "" || name == "-" {
			continue
		}
		kind := kindOptional
		switch f.Tag.Get("beenfar") {
		case "required":
			kind = kindRequired
		case "computed":
			kind = kindComputed
		case "secret":
			kind = kindSecret
		case "local":
			kind = kindLocal
		}
		out = append(out, field{index: i, name: name, kind: kind})
	}
	return out
}

func value(v reflect.Value, f field) attr.Value { return v.Field(f.index).Interface().(attr.Value) }

// nullOf returns a null value of v's type.
func nullOf(ctx context.Context, v attr.Value) attr.Value {
	switch v := v.(type) {
	case basetypes.StringValue:
		return types.StringNull()
	case basetypes.BoolValue:
		return types.BoolNull()
	case basetypes.Int64Value:
		return types.Int64Null()
	case basetypes.SetValue:
		return types.SetNull(v.ElementType(ctx))
	}
	panic(fmt.Sprintf("nullOf: unsupported attribute type %T", v))
}

// isZero reports whether v holds the API's zero value for its type: "",
// false, 0 or an empty set.
func isZero(v attr.Value) bool {
	if v.IsNull() || v.IsUnknown() {
		return true
	}
	switch v := v.(type) {
	case basetypes.StringValue:
		return v.ValueString() == ""
	case basetypes.BoolValue:
		return !v.ValueBool()
	case basetypes.Int64Value:
		return v.ValueInt64() == 0
	case basetypes.SetValue:
		return len(v.Elements()) == 0
	}
	return false
}

// refresh returns the state to record after reading server, the resource
// as the controller reports it, given the prior state:
//
//   - required and computed attributes come from the controller;
//   - optional attributes come from the controller when prior manages them
//     (has a value), and stay null otherwise;
//   - secrets and provider-only attributes keep their prior values;
//   - when importing, optional attributes are recorded when not zero.
func refresh[T any](ctx context.Context, prior, server T, importing bool) T {
	out := server
	pv, ov := reflect.ValueOf(prior), reflect.ValueOf(&out).Elem()
	for _, f := range fieldsOf(pv.Type()) {
		switch f.kind {
		case kindSecret, kindLocal:
			ov.Field(f.index).Set(pv.Field(f.index))
		case kindOptional:
			managed := !value(pv, f).IsNull()
			if importing {
				managed = !isZero(value(ov, f))
			}
			if !managed {
				ov.Field(f.index).Set(reflect.ValueOf(nullOf(ctx, value(ov, f))))
			}
		}
	}
	return out
}

// updateMask names the attributes of plan to send: those the configuration
// sets (not null) whose planned values differ from state. Computed and
// provider-only attributes are never sent.
func updateMask[T any](plan, state T) []string {
	pv, sv := reflect.ValueOf(plan), reflect.ValueOf(state)
	var paths []string
	for _, f := range fieldsOf(pv.Type()) {
		if f.kind == kindComputed || f.kind == kindLocal {
			continue
		}
		p := value(pv, f)
		if p.IsNull() || p.IsUnknown() || p.Equal(value(sv, f)) {
			continue
		}
		paths = append(paths, f.name)
	}
	return paths
}

// managedDrift lists the attributes that state manages and whose values the
// controller now reports differently (server is what the controller
// returned, before refresh). It decides whether an update that lost an
// etag race may be retried: a change to attributes Terraform does not manage
// is no reason to fail.
func managedDrift[T any](state, server T) []string {
	sv, cv := reflect.ValueOf(state), reflect.ValueOf(server)
	var drift []string
	for _, f := range fieldsOf(sv.Type()) {
		if f.kind != kindOptional && f.kind != kindRequired {
			continue
		}
		s := value(sv, f)
		if s.IsNull() || s.IsUnknown() || s.Equal(value(cv, f)) {
			continue
		}
		drift = append(drift, f.name)
	}
	return drift
}

// conflictError explains a lost etag race on attributes Terraform manages.
func conflictError(resource string, drift []string) error {
	return fmt.Errorf("the %s was changed outside Terraform since it was last read (%s); "+
		"run terraform plan again to see the changes", resource, strings.Join(drift, ", "))
}
