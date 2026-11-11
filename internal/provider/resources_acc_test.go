package provider

import (
	"context"
	"fmt"
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"

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
