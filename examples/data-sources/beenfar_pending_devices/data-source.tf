data "beenfar_pending_devices" "unifi" {
  adapter = "unifi"
}

output "waiting_for_adoption" {
  value = [for d in data.beenfar_pending_devices.unifi.devices : "${d.mac} (${d.model})"]
}
