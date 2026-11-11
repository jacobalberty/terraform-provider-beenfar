# Owns only the access point groups of a wifi network that is managed
# elsewhere, for instance in another team's configuration.
resource "beenfar_wifi_network_ap_groups" "corp" {
  wifi_network_id = data.terraform_remote_state.network.outputs.corp_wifi_id
  ap_groups       = ["office", "warehouse"]
}
