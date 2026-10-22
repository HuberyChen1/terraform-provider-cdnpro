// terraform-provider-cdnpro is a Terraform provider for the CDN Pro API.
package main

import (
	"context"
	"flag"
	"log"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"

	"github.com/mileweb/terraform-provider-cdnpro/internal/provider"
)

// version is set by goreleaser / -ldflags at build time.
var version = "dev"

func main() {
	var debug bool
	flag.BoolVar(&debug, "debug", false, "run the provider with support for debuggers like delve")
	flag.Parse()

	err := providerserver.Serve(context.Background(), provider.New(version), providerserver.ServeOpts{
		Address: "registry.terraform.io/HuberyChen1/cdnpro",
		Debug:   debug,
	})
	if err != nil {
		log.Fatal(err.Error())
	}
}
