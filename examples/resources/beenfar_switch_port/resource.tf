# Port 8 is a trunk to an access point: the default network untagged, and
# only the iot and guest VLANs tagged. PoE powers the access point.
resource "beenfar_switch_port" "ap_uplink" {
  device_id          = beenfar_device.core_switch.id
  port_index         = 8
  name               = "AP uplink"
  tagging            = "custom"
  tagged_network_ids = [beenfar_network.iot.id, beenfar_network.guest.id]
  poe_mode           = "auto"
}
