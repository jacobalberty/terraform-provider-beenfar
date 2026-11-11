# Needs an admin's API token.
data "beenfar_adapters" "all" {}

output "unhealthy_adapters" {
  value = [for a in data.beenfar_adapters.all.adapters : a.name if !a.healthy]
}
