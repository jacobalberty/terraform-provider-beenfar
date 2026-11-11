# Adopts the device once it has contacted the controller (for UniFi, after
# it was pointed at http://<controller>:8080/inform).
resource "beenfar_device" "core_switch" {
  mac             = "f0:9f:c2:79:63:90"
  wait_for_device = "5m"
}
