package policy

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/sweetgreen/terraform-provider-automox/internal/schedule"
)

func parseImportID(raw string) (int64, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return 0, fmt.Errorf("the ID is empty")
	}
	id, err := strconv.ParseInt(trimmed, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("not a number")
	}
	if id < 1 {
		return 0, fmt.Errorf("policy IDs are positive")
	}
	return id, nil
}

// ValidateConfig enforces the rules Automox applies, at plan time.
//
// Every rule here was established by being rejected by the live API. Catching
// them during plan turns an opaque 400 during apply into a message that names the
// attribute and what to do about it — and for a resource that decides when code
// runs on endpoints, failing before the write is worth the extra code.
func (r *policyResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var config policyModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	validateScheduleForms(config, resp)

	// A timezone-anchored schedule needs the offset. The API enforces this but
	// its message does not name the missing field.
	if config.UseScheduledTimezone.ValueBool() && config.ScheduledTimezone.IsNull() {
		resp.Diagnostics.AddAttributeError(
			path.Root("scheduled_timezone"),
			"Missing scheduled_timezone",
			"use_scheduled_timezone is true, so scheduled_timezone must be set, in Automox's "+
				"UTC offset form such as \"UTC-0500\".",
		)
	}

	if config.Configuration.IsNull() || config.Configuration.IsUnknown() {
		return
	}
	cfg := config.Configuration.Attributes()

	policyType := config.Type.ValueString()
	switch policyType {
	case TypePatch:
		validatePatchConfiguration(cfg, resp)
	case TypeWorklet:
		// Automox reports these in a spaced form ("The configuration.evaluation
		// code field is required"), which does not match anything the practitioner
		// wrote and is awkward to map back to the attribute.
		requireSet(cfg, "os_family", TypeWorklet, resp)
		requireSet(cfg, "evaluation_code", TypeWorklet, resp)
		requireSet(cfg, "remediation_code", TypeWorklet, resp)
	case TypeRequiredSoftware:
		// os_family is required here as well. The vendor documentation lists it for
		// neither type; the live API rejects both without it.
		requireSet(cfg, "os_family", TypeRequiredSoftware, resp)
		requireSet(cfg, "package_name", TypeRequiredSoftware, resp)
		requireSet(cfg, "package_version", TypeRequiredSoftware, resp)
		requireSet(cfg, "installation_code", TypeRequiredSoftware, resp)
	}
}

// validateScheduleForms rejects specifying a schedule twice.
//
// The encoded and named forms describe the same field, and Automox stores only
// the integer. Accepting both would mean silently honouring one — and a schedule
// that is not what the practitioner wrote is how patching happens at the wrong
// time.
func validateScheduleForms(config policyModel, resp *resource.ValidateConfigResponse) {
	pairs := []struct {
		encodedPath, namedPath string
		encoded                types.Int64
		named                  types.Set
	}{
		{"schedule_days", "schedule_days_of_week", config.ScheduleDays, config.ScheduleDaysOfWeek},
		{"schedule_months", "schedule_months_of_year", config.ScheduleMonths, config.ScheduleMonthsOfYear},
	}

	for _, p := range pairs {
		encodedSet := !p.encoded.IsNull() && !p.encoded.IsUnknown()
		namedSet := !p.named.IsNull() && !p.named.IsUnknown()
		if encodedSet && namedSet {
			resp.Diagnostics.AddAttributeError(
				path.Root(p.namedPath),
				"Conflicting schedule configuration",
				fmt.Sprintf("%s and %s set the same schedule; specify one.\n\n"+
					"%s is the readable form and is usually what you want; %s is Automox's "+
					"encoded integer.", p.encodedPath, p.namedPath, p.namedPath, p.encodedPath),
			)
		}
	}
}

func validatePatchConfiguration(cfg map[string]attr.Value, resp *resource.ValidateConfigResponse) {
	patchRule := stringOf(cfg, "patch_rule")

	// Established live: a patch policy without filter_type is rejected whatever
	// its patch_rule, even though the API's own message says the field is needed
	// only for patch_rule "filter".
	if isUnset(cfg, "filter_type") {
		resp.Diagnostics.AddAttributeError(
			path.Root("configuration").AtName("filter_type"),
			"Missing filter_type",
			"Automox requires configuration.filter_type on every patch policy, not only when "+
				"patch_rule is \"filter\". Use \"include\" with an empty filters list if the "+
				"policy is not meant to filter.",
		)
	}

	switch patchRule {
	case PatchRuleAdvanced:
		if isUnset(cfg, "advanced_filter") {
			resp.Diagnostics.AddAttributeError(
				path.Root("configuration").AtName("advanced_filter"),
				"Missing advanced_filter",
				"patch_rule is \"advanced\", so configuration.advanced_filter must contain at "+
					"least one expression.",
			)
		}
	case PatchRuleFilter:
		if isUnset(cfg, "filters") {
			resp.Diagnostics.AddAttributeWarning(
				path.Root("configuration").AtName("filters"),
				"Empty filters with patch_rule \"filter\"",
				"patch_rule is \"filter\" but no filters are set, so the policy will not select "+
					"any patches.",
			)
		}
	}
}

func requireSet(cfg map[string]attr.Value, name, policyType string, resp *resource.ValidateConfigResponse) {
	if isUnset(cfg, name) {
		resp.Diagnostics.AddAttributeError(
			path.Root("configuration").AtName(name),
			"Missing configuration."+name,
			fmt.Sprintf("A %q policy requires configuration.%s.", policyType, name),
		)
	}
}

// setNoneOf rejects members outside the allowed list, naming the valid ones.
func setNoneOf(allowed []string) validator.Set {
	return namedMemberValidator{allowed: allowed}
}

type namedMemberValidator struct {
	allowed []string
}

func (v namedMemberValidator) Description(context.Context) string {
	return "each element must be one of: " + strings.Join(v.allowed, ", ")
}

func (v namedMemberValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (v namedMemberValidator) ValidateSet(ctx context.Context, req validator.SetRequest, resp *validator.SetResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}

	var values []string
	resp.Diagnostics.Append(req.ConfigValue.ElementsAs(ctx, &values, false)...)
	if resp.Diagnostics.HasError() {
		return
	}

	for _, got := range values {
		if !contains(v.allowed, strings.ToLower(strings.TrimSpace(got))) {
			resp.Diagnostics.AddAttributeError(
				req.Path,
				"Invalid schedule value",
				fmt.Sprintf("%q is not recognised. Valid values are: %s.",
					got, strings.Join(v.allowed, ", ")),
			)
		}
	}
}

func contains(haystack []string, needle string) bool {
	for _, h := range haystack {
		if h == needle {
			return true
		}
	}
	return false
}

// stringOf reads a string attribute out of the configuration object, returning
// "" when it is absent or not yet known.
func stringOf(cfg map[string]attr.Value, name string) string {
	v, ok := cfg[name]
	if !ok || v.IsNull() || v.IsUnknown() {
		return ""
	}
	s, ok := v.(types.String)
	if !ok {
		return ""
	}
	return s.ValueString()
}

// isUnset reports whether an attribute was left out of the configuration.
//
// Unknown counts as set: the value comes from another resource and will exist by
// apply time, so rejecting it at plan would break legitimate composition.
func isUnset(cfg map[string]attr.Value, name string) bool {
	v, ok := cfg[name]
	if !ok {
		return true
	}
	return v.IsNull()
}

var _ = schedule.AllDays
