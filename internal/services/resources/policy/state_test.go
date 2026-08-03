package policy

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// TestUnitFlattenServerGroupsNeverNull pins the fix for policies that target no
// server groups.
//
// Automox returns an explicit JSON null for those, and a nil slice flattens to a
// null list rather than an empty one. Because server_groups is Required, that
// made such a policy impossible to manage at all: importing it failed with
// "Must set a configuration value for the server_groups attribute", and no
// configuration could satisfy the error, because the offending value was the one
// Read produced rather than anything the practitioner wrote.
//
// Eleven of the 26 policies in org 120547 were in that state, so this is the
// ordinary case rather than an edge one.
func TestUnitFlattenServerGroupsNeverNull(t *testing.T) {
	cases := map[string]struct {
		raw  string
		want []int64
	}{
		"explicit null":  {`{"id":1,"server_groups":null}`, []int64{}},
		"absent":         {`{"id":1}`, []int64{}},
		"empty array":    {`{"id":1,"server_groups":[]}`, []int64{}},
		"one group":      {`{"id":1,"server_groups":[230685]}`, []int64{230685}},
		"several groups": {`{"id":1,"server_groups":[1,2,3]}`, []int64{1, 2, 3}},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			var api apiPolicy
			if err := json.Unmarshal([]byte(tc.raw), &api); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}

			model, diags := flatten(context.Background(), &api, policyModel{})
			if diags.HasError() {
				t.Fatalf("flatten reported errors: %v", diags.Errors())
			}

			if model.Groups.IsNull() {
				t.Fatal("server_groups is null; it is a Required attribute, so a null " +
					"makes the policy unmanageable — import fails and no configuration " +
					"can fix it")
			}
			if model.Groups.IsUnknown() {
				t.Fatal("server_groups is unknown after a read")
			}

			var got []int64
			if d := model.Groups.ElementsAs(context.Background(), &got, false); d.HasError() {
				t.Fatalf("ElementsAs: %v", d.Errors())
			}
			if len(got) != len(tc.want) {
				t.Fatalf("server_groups = %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("server_groups = %v, want %v", got, tc.want)
				}
			}
		})
	}
}

// TestUnitFilterTypeAcceptsAll guards the filter_type value set against being
// narrowed back to what creating a policy happens to produce.
//
// The original set was include/exclude/severity, inferred from policies this
// provider created. Automox stores "all" for every policy whose patch_rule is
// "advanced" — 11 of the 26 in org 120547 — and because the value round-trips
// through the schema, rejecting it rejected those policies on import, not only
// on write.
func TestUnitFilterTypeAcceptsAll(t *testing.T) {
	for _, accepted := range []string{FilterTypeAll, FilterTypeInclude, FilterTypeExclude, FilterTypeSeverity} {
		if !filterTypeAccepts(t, accepted) {
			t.Errorf("filter_type rejects %q, which Automox stores and returns", accepted)
		}
	}

	if filterTypeAccepts(t, "not-a-filter-type") {
		t.Error("filter_type accepted an arbitrary value, so the validator is not running " +
			"and this test proves nothing")
	}
}

// filterTypeAccepts runs the schema's declared validators for filter_type,
// rather than asserting against the constant list, so that a validator which
// stops matching the constants is caught.
func filterTypeAccepts(t *testing.T, value string) bool {
	t.Helper()

	attribute, ok := configurationSchema().Attributes["filter_type"]
	if !ok {
		t.Fatal("configuration has no filter_type attribute")
	}

	stringAttribute, ok := attribute.(schema.StringAttribute)
	if !ok {
		t.Fatalf("filter_type is %T, not a schema.StringAttribute", attribute)
	}
	if len(stringAttribute.Validators) == 0 {
		t.Fatal("filter_type declares no validators")
	}

	for _, v := range stringAttribute.Validators {
		req := validator.StringRequest{ConfigValue: types.StringValue(value)}
		resp := &validator.StringResponse{}
		v.ValidateString(context.Background(), req, resp)
		if resp.Diagnostics.HasError() {
			return false
		}
	}
	return true
}
