package policy_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"

	automox "github.com/sweetgreen/terraform-provider-automox/internal/provider"
)

// TestUnitAppendingAdvancedFilterIsConsistentAfterApply is the offline
// regression for PLA-464 (sweetgreen/terraform-infrastructure#2275):
//
//	Provider produced inconsistent result after apply:
//	.configuration.advanced_filter[6].op: was null, but now cty.StringVal("does-not-contain")
//
// It drives the real provider server over the plugin protocol against a fake
// Automox API, and reproduces the two halves of the round trip that Terraform
// core owns: the proposed new state handed to PlanResourceChange, and the
// "planned known value must equal applied value" check after
// ApplyResourceChange. Acceptance tests need a live organization and do not run
// in CI; this does.
func TestUnitAppendingAdvancedFilterIsConsistentAfterApply(t *testing.T) {
	ctx := context.Background()
	api := newFakePolicyAPI(t, 42, []map[string]any{
		{"left": "patch-source", "condition": "is", "right": "windowsupdate"},
		{"left": "display-name", "condition": "does-not-contain", "right": "Preview"},
	})
	h := newProtocolHarness(t, api.server.URL)

	// State as v0.1.4 through v0.1.8 wrote it: every entry has op mirrored from
	// condition, because every read copied it there.
	prior := h.importAndRead(ctx, "42")
	prior = setAllAdvancedFilterOps(t, prior, true)

	existing := []filterEntry{
		{"patch-source", "is", "windowsupdate"},
		{"display-name", "does-not-contain", "Preview"},
	}
	appended := append(append([]filterEntry{}, existing...),
		filterEntry{"display-name", "does-not-contain", "SQL Server"})

	// Append an entry, as sweetgreen/terraform-infrastructure#2275 did.
	prior = h.planApplyConsistent(ctx, prior, h.config(appended))
	if got := api.filterCount(); got != 3 {
		t.Fatalf("Automox holds %d advanced filters, want 3", got)
	}

	// A refresh followed by a plan of the same configuration shows no changes.
	refreshed := h.read(ctx, prior)
	planned := h.plan(ctx, refreshed, h.config(appended))
	if !planned.Equal(refreshed) {
		diffs, _ := refreshed.Diff(planned)
		t.Fatalf("plan after refresh is not empty: %v", diffs)
	}
}

// TestUnitChangingAdvancedFilterConditionIsConsistentAfterApply covers the
// sibling of PLA-464: an entry already in state has op mirrored from its old
// condition, so op is planned from state, and the read after apply must not
// replace it with the new condition.
func TestUnitChangingAdvancedFilterConditionIsConsistentAfterApply(t *testing.T) {
	ctx := context.Background()
	api := newFakePolicyAPI(t, 42, []map[string]any{
		{"left": "patch-source", "condition": "is", "right": "windowsupdate"},
	})
	h := newProtocolHarness(t, api.server.URL)

	prior := h.importAndRead(ctx, "42")
	prior = setAllAdvancedFilterOps(t, prior, true)

	changed := []filterEntry{{"patch-source", "is-not", "windowsupdate"}}
	prior = h.planApplyConsistent(ctx, prior, h.config(changed))
	if got := api.filterCondition(0); got != "is-not" {
		t.Fatalf("Automox condition for entry 0 = %q, want is-not", got)
	}

	refreshed := h.read(ctx, prior)
	planned := h.plan(ctx, refreshed, h.config(changed))
	if !planned.Equal(refreshed) {
		diffs, _ := refreshed.Diff(planned)
		t.Fatalf("plan after refresh is not empty: %v", diffs)
	}
}

// TestUnitLegacyOpConfigurationStaysStable covers the v0.1.3 shape that the op
// alias exists for: a configuration that sets op and leaves condition unset.
func TestUnitLegacyOpConfigurationStaysStable(t *testing.T) {
	ctx := context.Background()
	api := newFakePolicyAPI(t, 7, []map[string]any{
		{"left": "patch-source", "condition": "is", "right": "windowsupdate"},
	})
	h := newProtocolHarness(t, api.server.URL)

	prior := h.importAndRead(ctx, "7")
	prior = setAllAdvancedFilterOps(t, prior, true)

	legacy := func(entries ...filterEntry) tftypes.Value {
		cfg := h.config(entries)
		return setAllAdvancedFilterOps(t, cfg, false)
	}

	// Append an entry written the v0.1.3 way.
	cfg := legacy(
		filterEntry{"patch-source", "is", "windowsupdate"},
		filterEntry{"display-name", "contains", "Chrome"},
	)
	prior = h.planApplyConsistent(ctx, prior, cfg)
	if got := api.filterCondition(1); got != "contains" {
		t.Fatalf("Automox condition for the appended legacy entry = %q, want contains", got)
	}

	refreshed := h.read(ctx, prior)
	planned := h.plan(ctx, refreshed, cfg)
	if !planned.Equal(refreshed) {
		diffs, _ := refreshed.Diff(planned)
		t.Fatalf("plan after refresh is not empty: %v", diffs)
	}
}

type filterEntry struct{ left, condition, right string }

// --- fake Automox API ------------------------------------------------------

type fakePolicyAPI struct {
	server *httptest.Server
	mu     sync.Mutex
	policy map[string]any
}

func newFakePolicyAPI(t *testing.T, id int64, filters []map[string]any) *fakePolicyAPI {
	t.Helper()
	rules := make([]any, 0, len(filters))
	for _, f := range filters {
		// Live Automox returns op: null on every advanced filter entry.
		rule := maps.Clone(f)
		rule["op"] = nil
		rules = append(rules, rule)
	}
	f := &fakePolicyAPI{policy: map[string]any{
		"id":                      id,
		"uuid":                    "00000000-0000-0000-0000-000000000042",
		"name":                    "retail_qsr_windows_updates",
		"policy_type_name":        "patch",
		"notes":                   "",
		"organization_id":         1,
		"server_groups":           []any{},
		"schedule_days":           0,
		"schedule_weeks_of_month": 0,
		"schedule_months":         0,
		"schedule_time":           "00:00",
		"use_scheduled_timezone":  false,
		"status":                  "inactive",
		"server_count":            0,
		"create_time":             "2026-08-01T00:00:00+0000",
		"configuration": map[string]any{
			"patch_rule":      "advanced",
			"auto_patch":      false,
			"auto_reboot":     false,
			"advanced_filter": rules,
		},
	}}
	path := fmt.Sprintf("/policies/%d", id)
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if r.URL.Path != path {
			http.NotFound(w, r)
			return
		}
		switch r.Method {
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(f.policy)
		case http.MethodPut:
			body, _ := io.ReadAll(r.Body)
			var update map[string]any
			if err := json.Unmarshal(body, &update); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			if cfg, ok := update["configuration"].(map[string]any); ok {
				if rules, ok := cfg["advanced_filter"].([]any); ok {
					for _, raw := range rules {
						rule := raw.(map[string]any)
						if _, sent := rule["op"]; sent {
							http.Error(w, `{"errors":["op is not a valid advanced rule key"]}`, http.StatusBadRequest)
							return
						}
						if _, sent := rule["condition"]; !sent {
							http.Error(w, `{"errors":["All advanced rules must contain a 'left', 'right' and 'condition'"]}`, http.StatusBadRequest)
							return
						}
						rule["op"] = nil
					}
				}
			}
			maps.Copy(f.policy, update)
			w.WriteHeader(http.StatusNoContent)
		default:
			http.Error(w, "unexpected method", http.StatusMethodNotAllowed)
		}
	}))
	t.Cleanup(f.server.Close)
	return f
}

func (f *fakePolicyAPI) rules() []any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.policy["configuration"].(map[string]any)["advanced_filter"].([]any)
}

func (f *fakePolicyAPI) filterCount() int { return len(f.rules()) }

func (f *fakePolicyAPI) filterCondition(i int) string {
	c, _ := f.rules()[i].(map[string]any)["condition"].(string)
	return c
}

// --- protocol harness ------------------------------------------------------

type protocolHarness struct {
	t      *testing.T
	server tfprotov6.ProviderServer
	schema *tfprotov6.Schema
	typ    tftypes.Object
}

const policyTypeName = "automox_policy"

func newProtocolHarness(t *testing.T, baseURL string) *protocolHarness {
	t.Helper()
	ctx := context.Background()
	server, err := providerserver.NewProtocol6WithError(automox.New("test")())()
	if err != nil {
		t.Fatal(err)
	}

	schemas, err := server.GetProviderSchema(ctx, &tfprotov6.GetProviderSchemaRequest{})
	if err != nil {
		t.Fatal(err)
	}
	failOnDiags(t, "GetProviderSchema", schemas.Diagnostics)

	providerType := schemas.Provider.ValueType().(tftypes.Object)
	providerCfg := tftypes.NewValue(providerType, map[string]tftypes.Value{
		"api_key":         tftypes.NewValue(tftypes.String, "test-key"),
		"organization_id": tftypes.NewValue(tftypes.Number, 1),
		"base_url":        tftypes.NewValue(tftypes.String, baseURL),
	})
	configured, err := server.ConfigureProvider(ctx, &tfprotov6.ConfigureProviderRequest{
		TerraformVersion: "1.9.0",
		Config:           dynamic(t, providerType, providerCfg),
	})
	if err != nil {
		t.Fatal(err)
	}
	failOnDiags(t, "ConfigureProvider", configured.Diagnostics)

	s := schemas.ResourceSchemas[policyTypeName]
	return &protocolHarness{t: t, server: server, schema: s, typ: s.ValueType().(tftypes.Object)}
}

func (h *protocolHarness) importAndRead(ctx context.Context, id string) tftypes.Value {
	h.t.Helper()
	imported, err := h.server.ImportResourceState(ctx, &tfprotov6.ImportResourceStateRequest{
		TypeName: policyTypeName, ID: id,
	})
	if err != nil {
		h.t.Fatal(err)
	}
	failOnDiags(h.t, "ImportResourceState", imported.Diagnostics)
	state, err := imported.ImportedResources[0].State.Unmarshal(h.typ)
	if err != nil {
		h.t.Fatal(err)
	}
	return h.read(ctx, state)
}

func (h *protocolHarness) read(ctx context.Context, state tftypes.Value) tftypes.Value {
	h.t.Helper()
	resp, err := h.server.ReadResource(ctx, &tfprotov6.ReadResourceRequest{
		TypeName: policyTypeName, CurrentState: dynamic(h.t, h.typ, state),
	})
	if err != nil {
		h.t.Fatal(err)
	}
	failOnDiags(h.t, "ReadResource", resp.Diagnostics)
	out, err := resp.NewState.Unmarshal(h.typ)
	if err != nil {
		h.t.Fatal(err)
	}
	return out
}

func (h *protocolHarness) plan(ctx context.Context, prior, config tftypes.Value) tftypes.Value {
	h.t.Helper()
	proposed := proposedNew(h.schema.Block.Attributes, prior, config)
	resp, err := h.server.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{
		TypeName:         policyTypeName,
		PriorState:       dynamic(h.t, h.typ, prior),
		ProposedNewState: dynamic(h.t, h.typ, proposed),
		Config:           dynamic(h.t, h.typ, config),
	})
	if err != nil {
		h.t.Fatal(err)
	}
	failOnDiags(h.t, "PlanResourceChange", resp.Diagnostics)
	planned, err := resp.PlannedState.Unmarshal(h.typ)
	if err != nil {
		h.t.Fatal(err)
	}
	return planned
}

// planApplyConsistent plans and applies config against prior and fails the test
// exactly where Terraform core would report "Provider produced inconsistent
// result after apply". It returns the new state.
func (h *protocolHarness) planApplyConsistent(ctx context.Context, prior, config tftypes.Value) tftypes.Value {
	h.t.Helper()
	planned := h.plan(ctx, prior, config)
	resp, err := h.server.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{
		TypeName:     policyTypeName,
		PriorState:   dynamic(h.t, h.typ, prior),
		PlannedState: dynamic(h.t, h.typ, planned),
		Config:       dynamic(h.t, h.typ, config),
	})
	if err != nil {
		h.t.Fatal(err)
	}
	failOnDiags(h.t, "ApplyResourceChange", resp.Diagnostics)
	applied, err := resp.NewState.Unmarshal(h.typ)
	if err != nil {
		h.t.Fatal(err)
	}

	var problems []string
	_ = tftypes.Walk(planned, func(p *tftypes.AttributePath, v tftypes.Value) (bool, error) {
		if !v.IsKnown() {
			return false, nil // unknown may become anything
		}
		if !v.Type().Is(tftypes.String) && !v.Type().Is(tftypes.Number) && !v.Type().Is(tftypes.Bool) {
			if v.IsNull() {
				got, _, _ := tftypes.WalkAttributePath(applied, p)
				if gv, ok := got.(tftypes.Value); !ok || !gv.IsNull() {
					problems = append(problems, fmt.Sprintf("%s: was null, but now %v", p, got))
				}
				return false, nil
			}
			return true, nil
		}
		got, _, err := tftypes.WalkAttributePath(applied, p)
		gv, ok := got.(tftypes.Value)
		if err != nil || !ok || !gv.Equal(v) {
			problems = append(problems, fmt.Sprintf("%s: was %v, but now %v", p, v, got))
		}
		return false, nil
	})
	if len(problems) > 0 {
		h.t.Fatalf("Provider produced inconsistent result after apply:\n%s", strings.Join(problems, "\n"))
	}
	return applied
}

// config builds the resource configuration the retail QSR policies use: every
// advanced filter entry sets condition and leaves op null.
func (h *protocolHarness) config(filters []filterEntry) tftypes.Value {
	cfgType := h.typ.AttributeTypes["configuration"].(tftypes.Object)
	listType := cfgType.AttributeTypes["advanced_filter"].(tftypes.List)
	ruleType := listType.ElementType.(tftypes.Object)

	rules := make([]tftypes.Value, 0, len(filters))
	for _, f := range filters {
		rules = append(rules, objectOf(ruleType, map[string]tftypes.Value{
			"left":      tftypes.NewValue(tftypes.String, f.left),
			"condition": tftypes.NewValue(tftypes.String, f.condition),
			"right":     tftypes.NewValue(tftypes.String, f.right),
		}))
	}

	return objectOf(h.typ, map[string]tftypes.Value{
		"name":          tftypes.NewValue(tftypes.String, "retail_qsr_windows_updates"),
		"policy_type":   tftypes.NewValue(tftypes.String, "patch"),
		"notes":         tftypes.NewValue(tftypes.String, ""),
		"server_groups": tftypes.NewValue(tftypes.List{ElementType: tftypes.Number}, []tftypes.Value{}),
		"schedule_time": tftypes.NewValue(tftypes.String, "00:00"),
		"schedule_days": tftypes.NewValue(tftypes.Number, 0),
		"configuration": objectOf(cfgType, map[string]tftypes.Value{
			"patch_rule":      tftypes.NewValue(tftypes.String, "advanced"),
			"auto_patch":      tftypes.NewValue(tftypes.Bool, false),
			"auto_reboot":     tftypes.NewValue(tftypes.Bool, false),
			"advanced_filter": tftypes.NewValue(listType, rules),
		}),
	})
}

// setAllAdvancedFilterOps rewrites every advanced filter entry. With fromCondition
// it copies condition into op, as v0.1.4-v0.1.8 reads did; without it, it moves
// condition into op and nulls condition, as a v0.1.3 configuration is written.
func setAllAdvancedFilterOps(t *testing.T, v tftypes.Value, fromCondition bool) tftypes.Value {
	t.Helper()
	out, err := tftypes.Transform(v, func(p *tftypes.AttributePath, v tftypes.Value) (tftypes.Value, error) {
		steps := p.Steps()
		if len(steps) != 3 || steps[0] != tftypes.AttributeName("configuration") ||
			steps[1] != tftypes.AttributeName("advanced_filter") {
			return v, nil
		}
		var rule map[string]tftypes.Value
		if err := v.As(&rule); err != nil {
			return v, err
		}
		rule["op"] = rule["condition"]
		if !fromCondition {
			rule["condition"] = tftypes.NewValue(tftypes.String, nil)
		}
		return tftypes.NewValue(v.Type(), rule), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// --- Terraform core emulation ---------------------------------------------

// proposedNew mirrors Terraform core's objchange.ProposedNew for the attribute
// shapes this resource uses: a computed attribute left null in configuration
// takes its prior value, and nested list elements are correlated with the prior
// list by index. An element with no prior counterpart is taken from
// configuration as-is.
func proposedNew(attrs []*tfprotov6.SchemaAttribute, prior, config tftypes.Value) tftypes.Value {
	if config.IsNull() || !config.IsKnown() {
		return config
	}
	if prior.IsNull() || !prior.IsKnown() {
		prior = tftypes.NewValue(config.Type(), nil)
	}
	var priorAttrs, configAttrs map[string]tftypes.Value
	_ = config.As(&configAttrs)
	if !prior.IsNull() {
		_ = prior.As(&priorAttrs)
	}

	out := make(map[string]tftypes.Value, len(configAttrs))
	for _, a := range attrs {
		cv := configAttrs[a.Name]
		pv, hasPrior := priorAttrs[a.Name]
		if !hasPrior {
			pv = tftypes.NewValue(cv.Type(), nil)
		}
		switch {
		case a.NestedType != nil && a.NestedType.Nesting == tfprotov6.SchemaObjectNestingModeSingle:
			if cv.IsNull() && a.Computed {
				out[a.Name] = pv
			} else {
				out[a.Name] = proposedNew(a.NestedType.Attributes, pv, cv)
			}
		case a.NestedType != nil && a.NestedType.Nesting == tfprotov6.SchemaObjectNestingModeList:
			if cv.IsNull() {
				if a.Computed {
					out[a.Name] = pv
				} else {
					out[a.Name] = cv
				}
				continue
			}
			var configElems, priorElems []tftypes.Value
			_ = cv.As(&configElems)
			if !pv.IsNull() {
				_ = pv.As(&priorElems)
			}
			elems := make([]tftypes.Value, 0, len(configElems))
			for i, ce := range configElems {
				if i >= len(priorElems) {
					elems = append(elems, ce)
					continue
				}
				elems = append(elems, proposedNew(a.NestedType.Attributes, priorElems[i], ce))
			}
			out[a.Name] = tftypes.NewValue(cv.Type(), elems)
		default:
			if cv.IsNull() && a.Computed {
				out[a.Name] = pv
			} else {
				out[a.Name] = cv
			}
		}
	}
	return tftypes.NewValue(config.Type(), out)
}

func objectOf(typ tftypes.Object, set map[string]tftypes.Value) tftypes.Value {
	values := make(map[string]tftypes.Value, len(typ.AttributeTypes))
	for name, attrType := range typ.AttributeTypes {
		if v, ok := set[name]; ok {
			values[name] = v
			continue
		}
		values[name] = tftypes.NewValue(attrType, nil)
	}
	return tftypes.NewValue(typ, values)
}

func dynamic(t *testing.T, typ tftypes.Type, v tftypes.Value) *tfprotov6.DynamicValue {
	t.Helper()
	dv, err := tfprotov6.NewDynamicValue(typ, v)
	if err != nil {
		t.Fatal(err)
	}
	return &dv
}

func failOnDiags(t *testing.T, step string, diags []*tfprotov6.Diagnostic) {
	t.Helper()
	for _, d := range diags {
		if d.Severity == tfprotov6.DiagnosticSeverityError {
			t.Fatalf("%s: %s: %s (%v)", step, d.Summary, d.Detail, d.Attribute)
		}
	}
}
