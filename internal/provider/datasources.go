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
		datasources.NewDataExtracts,
		datasources.NewDevices,
		datasources.NewEvents,
		datasources.NewNeedsAttentionReport,
		datasources.NewOrganizations,
		datasources.NewPolicies,
		datasources.NewPolicyStats,
		datasources.NewPrepatchReport,
		datasources.NewScheduledWindows,
		datasources.NewServerGroups,
		datasources.NewWorklet,
		datasources.NewWorklets,

		// Add automox provider data sources here
	}
}
