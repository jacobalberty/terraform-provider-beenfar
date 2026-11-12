# A WPA-personal network on the iot VLAN. ap_groups is not set, so the access
# points that broadcast it are chosen in the beenfar UI and Terraform leaves
# them alone.
resource "beenfar_wifi_network" "iot" {
  ssid         = "iot"
  security     = "wpa_personal"
  security_key = var.iot_passphrase
  band         = "2g"
  network_id   = beenfar_network.iot.id
}

# WPA-enterprise against a RADIUS server.
resource "beenfar_wifi_network" "corp" {
  ssid             = "corp"
  security         = "wpa_enterprise"
  radius_server_id = beenfar_radius_server.corp.id
}
