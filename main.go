package main

import (
	"context"
	"flag"
	"log"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"

	"github.com/sweetgreen/terraform-provider-automox/internal/provider"
)

// version is stamped by goreleaser at build time via -ldflags.
var version = "dev"

//go:generate go run github.com/hashicorp/terraform-plugin-docs/cmd/tfplugindocs generate --provider-name automox

func main() {
	var debug bool
	flag.BoolVar(&debug, "debug", false, "run the provider with support for debuggers such as delve")
	flag.Parse()

	// The address must match the source practitioners write in required_providers.
	// Sweetgreen's registry derives the GitHub repository from it, so this string
	// and the repository name are one contract.
	err := providerserver.Serve(context.Background(), provider.New(version), providerserver.ServeOpts{
		Address: "registry.sweetgreen.engineering/sweetgreen/automox",
		Debug:   debug,
	})
	if err != nil {
		log.Fatal(err)
	}
}
