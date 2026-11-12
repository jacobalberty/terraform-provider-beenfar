package provider

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	configv1 "github.com/jacobalberty/beenfar/api/gen/go/beenfar/config/v1"
)

// stateID returns the id of the resource at addr in s.
func stateID(s *terraform.State, addr string) (string, error) {
	rs, ok := s.RootModule().Resources[addr]
	if !ok {
		return "", fmt.Errorf("%s not in state", addr)
	}
	return rs.Primary.ID, nil
}

func expectEmptyPlan() resource.ConfigPlanChecks {
	return resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}
}

func TestAccNetwork(t *testing.T) {
	c := startController(t)
	var id string
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: providers(),
		CheckDestroy: func(*terraform.State) error {
			_, err := c.Client.Networks.GetNetwork(context.Background(), &configv1.GetNetworkRequest{Id: id})
			if connect.CodeOf(err) != connect.CodeNotFound {
				return fmt.Errorf("network %s still exists (%v)", id, err)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: c.config(`
resource "beenfar_network" "iot" {
  name    = "iot"
  vlan_id = 20
  subnet  = "10.0.20.1/24"
}`),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue("beenfar_network.iot", tfjsonpath.New("vlan_id"), knownvalue.Int64Exact(20)),
					statecheck.ExpectKnownValue("beenfar_network.iot", tfjsonpath.New("domain_name"), knownvalue.Null()),
				},
				Check: func(s *terraform.State) (err error) { id, err = stateID(s, "beenfar_network.iot"); return err },
			},
			{
				ResourceName:      "beenfar_network.iot",
				ImportState:       true,
				ImportStateVerify: true,
			},
			{
				Config: c.config(`
resource "beenfar_network" "iot" {
  name         = "iot"
  vlan_id      = 30
  subnet       = "10.0.30.1/24"
  dhcp_enabled = true
  dhcp_start   = "10.0.30.100"
  dhcp_stop    = "10.0.30.199"
  domain_name  = "iot.example"
}`),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue("beenfar_network.iot", tfjsonpath.New("vlan_id"), knownvalue.Int64Exact(30)),
					statecheck.ExpectKnownValue("beenfar_network.iot", tfjsonpath.New("domain_name"), knownvalue.StringExact("iot.example")),
				},
				Check: func(s *terraform.State) error {
					got, err := stateID(s, "beenfar_network.iot")
					if err == nil && got != id {
						err = fmt.Errorf("id changed from %s to %s: update replaced the network", id, got)
					}
					return err
				},
			},
		},
	})
}

// The access point groups set in the UI survive everything Terraform does
// to the wifi network short of destroying it, other fields the UI owns
// never show as drift, and a managed field changed in the UI does.
func TestAccWifiNetworkPartial(t *testing.T) {
	c := startController(t)
	ctx := context.Background()
	var id string
	get := func() (*configv1.WifiNetwork, error) {
		r, err := c.Client.Wifi.GetWifiNetwork(ctx, &configv1.GetWifiNetworkRequest{Id: id})
		return r.GetWifiNetwork(), err
	}
	// ui changes fields the way the beenfar UI does: the same masked RPC.
	ui := func(w *configv1.WifiNetwork, paths ...string) {
		t.Helper()
		w.Id = id
		if _, err := c.Client.Wifi.UpdateWifiNetwork(ctx, &configv1.UpdateWifiNetworkRequest{
			WifiNetwork: w, UpdateMask: &fieldmaskpb.FieldMask{Paths: paths},
		}); err != nil {
			t.Fatal(err)
		}
	}
	wantGroups := func(want ...string) func(*terraform.State) error {
		return func(*terraform.State) error {
			w, err := get()
			if err != nil {
				return err
			}
			if !slices.Equal(w.GetApGroups(), want) {
				return fmt.Errorf("ap_groups = %v, want %v", w.GetApGroups(), want)
			}
			return nil
		}
	}
	config := func(ssid string) string {
		return c.config(fmt.Sprintf(`
resource "beenfar_wifi_network" "home" {
  ssid         = %q
  security     = "wpa_personal"
  security_key = "correct-horse-battery"
}`, ssid))
	}

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: providers(),
		Steps: []resource.TestStep{
			{
				Config: config("home"),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue("beenfar_wifi_network.home", tfjsonpath.New("ap_groups"), knownvalue.Null()),
					statecheck.ExpectKnownValue("beenfar_wifi_network.home", tfjsonpath.New("hidden"), knownvalue.Null()),
				},
				Check: func(s *terraform.State) (err error) { id, err = stateID(s, "beenfar_wifi_network.home"); return err },
			},
			{
				// The UI associates access points and hides the SSID;
				// Terraform manages neither, so nothing changes.
				PreConfig: func() {
					ui(&configv1.WifiNetwork{ApGroups: []string{"upstairs", "garage"}, Hidden: true}, "ap_groups", "hidden")
				},
				Config:           config("home"),
				ConfigPlanChecks: expectEmptyPlan(),
				Check:            wantGroups("garage", "upstairs"),
			},
			{
				// Renaming the SSID leaves the associations alone.
				Config: config("home-2"),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("beenfar_wifi_network.home", plancheck.ResourceActionUpdate),
				}},
				Check: resource.ComposeTestCheckFunc(wantGroups("garage", "upstairs"), func(*terraform.State) error {
					w, err := get()
					if err == nil && (w.GetSsid() != "home-2" || !w.GetHidden()) {
						err = fmt.Errorf("after rename: %v", w)
					}
					return err
				}),
			},
			{
				// A managed field changed in the UI is drift, and applying
				// restores it, while the etag the UI's change made stale is
				// no obstacle and the UI's other fields stay.
				PreConfig: func() { ui(&configv1.WifiNetwork{SecurityKey: "changed-in-the-ui"}, "security_key") },
				Config:    config("home-2"),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("beenfar_wifi_network.home", plancheck.ResourceActionUpdate),
				}},
				Check: resource.ComposeTestCheckFunc(wantGroups("garage", "upstairs"), func(*terraform.State) error {
					w, err := get()
					if err == nil && w.GetSecurityKey() != "correct-horse-battery" {
						err = fmt.Errorf("security_key = %q, want it restored", w.GetSecurityKey())
					}
					return err
				}),
			},
			{
				// Import records what is set on the controller; the fields
				// this configuration does not manage differ from it.
				ResourceName:            "beenfar_wifi_network.home",
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"ap_groups", "hidden", "band"},
			},
		},
	})
}

// The etag guards against changes to managed fields made between plan and
// apply: an update that loses that race fails instead of overwriting them.
func TestAccWifiNetworkConflict(t *testing.T) {
	c := startController(t)
	ctx := context.Background()
	var id string
	config := func(key string) string {
		return c.config(fmt.Sprintf(`
resource "beenfar_wifi_network" "home" {
  ssid         = "home"
  security     = "wpa_personal"
  security_key = %q
}`, key))
	}
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: providers(),
		Steps: []resource.TestStep{
			{
				Config: config("first-passphrase"),
				Check:  func(s *terraform.State) (err error) { id, err = stateID(s, "beenfar_wifi_network.home"); return err },
			},
			{
				// Between the refresh in the plan and the apply, the UI
				// changes a managed field. The plan/apply runs here as one
				// step, so the race is emulated with a pre-apply check that
				// makes the change once the plan has been computed.
				Config: config("second-passphrase"),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{raceUI(func() error {
					_, err := c.Client.Wifi.UpdateWifiNetwork(ctx, &configv1.UpdateWifiNetworkRequest{
						WifiNetwork: &configv1.WifiNetwork{Id: id, Ssid: "renamed-in-the-ui"},
						UpdateMask:  &fieldmaskpb.FieldMask{Paths: []string{"ssid"}},
					})
					return err
				})}},
				ExpectError: regexp.MustCompile(`changed outside Terraform since it was last read \(ssid\)`),
			},
		},
	})
}

// waitSilent outlasts BEENFAR_UNIFI_READOPT_AFTER (1s in the harness), after
// which a device that informs with the default key counts as reset.
func waitSilent() { time.Sleep(1500 * time.Millisecond) }

// raceUI is a plan check that runs fn after the plan, before the apply.
type raceUI func() error

func (f raceUI) CheckPlan(_ context.Context, _ plancheck.CheckPlanRequest, resp *plancheck.CheckPlanResponse) {
	if err := f(); err != nil {
		resp.Error = err
	}
}

func TestAccRadiusServerAndEnterpriseWifi(t *testing.T) {
	c := startController(t)
	ctx := context.Background()
	var radiusID string
	config := func(secret string, acct int) string {
		return c.config(fmt.Sprintf(`
resource "beenfar_radius_server" "corp" {
  name            = "corp"
  address         = "10.2.1.76"
  accounting_port = %d
  shared_secret   = %q
}

resource "beenfar_wifi_network" "corp" {
  ssid             = "corp"
  security         = "wpa_enterprise"
  radius_server_id = beenfar_radius_server.corp.id
}`, acct, secret))
	}
	version := func(v int64) statecheck.StateCheck {
		return statecheck.ExpectKnownValue("beenfar_radius_server.corp", tfjsonpath.New("shared_secret_version"), knownvalue.Int64Exact(v))
	}
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: providers(),
		Steps: []resource.TestStep{
			{
				Config:            config("s3cret", 1813),
				ConfigStateChecks: []statecheck.StateCheck{version(1)},
				Check: resource.ComposeTestCheckFunc(
					func(s *terraform.State) (err error) {
						radiusID, err = stateID(s, "beenfar_radius_server.corp")
						return err
					},
					resource.TestCheckResourceAttrPair("beenfar_wifi_network.corp", "radius_server_id", "beenfar_radius_server.corp", "id"),
					resource.TestCheckNoResourceAttr("beenfar_radius_server.corp", "auth_port"),
				),
			},
			{
				// Rotating the secret moves the version.
				Config:            config("n3w-secret", 0),
				ConfigStateChecks: []statecheck.StateCheck{version(2)},
			},
			{
				// The secret replaced outside Terraform is noticed through
				// the version and set again.
				PreConfig: func() {
					if _, err := c.Client.Radius.UpdateRadiusServer(ctx, &configv1.UpdateRadiusServerRequest{
						RadiusServer: &configv1.RadiusServer{Id: radiusID, SharedSecret: "ui-secret"},
						UpdateMask:   &fieldmaskpb.FieldMask{Paths: []string{"shared_secret"}},
					}); err != nil {
						t.Fatal(err)
					}
				},
				Config: config("n3w-secret", 0),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("beenfar_radius_server.corp", plancheck.ResourceActionUpdate),
				}},
				ConfigStateChecks: []statecheck.StateCheck{version(4)},
			},
			{
				ResourceName:      "beenfar_radius_server.corp",
				ImportState:       true,
				ImportStateVerify: true,
				// Import records the port the controller defaulted, and leaves
				// out the secret it never returns and the accounting port the
				// configuration sets to zero.
				ImportStateVerifyIgnore: []string{"shared_secret", "auth_port", "accounting_port"},
			},
		},
	})
}

// A device is adopted by creating the resource, imported by MAC, re-adopted
// after a factory reset and forgotten on destroy; ports of the adopted
// device are configured with beenfar_switch_port.
func TestAccDeviceAndSwitchPorts(t *testing.T) {
	c := startController(t)
	ctx := context.Background()
	dev := newFakeDevice(1)
	dev.announce(t, c)

	var portID string
	config := func(poe string) string {
		return c.config(fmt.Sprintf(`
data "beenfar_pending_devices" "unifi" {
  adapter = "unifi"
}

resource "beenfar_device" "switch" {
  mac = %q
}

resource "beenfar_network" "iot" {
  name    = "iot"
  vlan_id = 20
}

resource "beenfar_switch_port" "p2" {
  device_id          = beenfar_device.switch.id
  port_index         = 2
  tagging            = "custom"
  tagged_network_ids = [beenfar_network.iot.id]
  poe_mode           = %q
}`, dev.MAC(), poe))
	}
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: providers(),
		CheckDestroy: func(*terraform.State) error {
			if d := c.device(t, dev); d != nil {
				return fmt.Errorf("device still known after destroy: %v", d)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: config("off"),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("beenfar_device.switch", "adapter", "unifi"),
					resource.TestCheckResourceAttr("beenfar_device.switch", "model", "U7PG2"),
					func(s *terraform.State) (err error) { portID, err = stateID(s, "beenfar_switch_port.p2"); return err },
					func(*terraform.State) error {
						if d := c.device(t, dev); d.GetState() != configv1.DeviceState_DEVICE_STATE_ADOPTED {
							return fmt.Errorf("device = %v, want adopted", d)
						}
						return nil
					},
				),
			},
			{
				// The device takes its key; the UI labels the port.
				PreConfig: func() {
					dev.settle(t, c)
					if _, err := c.Client.SwitchPorts.UpdateSwitchPort(ctx, &configv1.UpdateSwitchPortRequest{
						SwitchPort: &configv1.SwitchPort{Id: portID, Name: "printer"},
						UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"name"}},
					}); err != nil {
						t.Fatal(err)
					}
				},
				Config:           config("off"),
				ConfigPlanChecks: expectEmptyPlan(),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue("data.beenfar_pending_devices.unifi", tfjsonpath.New("devices"), knownvalue.ListSizeExact(0)),
				},
			},
			{
				Config: config("auto"),
				Check: func(*terraform.State) error {
					r, err := c.Client.SwitchPorts.GetSwitchPort(ctx, &configv1.GetSwitchPortRequest{Id: portID})
					if err == nil && (r.GetSwitchPort().GetPoeMode() != configv1.PoeMode_POE_MODE_AUTO || r.GetSwitchPort().GetName() != "printer") {
						err = fmt.Errorf("port = %v", r.GetSwitchPort())
					}
					return err
				},
			},
			{
				ResourceName:            "beenfar_switch_port.p2",
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"name"},
			},
			{
				ResourceName:            "beenfar_device.switch",
				ImportState:             true,
				ImportStateId:           dev.MAC(),
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"ip"},
			},
			{
				// The device is factory reset: after a silent second it
				// informs with the default key and awaits re-adoption. The
				// plan creates the device resource again; applying accepts
				// the re-adoption, keeping the device and its port settings.
				PreConfig: func() {
					dev.reset()
					waitSilent()
					if status, _ := dev.inform(t, c); status != 404 {
						t.Fatalf("reset device inform: HTTP %d", status)
					}
					if d := c.device(t, dev); d.GetReadoption() != configv1.ReadoptionState_READOPTION_STATE_AWAITING_ACCEPTANCE {
						t.Fatalf("device after reset = %v, want awaiting re-adoption", d)
					}
				},
				Config: config("auto"),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("beenfar_device.switch", plancheck.ResourceActionCreate),
					plancheck.ExpectResourceAction("beenfar_switch_port.p2", plancheck.ResourceActionNoop),
				}},
				Check: func(s *terraform.State) error {
					got, err := stateID(s, "beenfar_switch_port.p2")
					if err == nil && got != portID {
						err = fmt.Errorf("port settings were recreated (%s, was %s)", got, portID)
					}
					if d := c.device(t, dev); d.GetReadoption() != configv1.ReadoptionState_READOPTION_STATE_IN_PROGRESS {
						err = fmt.Errorf("device after apply = %v, want re-adoption in progress", d)
					}
					return err
				},
			},
		},
	})
}
