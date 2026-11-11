terraform {
  required_providers {
    beenfar = {
      source = "jacobalberty/beenfar"
    }
  }
}

# The URL and token can also come from BEENFAR_URL and BEENFAR_TOKEN.
# Create a token with: beenfard admin token -username admin -name terraform
provider "beenfar" {
  url   = "https://beenfar.example:8080"
  token = var.beenfar_token
}

variable "beenfar_token" {
  type      = string
  sensitive = true
}
