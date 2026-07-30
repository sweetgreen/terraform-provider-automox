package provider

import (
	"github.com/hashicorp/terraform-plugin-framework/resource"
)

// resources lists every managed resource the provider offers.
//
// Keep the insertion marker at the end; new resource packages are appended there
// so the list stays a single obvious place to look.
func resources() []func() resource.Resource {
	return []func() resource.Resource{
		// Add automox provider resources here
	}
}
