//go:build tools

// Package tools pins the version of tfplugindocs used to generate docs/.
//
// Without this the docs workflow would resolve whatever version is current at
// the time it runs, so a change in the generator would appear as an unexplained
// docs diff on an unrelated pull request. Pinning it here means the generator
// moves only when go.mod says so.
//
// The build tag keeps it out of the provider binary; it exists solely so `go
// mod` tracks the dependency.
package tools

import (
	_ "github.com/hashicorp/terraform-plugin-docs/cmd/tfplugindocs"
)
