package policy

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// serverCountUseStateForUnknown behaves like int64planmodifier.UseStateForUnknown,
// except it leaves the planned value unknown whenever server_groups is changing.
//
// server_count is API-populated: Automox recomputes it from live group
// membership. Carrying the refreshed value forward is correct for a
// schedule-only update, where group membership does not change, but it is
// wrong the moment server_groups itself is in the diff -- the API then
// returns a different count, and "planned known value must equal applied
// value" fails after the change has already been made.
func serverCountUseStateForUnknown() planmodifier.Int64 {
	return serverCountUseStateForUnknownModifier{}
}

type serverCountUseStateForUnknownModifier struct{}

func (m serverCountUseStateForUnknownModifier) Description(_ context.Context) string {
	return "Preserves the refreshed server_count across updates that do not change " +
		"server_groups; leaves it unknown when server_groups changes."
}

func (m serverCountUseStateForUnknownModifier) MarkdownDescription(ctx context.Context) string {
	return m.Description(ctx)
}

func (m serverCountUseStateForUnknownModifier) PlanModifyInt64(
	ctx context.Context, req planmodifier.Int64Request, resp *planmodifier.Int64Response,
) {
	if req.State.Raw.IsNull() {
		return // resource is being created
	}
	if !req.PlanValue.IsUnknown() {
		return
	}
	if req.ConfigValue.IsUnknown() {
		return
	}

	var planGroups, stateGroups types.List
	resp.Diagnostics.Append(req.Plan.GetAttribute(ctx, path.Root("server_groups"), &planGroups)...)
	resp.Diagnostics.Append(req.State.GetAttribute(ctx, path.Root("server_groups"), &stateGroups)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if planGroups.IsUnknown() || !planGroups.Equal(stateGroups) {
		return // server_groups is changing; the API will report a new count
	}

	resp.PlanValue = req.StateValue
}

// statusUseStateForUnknown behaves like stringplanmodifier.UseStateForUnknown,
// except it leaves the planned status unknown whenever the update changes
// something Automox derives status from.
//
// status is read-only: Automox sets it itself. Reading every policy in org
// 120547 (2026-11-17) found status == "active" exactly when
// configuration.auto_patch is true, on all 32 patch policies, regardless of
// server groups or a zero schedule; worklets are always "active". Promising the
// prior value across an auto_patch change therefore fails the apply after the
// write has landed -- sweetgreen/terraform-infrastructure#2149, policy 726918:
// ".status: was cty.StringVal("inactive"), but now cty.StringVal("active")".
//
// A server_groups change between empty and non-empty is treated as a driver
// too. No production policy could confirm or rule it out, and leaving status
// unknown costs one "known after apply" line on a change that is rare and
// significant anyway. A move between two non-empty group lists keeps status:
// 554218 shows membership does not decide it, and ring moves are routine.
func statusUseStateForUnknown() planmodifier.String {
	return statusUseStateForUnknownModifier{}
}

type statusUseStateForUnknownModifier struct{}

func (m statusUseStateForUnknownModifier) Description(_ context.Context) string {
	return "Preserves the refreshed status across updates that do not change " +
		"configuration.auto_patch and do not add groups to, or remove all groups from, " +
		"the policy; leaves it unknown otherwise."
}

func (m statusUseStateForUnknownModifier) MarkdownDescription(ctx context.Context) string {
	return m.Description(ctx)
}

func (m statusUseStateForUnknownModifier) PlanModifyString(
	ctx context.Context, req planmodifier.StringRequest, resp *planmodifier.StringResponse,
) {
	if req.State.Raw.IsNull() {
		return // resource is being created
	}
	if !req.PlanValue.IsUnknown() {
		return
	}
	if req.ConfigValue.IsUnknown() {
		return
	}

	changing, diags := statusDriversChanging(ctx, req)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() || changing {
		return
	}

	resp.PlanValue = req.StateValue
}

// statusDriversChanging reports whether the update may make Automox report a
// different status.
func statusDriversChanging(ctx context.Context, req planmodifier.StringRequest) (bool, diag.Diagnostics) {
	var diags diag.Diagnostics

	var planGroups, stateGroups types.List
	diags.Append(req.Plan.GetAttribute(ctx, path.Root("server_groups"), &planGroups)...)
	diags.Append(req.State.GetAttribute(ctx, path.Root("server_groups"), &stateGroups)...)
	if diags.HasError() {
		return false, diags
	}
	if planGroups.IsUnknown() || listEmpty(planGroups) != listEmpty(stateGroups) {
		return true, diags
	}

	// auto_patch is read from configuration, not from the plan. The framework
	// hands every attribute modifier the plan as proposed, before any modifier
	// has run, so an auto_patch the configuration leaves out is unknown there
	// whenever anything else changes -- even though its own UseStateForUnknown
	// is about to keep the state value. Null in configuration means exactly
	// that: unchanged.
	var configuration types.Object
	diags.Append(req.Config.GetAttribute(ctx, path.Root("configuration"), &configuration)...)
	if diags.HasError() {
		return false, diags
	}
	if configuration.IsUnknown() {
		return true, diags
	}
	if configuration.IsNull() {
		return false, diags
	}

	autoPatch := path.Root("configuration").AtName("auto_patch")
	var configAutoPatch, stateAutoPatch types.Bool
	diags.Append(req.Config.GetAttribute(ctx, autoPatch, &configAutoPatch)...)
	diags.Append(req.State.GetAttribute(ctx, autoPatch, &stateAutoPatch)...)
	if diags.HasError() {
		return false, diags
	}
	switch {
	case configAutoPatch.IsUnknown():
		return true, diags
	case configAutoPatch.IsNull():
		return false, diags
	default:
		return !configAutoPatch.Equal(stateAutoPatch), diags
	}
}

// listEmpty treats a null list as empty: flatten normalises a policy with no
// groups to [], but a null must not read as a change against it.
func listEmpty(l types.List) bool {
	return l.IsNull() || len(l.Elements()) == 0
}
