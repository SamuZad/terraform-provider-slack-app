package main

import (
	"context"
	"flag"
	"log"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/samuzad/terraform-provider-slack-app/internal/provider"
)

// version is set by GoReleaser via ldflags at release time.
var version = "dev"

func main() {
	var debug bool
	flag.BoolVar(&debug, "debug", false, "set to true to run the provider with support for debuggers like delve")
	flag.Parse()

	err := providerserver.Serve(context.Background(), provider.New(version), providerserver.ServeOpts{
		Address: "registry.terraform.io/samuzad/slack-app",
		Debug:   debug,
	})
	if err != nil {
		log.Fatal(err.Error())
	}
}
