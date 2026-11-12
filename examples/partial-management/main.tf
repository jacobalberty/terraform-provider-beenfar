# Terraform manages the networks, the wifi networks and their security
# (including WPA-enterprise against a RADIUS server), the RADIUS server and
# the switch ports. The access point associations of the wifi networks
# (ap_groups) are left to the beenfar UI: no resource sets them, so Terraform
# neither changes them nor reports changes made in the UI.

terraform {
  required_providers {
    beenfar = {
      source = "jacobalberty/beenfar"
    }
  }
}

# BEENFAR_URL and BEENFAR_TOKEN supply the connection.
provider "beenfar" {}

variable "radius_secret" {
  type      = string
  sensitive = true
}

variable "guest_passphrase" {
  type      = string
  sensitive = true
}

variable "switch_mac" {
  type        = string
  description = "MAC address of the switch to adopt, lower case and colon-separated."
}

# --- Networks ---

resource "beenfar_network" "corp" {
  name    = "corp"
  vlan_id = 10
  subnet  = "10.0.10.1/24"
}

resource "beenfar_network" "guest" {
  name         = "guest"
  vlan_id      = 5
  subnet       = "10.0.5.1/24"
  dhcp_enabled = true
  dhcp_start   = "10.0.5.100"
  dhcp_stop    = "10.0.5.250"
}

# --- RADIUS ---

resource "beenfar_radius_server" "corp" {
  name            = "corp"
  address         = "10.2.1.76"
  accounting_port = 1813
  shared_secret   = var.radius_secret
}

# --- Wifi ---
# No ap_groups: which access points broadcast these is decided in the UI.

resource "beenfar_wifi_network" "corp" {
  ssid             = "corp"
  security         = "wpa_enterprise"
  radius_server_id = beenfar_radius_server.corp.id
  network_id       = beenfar_network.corp.id
}

resource "beenfar_wifi_network" "guest" {
  ssid         = "guest"
  security     = "wpa_personal"
  security_key = var.guest_passphrase
  network_id   = beenfar_network.guest.id
}

# --- Switch ports ---

resource "beenfar_device" "switch" {
  mac             = var.switch_mac
  wait_for_device = "5m"
}

# An access point uplink: the default network untagged, corp and guest
# tagged, powered over Ethernet.
resource "beenfar_switch_port" "ap_uplink" {
  device_id          = beenfar_device.switch.id
  port_index         = 1
  tagging            = "custom"
  tagged_network_ids = [beenfar_network.corp.id, beenfar_network.guest.id]
  poe_mode           = "auto"
}

# A desk port on the corp network only. Its name is not set, so it can be
# labelled in the UI.
resource "beenfar_switch_port" "desk" {
  device_id         = beenfar_device.switch.id
  port_index        = 2
  native_network_id = beenfar_network.corp.id
  tagging           = "none"
  poe_mode          = "off"
}
