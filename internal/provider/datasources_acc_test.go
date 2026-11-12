package provider

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
)

func TestAccDataSources(t *testing.T) {
	c := startController(t)
	dev := newFakeDevice(2)
	dev.announce(t, c)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: providers(),
		Steps: []resource.TestStep{
			// Lookups that fail come first: the test destroys with the last step's
			// configuration.
			{
				Config: c.config(`
data "beenfar_network" "missing" {
  name = "nope"
}`),
				ExpectError: regexp.MustCompile(`Network not found`),
			},
			{
				Config: c.config(`
data "beenfar_device" "both" {
  id  = "x"
  mac = "aa:bb:cc:dd:ee:ff"
}`),
				ExpectError: regexp.MustCompile(`Invalid Attribute Combination`),
			},
			{
				Config: c.config(fmt.Sprintf(`
resource "beenfar_network" "lan" {
  name    = "lan"
  vlan_id = 7
}

resource "beenfar_radius_server" "corp" {
  name          = "corp"
  address       = "2001:db8::2"
  shared_secret = "s3cret"
}

data "beenfar_network" "by_name" {
  name = beenfar_network.lan.name
}

data "beenfar_radius_server" "by_id" {
  id = beenfar_radius_server.corp.id
}

data "beenfar_pending_devices" "all" {}

data "beenfar_device" "by_mac" {
  mac = %q
}

data "beenfar_adapters" "all" {}
`, dev.MAC())),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue("data.beenfar_network.by_name", tfjsonpath.New("vlan_id"), knownvalue.Int64Exact(7)),
					statecheck.ExpectKnownValue("data.beenfar_radius_server.by_id", tfjsonpath.New("auth_port"), knownvalue.Int64Exact(1812)),
					statecheck.ExpectKnownValue("data.beenfar_radius_server.by_id", tfjsonpath.New("shared_secret_version"), knownvalue.Int64Exact(1)),
					statecheck.ExpectKnownValue("data.beenfar_pending_devices.all", tfjsonpath.New("devices"), knownvalue.ListExact([]knownvalue.Check{
						knownvalue.ObjectPartial(map[string]knownvalue.Check{
							"mac": knownvalue.StringExact(dev.MAC()), "state": knownvalue.StringExact("pending"),
							"model": knownvalue.StringExact("U7PG2"), "adapter": knownvalue.StringExact("unifi"),
						}),
					})),
					statecheck.ExpectKnownValue("data.beenfar_device.by_mac", tfjsonpath.New("state"), knownvalue.StringExact("pending")),
					statecheck.ExpectKnownValue("data.beenfar_adapters.all", tfjsonpath.New("adapters"), knownvalue.ListExact([]knownvalue.Check{
						knownvalue.ObjectPartial(map[string]knownvalue.Check{
							"name": knownvalue.StringExact("unifi"), "kind": knownvalue.StringExact("builtin"), "healthy": knownvalue.Bool(true),
						}),
					})),
				},
				Check: resource.TestCheckResourceAttrPair("data.beenfar_network.by_name", "id", "beenfar_network.lan", "id"),
			},
		},
	})
}
