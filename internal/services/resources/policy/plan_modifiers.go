package policy

import (
	"context"

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
