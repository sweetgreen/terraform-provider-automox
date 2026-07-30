package provider

import (
	"github.com/hashicorp/terraform-plugin-framework/datasource"
)

// dataSources lists every read-only data source the provider offers.
func dataSources() []func() datasource.DataSource {
	return []func() datasource.DataSource{
		// Add automox provider data sources here
	}
}
