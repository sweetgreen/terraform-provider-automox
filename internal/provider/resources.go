package provider

import (
	"github.com/hashicorp/terraform-plugin-framework/resource"

	"github.com/sweetgreen/terraform-provider-automox/internal/services/resources/policy"
	"github.com/sweetgreen/terraform-provider-automox/internal/services/resources/policy_file"
	"github.com/sweetgreen/terraform-provider-automox/internal/services/resources/scheduled_window"
	"github.com/sweetgreen/terraform-provider-automox/internal/services/resources/server_group"
)

// resources lists every managed resource the provider offers.
//
// Keep the insertion marker at the end; new resource packages are appended there
// so the list stays a single obvious place to look.
func resources() []func() resource.Resource {
	return []func() resource.Resource{
		policy.New,
		policy_file.New,
		scheduled_window.New,
		server_group.New,

		// Add automox provider resources here
	}
}
