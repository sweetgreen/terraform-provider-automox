package policy

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

// TestUnitScheduleOnlyPlanPreservesComputedPolicyState is the offline regression
// for schedule-only updates. Terraform makes unconfigured Optional+Computed
// values unknown whenever any configured value changes; these modifiers keep
// the values returned by the refresh instead of producing unrelated
// "known after apply" entries.
func TestUnitScheduleOnlyPlanPreservesComputedPolicyState(t *testing.T) {
	t.Parallel()

	var response resource.SchemaResponse
	(&policyResource{}).Schema(context.Background(), resource.SchemaRequest{}, &response)

	assertModifierCount(t, response.Schema.Attributes["status"], 1)
	assertModifierCount(t, response.Schema.Attributes["server_count"], 1)
	assertModifierCount(t, response.Schema.Attributes["create_time"], 1)
	assertModifierCount(t, response.Schema.Attributes["secret_bindings"], 1)

	configuration := configurationSchema().GetAttributes()
	for _, name := range []string{
		"auto_patch",
		"filter_type",
		"filters",
		"evaluation_code",
		"notify_user",
		"custom_notification_deferment_periods",
		"install_notification_deadline",
	} {
		assertModifierCount(t, configuration[name], 1)
	}
}

// TestUnitChangingAPIStateIsNotHidden pins the refresh semantics on which the
// state-preserving modifiers rely. UseStateForUnknown copies the current state
// supplied to planning, so a value changed by the API during refresh remains
// the value in the plan; it does not resurrect an older pre-refresh value.
func TestUnitChangingAPIStateIsNotHidden(t *testing.T) {
	t.Parallel()

	attribute := configurationSchema().GetAttributes()["evaluation_code"].(schema.StringAttribute)
	request := planmodifier.StringRequest{
		ConfigValue: types.StringNull(),
		PlanValue:   types.StringUnknown(),
		State: tfsdk.State{
			Raw: tftypes.NewValue(tftypes.Bool, true),
		},
		StateValue: types.StringValue("changed outside Terraform"),
	}
	response := planmodifier.StringResponse{PlanValue: request.PlanValue}
	attribute.PlanModifiers[0].PlanModifyString(context.Background(), request, &response)

	if got := response.PlanValue; got.IsUnknown() || got.ValueString() != "changed outside Terraform" {
		t.Fatalf("planned evaluation_code = %#v, want refreshed API value", got)
	}
}

func TestUnitTrulyApplyTimeAndAdvancedCompatibilityValuesStayUnknown(t *testing.T) {
	t.Parallel()

	var response resource.SchemaResponse
	(&policyResource{}).Schema(context.Background(), resource.SchemaRequest{}, &response)
	assertModifierCount(t, response.Schema.Attributes["next_remediation"], 0)

	advanced := configurationSchema().GetAttributes()["advanced_filter"].(schema.ListNestedAttribute)
	rule := advanced.NestedObject.Attributes

	// The canonical condition remains planned from configuration/API comparison.
	// Preserving it for a legacy configuration that supplies only op could make
	// the refreshed condition override a real op edit on the write path.
	assertModifierCount(t, rule["condition"], 0)
	// The read-only legacy mirror is safe to preserve when omitted.
	assertModifierCount(t, rule["op"], 1)
}

// TestUnitServerCountUnknownOnlyWhenServerGroupsChange is the offline
// regression for the "produced an unexpected new value: .server_count" failure
// reported live moving a policy between server groups of very different sizes.
// server_count is API-populated from live group membership, so it can only be
// safely carried forward from refreshed state when server_groups itself is not
// part of the plan.
func TestUnitServerCountUnknownOnlyWhenServerGroupsChange(t *testing.T) {
	t.Parallel()

	var schemaResp resource.SchemaResponse
	(&policyResource{}).Schema(context.Background(), resource.SchemaRequest{}, &schemaResp)
	resourceSchema := schemaResp.Schema

	stateCount := int64(166)
	stateModel, diags := flatten(context.Background(), &apiPolicy{
		ID:             1,
		Name:           "final_retail_windows_cleanup",
		PolicyTypeName: TypeWorklet,
		ServerGroups:   []int64{230685, 230686, 230687},
		ServerCount:    &stateCount,
		Configuration:  map[string]any{},
	}, policyModel{})
	if diags.HasError() {
		t.Fatalf("flatten state: %v", diags.Errors())
	}

	cases := map[string]struct {
		planGroups []int64
		wantKnown  bool
	}{
		"server_groups unchanged": {[]int64{230685, 230686, 230687}, true},
		"server_groups changed":   {[]int64{1}, false},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()

			planModel := stateModel
			groups, groupDiags := types.ListValueFrom(ctx, types.Int64Type, tc.planGroups)
			if groupDiags.HasError() {
				t.Fatalf("build plan server_groups: %v", groupDiags.Errors())
			}
			planModel.Groups = groups
			planModel.ServerCount = types.Int64Unknown()

			state := tfsdk.State{Schema: resourceSchema}
			if d := state.Set(ctx, stateModel); d.HasError() {
				t.Fatalf("set state: %v", d.Errors())
			}
			plan := tfsdk.Plan{Schema: resourceSchema}
			if d := plan.Set(ctx, planModel); d.HasError() {
				t.Fatalf("set plan: %v", d.Errors())
			}

			req := planmodifier.Int64Request{
				Path:        path.Root("server_count"),
				Plan:        plan,
				PlanValue:   types.Int64Unknown(),
				State:       state,
				StateValue:  stateModel.ServerCount,
				Config:      tfsdk.Config{Schema: resourceSchema},
				ConfigValue: types.Int64Null(),
			}
			resp := planmodifier.Int64Response{PlanValue: req.PlanValue}

			serverCountUseStateForUnknown().PlanModifyInt64(ctx, req, &resp)
			if resp.Diagnostics.HasError() {
				t.Fatalf("plan modify: %v", resp.Diagnostics.Errors())
			}

			switch {
			case tc.wantKnown && resp.PlanValue.IsUnknown():
				t.Fatalf("server_count is unknown after apply, want the refreshed state value %v",
					stateModel.ServerCount)
			case tc.wantKnown && resp.PlanValue.ValueInt64() != stateModel.ServerCount.ValueInt64():
				t.Fatalf("server_count = %v, want %v", resp.PlanValue, stateModel.ServerCount)
			case !tc.wantKnown && !resp.PlanValue.IsUnknown():
				t.Fatalf("server_count = %v, want known after apply since server_groups changed",
					resp.PlanValue)
			}
		})
	}
}

// A policy that targeted no groups at all is the case that took down
// sweetgreen/terraform-infrastructure#1687: server_groups went from [] to one
// group of 50 devices, so server_count went 0 -> 50 and the apply failed the
// consistency check after the change had already been written. An empty state
// list is worth its own case -- it is the shape every newly-attached policy
// has, and equality against an empty list is easy to get wrong.
func TestUnitServerCountUnknownWhenGroupsAttachedFromNone(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	var schemaResp resource.SchemaResponse
	(&policyResource{}).Schema(ctx, resource.SchemaRequest{}, &schemaResp)
	resourceSchema := schemaResp.Schema

	stateCount := int64(0)
	stateModel, diags := flatten(ctx, &apiPolicy{
		ID:             554218,
		Name:           "Corporate macOS - Security Definitions Updates Policy",
		PolicyTypeName: TypePatch,
		ServerGroups:   []int64{},
		ServerCount:    &stateCount,
		Configuration:  map[string]any{},
	}, policyModel{})
	if diags.HasError() {
		t.Fatalf("flatten state: %v", diags.Errors())
	}

	planModel := stateModel
	groups, groupDiags := types.ListValueFrom(ctx, types.Int64Type, []int64{262169})
	if groupDiags.HasError() {
		t.Fatalf("build plan server_groups: %v", groupDiags.Errors())
	}
	planModel.Groups = groups
	planModel.ServerCount = types.Int64Unknown()

	state := tfsdk.State{Schema: resourceSchema}
	if d := state.Set(ctx, stateModel); d.HasError() {
		t.Fatalf("set state: %v", d.Errors())
	}
	plan := tfsdk.Plan{Schema: resourceSchema}
	if d := plan.Set(ctx, planModel); d.HasError() {
		t.Fatalf("set plan: %v", d.Errors())
	}

	req := planmodifier.Int64Request{
		Path:        path.Root("server_count"),
		Plan:        plan,
		PlanValue:   types.Int64Unknown(),
		State:       state,
		StateValue:  stateModel.ServerCount,
		Config:      tfsdk.Config{Schema: resourceSchema},
		ConfigValue: types.Int64Null(),
	}
	resp := planmodifier.Int64Response{PlanValue: req.PlanValue}

	serverCountUseStateForUnknown().PlanModifyInt64(ctx, req, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("plan modify: %v", resp.Diagnostics.Errors())
	}
	if !resp.PlanValue.IsUnknown() {
		t.Fatalf("server_count = %v, want known after apply: attaching a group to a "+
			"policy that targeted none changes the count the API reports", resp.PlanValue)
	}
}

func assertModifierCount(t *testing.T, attribute schema.Attribute, want int) {
	t.Helper()

	var got int
	switch attribute := attribute.(type) {
	case schema.StringAttribute:
		got = len(attribute.PlanModifiers)
	case schema.BoolAttribute:
		got = len(attribute.PlanModifiers)
	case schema.Int64Attribute:
		got = len(attribute.PlanModifiers)
	case schema.ListAttribute:
		got = len(attribute.PlanModifiers)
	case schema.MapAttribute:
		got = len(attribute.PlanModifiers)
	default:
		t.Fatalf("unsupported attribute type %T", attribute)
	}
	if got != want {
		t.Errorf("%T has %d plan modifiers, want %d", attribute, got, want)
	}
}
