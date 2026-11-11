// Command terraform-provider-beenfar is the Terraform provider for the
// beenfar network controller.
package main

import (
	"context"
	"flag"
	"log"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"

	"github.com/jacobalberty/terraform-provider-beenfar/internal/provider"
)

// Run "go generate" to regenerate the docs from the schema, the examples and
// templates/. tfplugindocs needs a terraform binary and downloads one if
// none is on the PATH.
//go:generate go run github.com/hashicorp/terraform-plugin-docs/cmd/tfplugindocs@v0.25.0 generate --provider-name beenfar

// version is set at build time (-ldflags "-X main.version=...").
var version = "dev"

func main() {
	debug := flag.Bool("debug", false, "run the provider with support for debuggers like delve")
	flag.Parse()

	err := providerserver.Serve(context.Background(), provider.New(version), providerserver.ServeOpts{
		Address: "registry.terraform.io/jacobalberty/beenfar",
		Debug:   *debug,
	})
	if err != nil {
		log.Fatal(err)
	}
}
