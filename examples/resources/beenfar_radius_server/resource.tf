resource "beenfar_radius_server" "corp" {
  name            = "corp"
  address         = "10.2.1.76"
  accounting_port = 1813
  shared_secret   = var.radius_secret
}
