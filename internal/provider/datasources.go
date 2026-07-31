package provider

import (
	"github.com/hashicorp/terraform-plugin-framework/datasource"

	"github.com/sweetgreen/terraform-provider-automox/internal/services/datasources"
)

// dataSources lists every read-only data source the provider offers.
//
// Keep the insertion marker at the end; new data sources are appended there so
// the list stays a single obvious place to look.
func dataSources() []func() datasource.DataSource {
	return []func() datasource.DataSource{
		datasources.NewDevice,
		datasources.NewDevicePackages,
		datasources.NewDevices,
		datasources.NewEvents,
		datasources.NewOrganizations,
		datasources.NewPolicies,
		datasources.NewPolicyStats,
		datasources.NewServerGroups,
		datasources.NewWorklet,
		datasources.NewWorklets,

		// Add automox provider data sources here
	}
}
