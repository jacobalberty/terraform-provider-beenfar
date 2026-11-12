package provider

import (
	"context"
	"fmt"
	"slices"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	configv1 "github.com/jacobalberty/beenfar/api/gen/go/beenfar/config/v1"
)

// Two Terraform configurations, each with its own state, manage different
// parts of the same wifi network: the one under test owns the network, a
// second one (run with the terraform or tofu CLI and the built provider)
// owns its access point groups and a RADIUS server. Neither undoes or
// reports the other's changes.
func TestAccTwoConfigurationsSideBySide(t *testing.T) {
	c := startController(t)
	ctx := context.Background()
	other := newCLIConfig(t)
	var wifiID string

	configA := func(ssid string) string {
		return c.config(fmt.Sprintf(`
resource "beenfar_network" "guest" {
  name    = "guest"
  vlan_id = 5
}

resource "beenfar_wifi_network" "home" {
  ssid         = %q
  security     = "wpa_personal"
  security_key = "correct-horse-battery"
  network_id   = beenfar_network.guest.id
}`, ssid))
	}
	writeB := func(groups string) {
		other.write(c.config(fmt.Sprintf(`
resource "beenfar_wifi_network_ap_groups" "home" {
  wifi_network_id = %q
  ap_groups       = %s
}

resource "beenfar_radius_server" "corp" {
  name          = "corp"
  address       = "10.2.1.76"
  shared_secret = "s3cret"
}`, wifiID, groups)))
	}
	wantB := func(t *testing.T) {
		t.Helper()
		if empty, out := other.planIsEmpty(); !empty {
			t.Fatalf("the other configuration's plan is not empty:\n%s", out)
		}
	}
	wifi := func() *configv1.WifiNetwork {
		t.Helper()
		r, err := c.Client.Wifi.GetWifiNetwork(ctx, &configv1.GetWifiNetworkRequest{Id: wifiID})
		if err != nil {
			t.Fatal(err)
		}
		return r.GetWifiNetwork()
	}
	check := func(ssid string, groups ...string) resource.TestCheckFunc {
		return func(*terraform.State) error {
			w := wifi()
			if w.GetSsid() != ssid || !slices.Equal(w.GetApGroups(), groups) {
				return fmt.Errorf("wifi network = %v, want ssid %q and ap_groups %v", w, ssid, groups)
			}
			wantB(t)
			return nil
		}
	}

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: providers(),
		Steps: []resource.TestStep{
			{
				Config: configA("home"),
				Check: func(s *terraform.State) (err error) {
					wifiID, err = stateID(s, "beenfar_wifi_network.home")
					return err
				},
			},
			{
				// The other configuration takes the AP groups.
				PreConfig:        func() { writeB(`["upstairs"]`); other.apply() },
				Config:           configA("home"),
				ConfigPlanChecks: expectEmptyPlan(),
				Check:            check("home", "upstairs"),
			},
			{
				// This one renames the network; the groups stay and the
				// other configuration sees no drift.
				Config: configA("home-2"),
				Check:  check("home-2", "upstairs"),
			},
			{
				// The other one changes the groups; this one sees no drift.
				PreConfig:        func() { writeB(`["upstairs", "garage"]`); other.apply() },
				Config:           configA("home-2"),
				ConfigPlanChecks: expectEmptyPlan(),
				Check:            check("home-2", "garage", "upstairs"),
			},
			{
				// Destroying the other configuration clears only what it
				// managed.
				PreConfig:        func() { other.destroy() },
				Config:           configA("home-2"),
				ConfigPlanChecks: expectEmptyPlan(),
				Check: func(*terraform.State) error {
					if w := wifi(); w.GetSsid() != "home-2" || len(w.GetApGroups()) != 0 {
						return fmt.Errorf("after destroying the other configuration: %v", w)
					}
					return nil
				},
			},
		},
	})
}
