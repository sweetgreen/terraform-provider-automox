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

// statusCase describes one planned update for the status modifier. The state
// side is always a refreshed patch policy; each field describes how the
// configuration (and therefore the plan) differs from it.
type statusCase struct {
	stateGroups    []int64
	stateAutoPatch bool
	stateStatus    string

	planGroups        []int64 // nil means "same as state"
	planGroupsUnknown bool
	// configAutoPatch is the configured auto_patch: nil leaves it out of
	// configuration altogether, which is the common case for callers that do
	// not manage it.
	configAutoPatch        *bool
	configAutoPatchUnknown bool
	notes                  string // "" means "same as state"
	scheduleTime           string // "" means "same as state"
	create                 bool

	wantUnknown bool
}

func boolPtr(b bool) *bool { return &b }

// runStatusModifier builds real State, Plan and Config values from the
// resource schema, the way the framework does, and runs the status modifier.
//
// The plan is the *proposed* plan: the framework passes every attribute
// modifier the plan as it stood before any modifier ran, so an Optional+Computed
// attribute the configuration leaves out (auto_patch, typically) is unknown in
// it whenever anything else changes. The test reproduces that, because that is
// exactly the trap a status modifier reading the plan would fall into.
func runStatusModifier(t *testing.T, tc statusCase) planmodifier.StringResponse {
	t.Helper()
	ctx := context.Background()

	var schemaResp resource.SchemaResponse
	(&policyResource{}).Schema(ctx, resource.SchemaRequest{}, &schemaResp)
	resourceSchema := schemaResp.Schema

	status := tc.stateStatus
	stateModel, diags := flatten(ctx, &apiPolicy{
		ID:             726918,
		Name:           "Retail BOH Windows 11 - Windows Updates Policy (PHASE 3)",
		PolicyTypeName: TypePatch,
		Notes:          "phase 3",
		ServerGroups:   tc.stateGroups,
		ScheduleDays:   4,
		ScheduleTime:   "03:00",
		Status:         &status,
		Configuration:  map[string]any{"auto_patch": tc.stateAutoPatch, "patch_rule": PatchRuleAdvanced},
	}, policyModel{})
	if diags.HasError() {
		t.Fatalf("flatten state: %v", diags.Errors())
	}

	planModel := stateModel
	planModel.Status = types.StringUnknown()
	if tc.planGroups != nil {
		groups, d := types.ListValueFrom(ctx, types.Int64Type, tc.planGroups)
		if d.HasError() {
			t.Fatalf("plan server_groups: %v", d.Errors())
		}
		planModel.Groups = groups
	}
	if tc.planGroupsUnknown {
		planModel.Groups = types.ListUnknown(types.Int64Type)
	}
	if tc.notes != "" {
		planModel.Notes = types.StringValue(tc.notes)
	}
	if tc.scheduleTime != "" {
		planModel.ScheduleTime = types.StringValue(tc.scheduleTime)
	}

	plan := tfsdk.Plan{Schema: resourceSchema}
	if d := plan.Set(ctx, planModel); d.HasError() {
		t.Fatalf("set plan: %v", d.Errors())
	}

	autoPatch := path.Root("configuration").AtName("auto_patch")
	var configAutoPatch, planAutoPatch types.Bool
	switch {
	case tc.configAutoPatchUnknown:
		configAutoPatch, planAutoPatch = types.BoolUnknown(), types.BoolUnknown()
	case tc.configAutoPatch == nil:
		// Left out of configuration: null there, unknown in the proposed plan
		// (Optional+Computed), and kept from state by its own modifier later.
		configAutoPatch, planAutoPatch = types.BoolNull(), types.BoolUnknown()
	default:
		configAutoPatch = types.BoolValue(*tc.configAutoPatch)
		planAutoPatch = configAutoPatch
	}

	config := tfsdk.Plan{Schema: resourceSchema}
	if d := config.Set(ctx, planModel); d.HasError() {
		t.Fatalf("set config: %v", d.Errors())
	}
	if d := config.SetAttribute(ctx, autoPatch, configAutoPatch); d.HasError() {
		t.Fatalf("set config auto_patch: %v", d.Errors())
	}
	if d := config.SetAttribute(ctx, path.Root("status"), types.StringNull()); d.HasError() {
		t.Fatalf("set config status: %v", d.Errors())
	}
	if d := plan.SetAttribute(ctx, autoPatch, planAutoPatch); d.HasError() {
		t.Fatalf("set plan auto_patch: %v", d.Errors())
	}

	state := tfsdk.State{Schema: resourceSchema}
	stateValue := stateModel.Status
	if tc.create {
		state.Raw = tftypes.NewValue(resourceSchema.Type().TerraformType(ctx), nil)
		stateValue = types.StringNull()
	} else if d := state.Set(ctx, stateModel); d.HasError() {
		t.Fatalf("set state: %v", d.Errors())
	}

	req := planmodifier.StringRequest{
		Path:        path.Root("status"),
		Plan:        plan,
		PlanValue:   types.StringUnknown(),
		State:       state,
		StateValue:  stateValue,
		Config:      tfsdk.Config{Schema: resourceSchema, Raw: config.Raw},
		ConfigValue: types.StringNull(),
	}
	resp := planmodifier.StringResponse{PlanValue: req.PlanValue}
	statusUseStateForUnknown().PlanModifyString(ctx, req, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("plan modify: %v", resp.Diagnostics.Errors())
	}
	return resp
}

// TestUnitStatusUnknownOnlyWhenItsDriversChange is the offline regression for
// sweetgreen/terraform-infrastructure#2149, where activating BOH ring phase 3
// (policy 726918: server_groups [] -> [446992], auto_patch false -> true) failed
// with ".status: was cty.StringVal(\"inactive\"), but now
// cty.StringVal(\"active\")" after the change had landed in Automox.
//
// A read of all 42 policies in org 120547 found status == auto_patch on every
// patch policy, with groups and a zero schedule making no difference on their
// own. So status is left unknown when auto_patch changes, and when
// server_groups moves between empty and non-empty (the case the task requires
// and the data cannot rule out). Everything else keeps the refreshed value so
// plans stay quiet.
func TestUnitStatusUnknownOnlyWhenItsDriversChange(t *testing.T) {
	t.Parallel()

	cases := map[string]statusCase{
		"unchanged policy keeps status": {
			stateGroups: []int64{446992}, stateAutoPatch: true, stateStatus: "active",
			configAutoPatch: boolPtr(true),
		},
		"terraform-infrastructure#2149: groups attached and auto_patch enabled": {
			stateGroups: []int64{}, stateAutoPatch: false, stateStatus: "inactive",
			planGroups: []int64{446992}, configAutoPatch: boolPtr(true),
			wantUnknown: true,
		},
		"groups attached to a policy that had none": {
			stateGroups: []int64{}, stateAutoPatch: false, stateStatus: "inactive",
			planGroups: []int64{446992}, configAutoPatch: boolPtr(false),
			wantUnknown: true,
		},
		"every group removed": {
			stateGroups: []int64{446992}, stateAutoPatch: true, stateStatus: "active",
			planGroups: []int64{}, configAutoPatch: boolPtr(true),
			wantUnknown: true,
		},
		"groups moved between non-empty sets keeps status": {
			stateGroups: []int64{446990}, stateAutoPatch: true, stateStatus: "active",
			planGroups: []int64{446991, 446992}, configAutoPatch: boolPtr(true),
		},
		"server_groups unknown at plan time": {
			stateGroups: []int64{446992}, stateAutoPatch: true, stateStatus: "active",
			planGroupsUnknown: true, configAutoPatch: boolPtr(true),
			wantUnknown: true,
		},
		"auto_patch enabled": {
			stateGroups: []int64{446992}, stateAutoPatch: false, stateStatus: "inactive",
			configAutoPatch: boolPtr(true),
			wantUnknown:     true,
		},
		"auto_patch disabled": {
			stateGroups: []int64{446992}, stateAutoPatch: true, stateStatus: "active",
			configAutoPatch: boolPtr(false),
			wantUnknown:     true,
		},
		"auto_patch unknown at plan time": {
			stateGroups: []int64{446992}, stateAutoPatch: true, stateStatus: "active",
			configAutoPatchUnknown: true,
			wantUnknown:            true,
		},
		"auto_patch left out of configuration while notes change keeps status": {
			stateGroups: []int64{446992}, stateAutoPatch: true, stateStatus: "active",
			notes: "phase 3, reworded",
		},
		"notes changed keeps status": {
			stateGroups: []int64{446992}, stateAutoPatch: true, stateStatus: "active",
			configAutoPatch: boolPtr(true), notes: "phase 3, reworded",
		},
		"schedule_time changed keeps status": {
			stateGroups: []int64{}, stateAutoPatch: false, stateStatus: "inactive",
			configAutoPatch: boolPtr(false), scheduleTime: "04:00",
		},
		"create leaves status unknown": {
			stateGroups: []int64{}, stateAutoPatch: false, stateStatus: "inactive",
			configAutoPatch: boolPtr(false), create: true,
			wantUnknown: true,
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := runStatusModifier(t, tc).PlanValue
			switch {
			case tc.wantUnknown && !got.IsUnknown():
				t.Fatalf("status planned as %v, want known after apply: Automox may "+
					"change it, and a promised value fails the apply after the write", got)
			case !tc.wantUnknown && got.IsUnknown():
				t.Fatalf("status is known after apply, want the refreshed %q: nothing "+
					"that drives it is changing", tc.stateStatus)
			case !tc.wantUnknown && got.ValueString() != tc.stateStatus:
				t.Fatalf("status = %v, want %q", got, tc.stateStatus)
			}
		})
	}
}

// TestUnitStatusUsesDriverAwareModifier pins the schema wiring: a generic
// UseStateForUnknown on status is the bug this replaces.
func TestUnitStatusUsesDriverAwareModifier(t *testing.T) {
	t.Parallel()

	var response resource.SchemaResponse
	(&policyResource{}).Schema(context.Background(), resource.SchemaRequest{}, &response)
	status := response.Schema.Attributes["status"].(schema.StringAttribute)

	if status.Optional || status.Required || !status.Computed {
		t.Fatalf("status must stay Computed-only, got Optional=%v Required=%v Computed=%v",
			status.Optional, status.Required, status.Computed)
	}
	if len(status.PlanModifiers) != 1 {
		t.Fatalf("status has %d plan modifiers, want 1", len(status.PlanModifiers))
	}
	if _, ok := status.PlanModifiers[0].(statusUseStateForUnknownModifier); !ok {
		t.Fatalf("status plan modifier is %T, want statusUseStateForUnknownModifier", status.PlanModifiers[0])
	}
}
