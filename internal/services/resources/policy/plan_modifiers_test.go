package policy

import (
	"context"
	"testing"

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
