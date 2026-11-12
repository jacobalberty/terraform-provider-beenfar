package provider

import (
	"context"
	"slices"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"

	configv1 "github.com/jacobalberty/beenfar/api/gen/go/beenfar/config/v1"
	"github.com/jacobalberty/terraform-provider-beenfar/internal/conv"
	"github.com/jacobalberty/terraform-provider-beenfar/internal/model"
)

func wifiModel(mod func(*model.WifiNetwork)) model.WifiNetwork {
	m := model.WifiNetwork{
		ID: types.StringValue("w1"), Ssid: types.StringValue("home"), Security: types.StringValue("wpa_personal"),
		SecurityKey: types.StringValue("correct-horse"), Band: types.StringNull(), NetworkID: types.StringNull(),
		Guest: types.BoolNull(), Hidden: types.BoolNull(), ApGroups: types.SetNull(types.StringType),
		DefaultUserGroup: types.StringNull(), RadiusServerID: types.StringNull(),
	}
	if mod != nil {
		mod(&m)
	}
	return m
}

// server is what the controller returns: every field set.
func serverWifi() model.WifiNetwork {
	return wifiModel(func(m *model.WifiNetwork) {
		m.Band, m.NetworkID, m.Guest, m.Hidden = types.StringValue("both"), types.StringValue(""), types.BoolValue(false), types.BoolValue(true)
		m.ApGroups = conv.StringSet([]string{"garage"})
		m.DefaultUserGroup, m.RadiusServerID = types.StringValue(""), types.StringValue("")
	})
}

func TestRefreshKeepsUnmanagedNull(t *testing.T) {
	ctx := context.Background()
	prior := wifiModel(func(m *model.WifiNetwork) { m.Ssid = types.StringValue("old") })
	got := refresh(ctx, prior, serverWifi(), false)
	want := wifiModel(nil) // ssid from the server, ap_groups/hidden/band still null
	if !wifiEqual(got, want) {
		t.Fatalf("refresh:\n got %+v\nwant %+v", got, want)
	}

	// A managed attribute takes the controller's value (drift shows).
	prior.Hidden = types.BoolValue(false)
	if got := refresh(ctx, prior, serverWifi(), false); !got.Hidden.Equal(types.BoolValue(true)) {
		t.Fatalf("managed hidden = %v", got.Hidden)
	}
}

func TestRefreshImportRecordsNonZero(t *testing.T) {
	got := refresh(context.Background(), model.WifiNetwork{ID: types.StringValue("w1")}, serverWifi(), true)
	if !got.Hidden.Equal(types.BoolValue(true)) || !got.Band.Equal(types.StringValue("both")) || got.ApGroups.IsNull() {
		t.Fatalf("import kept too little: %+v", got)
	}
	if !got.Guest.IsNull() || !got.NetworkID.IsNull() || !got.RadiusServerID.IsNull() {
		t.Fatalf("import recorded zero values: %+v", got)
	}
}

func TestRefreshKeepsSecretsAndLocals(t *testing.T) {
	prior := model.RadiusServer{SharedSecret: types.StringValue("s3cret"), AuthPort: types.Int64Null()}
	server := model.RadiusServer{Name: types.StringValue("r"), AuthPort: types.Int64Value(1812), SharedSecretVersion: types.Int64Value(3)}
	got := refresh(context.Background(), prior, server, false)
	if got.SharedSecret.ValueString() != "s3cret" || !got.AuthPort.IsNull() || got.SharedSecretVersion.ValueInt64() != 3 {
		t.Fatalf("refresh = %+v", got)
	}
}

func TestUpdateMask(t *testing.T) {
	state := wifiModel(nil)
	plan := wifiModel(func(m *model.WifiNetwork) {
		m.Ssid = types.StringValue("home-2")         // changed: sent
		m.Hidden = types.BoolValue(false)            // newly managed: sent
		m.ID = types.StringValue("other")            // computed: never sent
		m.ApGroups = types.SetNull(types.StringType) // unmanaged: not sent
	})
	if got := updateMask(plan, state); !slices.Equal(got, []string{"ssid", "hidden"}) {
		t.Fatalf("mask = %v", got)
	}
	if got := updateMask(state, state); len(got) != 0 {
		t.Fatalf("no change, mask = %v", got)
	}
	// Dropping an attribute from the configuration does not send it.
	managed := wifiModel(func(m *model.WifiNetwork) { m.Hidden = types.BoolValue(true) })
	if got := updateMask(wifiModel(nil), managed); len(got) != 0 {
		t.Fatalf("unmanaging sent %v", got)
	}
}

func TestManagedDrift(t *testing.T) {
	state := wifiModel(nil)
	server := serverWifi() // differs only in fields state does not manage
	if d := managedDrift(state, server); len(d) != 0 {
		t.Fatalf("drift on unmanaged fields: %v", d)
	}
	server.Ssid = types.StringValue("renamed")
	if d := managedDrift(state, server); !slices.Equal(d, []string{"ssid"}) {
		t.Fatalf("drift = %v", d)
	}
}

func TestEnums(t *testing.T) {
	for _, s := range []string{"open", "wep", "wpa_personal", "wpa_enterprise"} {
		e, err := conv.FromWifiSecurity(types.StringValue(s))
		if err != nil || conv.WifiSecurity(e).ValueString() != s {
			t.Errorf("%s: %v, %v", s, e, err)
		}
	}
	if _, err := conv.FromWifiSecurity(types.StringValue("unspecified")); err == nil {
		t.Error("unspecified accepted")
	}
	if e, err := conv.FromPoeMode(types.StringNull()); err != nil || e != configv1.PoeMode_POE_MODE_UNSPECIFIED {
		t.Errorf("null poe_mode = %v, %v", e, err)
	}
}

func wifiEqual(a, b model.WifiNetwork) bool {
	return a.ID.Equal(b.ID) && a.Ssid.Equal(b.Ssid) && a.Security.Equal(b.Security) && a.SecurityKey.Equal(b.SecurityKey) &&
		a.Band.Equal(b.Band) && a.NetworkID.Equal(b.NetworkID) && a.Guest.Equal(b.Guest) && a.Hidden.Equal(b.Hidden) &&
		a.ApGroups.Equal(b.ApGroups) && a.DefaultUserGroup.Equal(b.DefaultUserGroup) && a.RadiusServerID.Equal(b.RadiusServerID)
}
