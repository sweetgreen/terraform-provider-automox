package policy

import (
	"context"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/diag"

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

// TestUnitMergeConfigurationPreservesUnmodeledKeys pins the guard against an
// update silently dropping configuration this provider does not model.
//
// The schema models 43 configuration keys and the 26 policies in org 120547 set
// 36 between them, of which exactly one is unmodeled: `secrets`, present on all
// 26. That is how a worklet binds a Shared Secret, and the device-cleanup worklet
// does nothing but report the absence of its apiKey without it.
//
// An earlier version of this comment claimed 22 modeled and 18 unmodeled. That
// came from a regex over configuration.go which missed attributes declared on one
// line; the fixture below still exercises several modeled keys, which is
// deliberate — the merge must be indifferent to whether a key is modeled.
//
// The loss would be invisible. An unmodeled key has no attribute to diff, so it
// cannot appear in a plan for anyone to review — the apply would simply succeed
// and the setting would be gone.
func TestUnitMergeConfigurationPreservesUnmodeledKeys(t *testing.T) {
	current := map[string]any{
		"patch_rule":                      "advanced",
		"filter_type":                     "all",
		"secrets":                         map[string]any{"apiKey": map[string]any{"name": "apiKey"}},
		"pending_reboot_deferral_enabled": true,
		"notify_deferred_reboot_user":     true,
		"is_patch_tuesday":                true,
	}
	writing := map[string]any{
		"patch_rule":  "advanced",
		"filter_type": "all",
		"auto_reboot": false,
	}

	merged := mergeConfiguration(current, writing)

	// secrets is the one genuinely unmodeled key; the rest are modeled but absent
	// from what is being written, and must survive for the same reason.
	for _, key := range []string{"secrets", "pending_reboot_deferral_enabled",
		"notify_deferred_reboot_user", "is_patch_tuesday"} {
		if _, ok := merged[key]; !ok {
			t.Errorf("update would drop %q, which Automox holds and this write does not "+
				"carry; for an unmodeled key such as secrets the loss would not appear "+
				"in any plan", key)
		}
	}

	// What the provider is writing must still win, or an update would be a no-op.
	if merged["auto_reboot"] != false {
		t.Error("merge lost the attribute being written")
	}
	if merged["patch_rule"] != "advanced" {
		t.Error("merge corrupted a modeled attribute")
	}

	// The inputs must not be mutated: body["configuration"] and the read result
	// are both used again after this call.
	if _, ok := writing["secrets"]; ok {
		t.Error("mergeConfiguration mutated its second argument")
	}
	if len(current) != 6 {
		t.Error("mergeConfiguration mutated its first argument")
	}
}

// TestUnitUpdateMergesConfiguration checks that Update actually calls
// mergeConfiguration.
//
// TestUnitMergeConfigurationPreservesUnmodeledKeys proves the function is
// correct, which is not the same as proving it is used: deleting the call from
// Update still compiles and still leaves that test passing, while restoring
// exactly the data loss it was written to prevent. Nothing else fails either,
// because an unmodeled key produces no plan diff.
//
// This is a source scan rather than a behavioural test because exercising Update
// for real means writing to a live Automox policy, and there is no sandbox
// organization — the write would be against the fleet the guard exists to
// protect.
func TestUnitUpdateMergesConfiguration(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "crud.go", nil, 0)
	if err != nil {
		t.Fatalf("parse crud.go: %v", err)
	}

	var update *ast.FuncDecl
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if ok && fn.Name.Name == "Update" && fn.Recv != nil {
			update = fn
			break
		}
	}
	if update == nil {
		t.Fatal("no Update method found in crud.go; this guard is not looking where the " +
			"code is and would pass however broken the update path was")
	}

	found := false
	ast.Inspect(update, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if ident, ok := call.Fun.(*ast.Ident); ok && ident.Name == "mergeConfiguration" {
			found = true
			return false
		}
		return true
	})

	if !found {
		t.Error("Update does not call mergeConfiguration, so an update will drop every " +
			"configuration key this provider does not model — including the secrets a " +
			"worklet needs — with nothing appearing in the plan")
	}
}

// TestUnitSecretBindingsIgnoresSharedCounts pins what a binding is reduced to.
//
// numPolicies counts how many policies share a secret, so it changes when that
// secret is bound to some unrelated policy. Recording it would report drift on
// this policy for a change that did not touch it, and a warning that cries wolf
// is worse than none. description is free text and does not change what a
// worklet can do.
func TestUnitSecretBindingsIgnoresSharedCounts(t *testing.T) {
	before := secretBindings(map[string]any{"secrets": map[string]any{
		"apiKey": map[string]any{
			"id": "67102549-9443-4bdf-a00a-8f312ff48920", "name": "apiKey",
			"numPolicies": float64(1), "description": "prod key",
		},
	}})
	after := secretBindings(map[string]any{"secrets": map[string]any{
		"apiKey": map[string]any{
			"id": "67102549-9443-4bdf-a00a-8f312ff48920", "name": "apiKey",
			"numPolicies": float64(7), "description": "prod key (shared)",
		},
	}})

	if len(before) != 1 || before["apiKey"] != "67102549-9443-4bdf-a00a-8f312ff48920" {
		t.Fatalf("binding not reduced to name -> id: %v", before)
	}
	if before["apiKey"] != after["apiKey"] {
		t.Error("binding the same secret elsewhere changed this policy's recorded binding, " +
			"which would report drift for a change that did not touch this policy")
	}

	if got := secretBindings(map[string]any{}); len(got) != 0 {
		t.Errorf("a policy with no secrets should record none, got %v", got)
	}
}

// TestUnitWarnSecretBindingDrift covers the three ways a binding can change and
// the two cases that must stay quiet.
func TestUnitWarnSecretBindingDrift(t *testing.T) {
	mapOf := func(t *testing.T, m map[string]string) types.Map {
		t.Helper()
		v, d := types.MapValueFrom(context.Background(), types.StringType, m)
		if d.HasError() {
			t.Fatalf("building map: %v", d.Errors())
		}
		return v
	}

	cases := map[string]struct {
		prior   types.Map
		current map[string]string
		warn    bool
		expect  string
	}{
		"unchanged":   {mapOf(t, map[string]string{"apiKey": "a"}), map[string]string{"apiKey": "a"}, false, ""},
		"unbound":     {mapOf(t, map[string]string{"apiKey": "a"}), map[string]string{}, true, "was unbound"},
		"bound":       {mapOf(t, map[string]string{}), map[string]string{"apiKey": "a"}, true, "was bound"},
		"replaced":    {mapOf(t, map[string]string{"apiKey": "a"}), map[string]string{"apiKey": "b"}, true, "different secret"},
		"no baseline": {types.MapNull(types.StringType), map[string]string{"apiKey": "a"}, false, ""},
		"both empty":  {mapOf(t, map[string]string{}), map[string]string{}, false, ""},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			var diags diag.Diagnostics
			warnSecretBindingDrift(context.Background(), "a policy", tc.prior, tc.current, &diags)

			if got := diags.WarningsCount(); (got > 0) != tc.warn {
				t.Fatalf("warned=%v, want %v (%v)", got > 0, tc.warn, diags.Warnings())
			}
			if tc.warn && !strings.Contains(diags.Warnings()[0].Detail(), tc.expect) {
				t.Errorf("warning does not say what changed; want %q, got %q",
					tc.expect, diags.Warnings()[0].Detail())
			}
			if diags.ErrorsCount() > 0 {
				t.Error("binding drift must not be an error: the provider cannot write bindings, " +
					"so failing the read would leave the policy unmanageable")
			}
		})
	}
}

// TestUnitReadWarnsOnSecretBindingDrift checks that Read actually calls
// warnSecretBindingDrift.
//
// Same reasoning as TestUnitUpdateMergesConfiguration: the function being
// correct is not the same as it being used. Deleting the call from Read
// compiles cleanly, leaves every other test passing, and restores exactly the
// silence the warning exists to break — nothing else fails, because the whole
// point is that this drift produces no plan diff.
func TestUnitReadWarnsOnSecretBindingDrift(t *testing.T) {
	assertMethodCalls(t, "Read", "warnSecretBindingDrift")
}

// assertMethodCalls fails unless the named method on the resource calls the
// named function somewhere in its body.
func assertMethodCalls(t *testing.T, method, callee string) {
	t.Helper()

	file, err := parser.ParseFile(token.NewFileSet(), "crud.go", nil, 0)
	if err != nil {
		t.Fatalf("parse crud.go: %v", err)
	}

	var fn *ast.FuncDecl
	for _, decl := range file.Decls {
		d, ok := decl.(*ast.FuncDecl)
		if ok && d.Name.Name == method && d.Recv != nil {
			fn = d
			break
		}
	}
	if fn == nil {
		t.Fatalf("no %s method in crud.go; this guard is not looking where the code is "+
			"and would pass however broken that method was", method)
	}

	found := false
	ast.Inspect(fn, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if ident, ok := call.Fun.(*ast.Ident); ok && ident.Name == callee {
			found = true
			return false
		}
		return true
	})

	if !found {
		t.Errorf("%s does not call %s, so the behaviour it provides is absent while every "+
			"other test still passes", method, callee)
	}
}
