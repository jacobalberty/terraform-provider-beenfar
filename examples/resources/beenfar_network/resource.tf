resource "beenfar_network" "iot" {
  name         = "iot"
  vlan_id      = 20
  subnet       = "10.0.20.1/24"
  dhcp_enabled = true
  dhcp_start   = "10.0.20.100"
  dhcp_stop    = "10.0.20.199"
}
