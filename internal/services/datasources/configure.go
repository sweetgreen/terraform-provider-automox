// Package datasources implements the provider's read-only data sources.
//
// Every data source here reads whole collections through client.List, so a
// result is either complete or an error -- never a silently truncated page. That
// matters more for reads than it looks: a partial policy list is
// indistinguishable from an organization that has fewer policies than it does,
// and configuration written against the short answer would target the wrong
// things.
package datasources

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/datasource"

	"github.com/sweetgreen/terraform-provider-automox/internal/client"
)

// configure resolves the shared client from provider data.
//
// req.ProviderData is nil during early plan walks, before the provider has been
// configured. That is normal and must not be reported as an error; the data
// source is configured again once the client exists.
func configure(req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) *client.Client {
	if req.ProviderData == nil {
		return nil
	}

	c, ok := req.ProviderData.(*client.Client)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected provider data",
			fmt.Sprintf("Expected *client.Client, got %T. This is a bug in the provider.",
				req.ProviderData),
		)
		return nil
	}
	return c
}

// notConfigured reports the one case a data source cannot recover from: being
// asked to read before the provider handed over a client.
func notConfigured(c *client.Client, resp *datasource.ReadResponse) bool {
	if c != nil {
		return false
	}
	resp.Diagnostics.AddError(
		"Provider not configured",
		"The Automox client is unavailable, so this data source cannot be read. "+
			"This is a bug in the provider.",
	)
	return true
}

var _ = context.Background
