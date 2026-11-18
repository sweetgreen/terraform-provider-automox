package policy

import (
	"context"
	"fmt"
	"maps"
	"sort"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/sweetgreen/terraform-provider-automox/internal/schedule"
)

// construct builds the create/update body.
//
// PUT is a full replacement, so everything managed is sent every time.
func construct(ctx context.Context, model policyModel, orgID int64) (map[string]any, diag.Diagnostics) {
	var diags diag.Diagnostics

	var groups []int64
	if !model.Groups.IsNull() && !model.Groups.IsUnknown() {
		diags.Append(model.Groups.ElementsAs(ctx, &groups, false)...)
		if diags.HasError() {
			return nil, diags
		}
	}

	days, dayDiags := resolveScheduleMask(ctx, model.ScheduleDays, model.ScheduleDaysOfWeek,
		schedule.EncodeDays, "schedule_days")
	diags.Append(dayDiags...)

	months, monthDiags := resolveScheduleMask(ctx, model.ScheduleMonths, model.ScheduleMonthsOfYear,
		schedule.EncodeMonths, "schedule_months")
	diags.Append(monthDiags...)

	if diags.HasError() {
		return nil, diags
	}

	body := map[string]any{
		// Automox requires the organization in the body as well as in the ?o=
		// query parameter, and rejects the request without it. The value comes
		// from the client rather than the model because the model attribute is
		// computed and still unknown at create time.
		"organization_id":         orgID,
		"name":                    model.Name.ValueString(),
		"policy_type_name":        model.Type.ValueString(),
		"notes":                   model.Notes.ValueString(),
		"server_groups":           groups,
		"schedule_days":           days,
		"schedule_months":         months,
		"schedule_weeks_of_month": model.ScheduleWeeksOfMonth.ValueInt64(),
		"schedule_time":           model.ScheduleTime.ValueString(),
		"use_scheduled_timezone":  model.UseScheduledTimezone.ValueBool(),
	}

	if !model.ScheduledTimezone.IsNull() && !model.ScheduledTimezone.IsUnknown() {
		body["scheduled_timezone"] = model.ScheduledTimezone.ValueString()
	}

	cfg, cfgDiags := configurationToAPI(ctx, model.Configuration)
	diags.Append(cfgDiags...)
	if diags.HasError() {
		return nil, diags
	}
	body["configuration"] = cfg

	return body, diags
}

// resolveScheduleMask turns whichever schedule form the practitioner used into
// the integer Automox stores. ValidateConfig has already rejected setting both.
func resolveScheduleMask(
	ctx context.Context,
	encoded types.Int64,
	named types.Set,
	encode func([]string) (int64, error),
	field string,
) (int64, diag.Diagnostics) {
	var diags diag.Diagnostics

	if !named.IsNull() && !named.IsUnknown() {
		var names []string
		diags.Append(named.ElementsAs(ctx, &names, false)...)
		if diags.HasError() {
			return 0, diags
		}
		mask, err := encode(names)
		if err != nil {
			diags.AddError(
				"Invalid schedule",
				fmt.Sprintf("Could not encode %s: %s", field, err),
			)
			return 0, diags
		}
		return mask, diags
	}

	if !encoded.IsNull() && !encoded.IsUnknown() {
		return encoded.ValueInt64(), diags
	}

	// Neither form set: never runs on this axis.
	return 0, diags
}

// mergeConfiguration overlays the configuration this provider is writing onto
// the one Automox currently holds, so that keys the schema does not model
// survive an update.
//
// The schema models 22 configuration keys. The 26 policies in org 120547 set 36
// between them, and every one of those policies sets at least one key the schema
// does not know about — `secrets` among them, which is how a worklet binds a
// Shared Secret. The "Cleanup Disconnected Automox Devices" worklet exits with an
// error without its `apiKey` secret.
//
// Automox does not document whether PUT replaces the configuration object or
// merges into it, and the only way to find out by experiment is to issue the
// destructive call being guarded against. Merging is correct either way: under
// replacement the unmodeled keys are carried through explicitly, and under merge
// resending their current values is a no-op. The alternative — modelling the
// missing keys — would have to be repeated every time Automox adds one, and
// would be silently wrong in between.
//
// Silence is what makes this worth guarding. An unmodeled key cannot appear in a
// plan, because there is no attribute to diff, so dropping one would not show up
// as a change for anyone to review.
func mergeConfiguration(current, writing map[string]any) map[string]any {
	merged := make(map[string]any, len(current)+len(writing))
	for k, v := range current {
		merged[k] = v
	}
	for k, v := range writing {
		merged[k] = v
	}
	return merged
}

// configurationToAPI converts the configuration object into the request body,
// sending only what the practitioner set. Optional+Computed attributes come back
// from the API populated, so sending an unset one would write a value that was
// never configured.
func configurationToAPI(ctx context.Context, obj types.Object) (map[string]any, diag.Diagnostics) {
	var diags diag.Diagnostics
	out := map[string]any{}

	if obj.IsNull() || obj.IsUnknown() {
		return out, diags
	}

	for name, value := range obj.Attributes() {
		if value.IsNull() || value.IsUnknown() {
			continue
		}

		converted, convDiags := attrToGo(ctx, value)
		diags.Append(convDiags...)
		if diags.HasError() {
			return nil, diags
		}
		if name == "advanced_filter" {
			converted = normalizeAdvancedFilterConditions(converted)
		}
		out[name] = converted
	}

	return out, diags
}

// normalizeAdvancedFilterConditions translates the short-lived v0.1.3 `op`
// attribute to Automox's wire key. Keep this at the advanced-filter boundary:
// device filters also have a legitimate `op` field that must not be renamed.
func normalizeAdvancedFilterConditions(raw any) any {
	rules, ok := raw.([]any)
	if !ok {
		return raw
	}
	for _, rawRule := range rules {
		rule, ok := rawRule.(map[string]any)
		if !ok {
			continue
		}
		if _, present := rule["condition"]; !present {
			if legacy, present := rule["op"]; present {
				rule["condition"] = legacy
			}
		}
		delete(rule, "op")
	}
	return rules
}

func attrToGo(ctx context.Context, value attr.Value) (any, diag.Diagnostics) {
	var diags diag.Diagnostics

	switch v := value.(type) {
	case types.String:
		return v.ValueString(), diags
	case scriptStringValue:
		// Sent as configured. Automox does its own trimming.
		return v.ValueString(), diags
	case types.Bool:
		return v.ValueBool(), diags
	case types.Int64:
		return v.ValueInt64(), diags
	case types.List:
		return listToGo(ctx, v)
	case types.Object:
		nested := map[string]any{}
		for name, attrVal := range v.Attributes() {
			if attrVal.IsNull() || attrVal.IsUnknown() {
				continue
			}
			converted, d := attrToGo(ctx, attrVal)
			diags.Append(d...)
			if diags.HasError() {
				return nil, diags
			}
			nested[name] = converted
		}
		return nested, diags
	default:
		diags.AddError(
			"Unsupported configuration value",
			fmt.Sprintf("The provider does not know how to send a %T to Automox. This is a bug.", value),
		)
		return nil, diags
	}
}

func listToGo(ctx context.Context, list types.List) (any, diag.Diagnostics) {
	var diags diag.Diagnostics
	out := make([]any, 0, len(list.Elements()))

	for _, elem := range list.Elements() {
		converted, d := attrToGo(ctx, elem)
		diags.Append(d...)
		if diags.HasError() {
			return nil, diags
		}
		out = append(out, converted)
	}
	return out, diags
}

// flatten maps an API policy onto the Terraform model.
func flatten(ctx context.Context, api *apiPolicy, prior policyModel) (policyModel, diag.Diagnostics) {
	var diags diag.Diagnostics

	model := policyModel{
		ID:                   types.Int64Value(api.ID),
		Name:                 types.StringValue(api.Name),
		Type:                 types.StringValue(api.PolicyTypeName),
		Notes:                types.StringValue(api.Notes),
		OrganizationID:       types.Int64Value(api.OrganizationID),
		ScheduleTime:         types.StringValue(api.ScheduleTime),
		ScheduleDays:         types.Int64Value(api.ScheduleDays),
		ScheduleMonths:       types.Int64Value(api.ScheduleMonths),
		ScheduleWeeksOfMonth: types.Int64Value(api.ScheduleWeeksOfMonth),
	}

	// Every policy type observed live carries a UUID, required_software included.
	// The empty case is still handled rather than assumed away, so that a policy
	// returned without one becomes null instead of an empty string that would then
	// show as a diff against the computed attribute.
	if api.UUID != "" {
		model.UUID = types.StringValue(api.UUID)
	} else {
		model.UUID = types.StringNull()
	}

	// Automox returns an explicit JSON null for a policy targeting no groups, and
	// a nil slice becomes a null list rather than an empty one. server_groups is
	// Required, so a null made those policies unrepresentable: importing one
	// failed with "Must set a configuration value for the server_groups
	// attribute" and no configuration could satisfy it, because the value Read
	// produced was the one being rejected. Eleven of the 26 policies in org 120547
	// are in that state. Normalising to an empty list keeps the attribute Required
	// — a policy targeting nothing says so with `server_groups = []` rather than
	// by omission. automox_server_group.policies already does this.
	apiGroups := api.ServerGroups
	if apiGroups == nil {
		apiGroups = []int64{}
	}
	groups, groupDiags := types.ListValueFrom(ctx, types.Int64Type, apiGroups)
	diags.Append(groupDiags...)
	model.Groups = groups

	// The named schedule forms are echoed back only when the practitioner used
	// them, so the configuration and the state agree on which form is in play.
	model.ScheduleDaysOfWeek = mirrorNamedSchedule(ctx, prior.ScheduleDaysOfWeek, api.ScheduleDays,
		schedule.DecodeDays, &diags)
	model.ScheduleMonthsOfYear = mirrorNamedSchedule(ctx, prior.ScheduleMonthsOfYear, api.ScheduleMonths,
		schedule.DecodeMonths, &diags)

	// use_scheduled_timezone is absent from list responses on some policies.
	if api.UseScheduledTimezone != nil {
		model.UseScheduledTimezone = types.BoolValue(*api.UseScheduledTimezone)
	} else if cfgVal, ok := api.Configuration["use_scheduled_timezone"].(bool); ok {
		// The API also mirrors it inside configuration; prefer the top level but
		// fall back rather than leaving a Computed attribute unknown.
		model.UseScheduledTimezone = types.BoolValue(cfgVal)
	} else {
		model.UseScheduledTimezone = types.BoolValue(false)
	}

	if api.ScheduledTimezone != nil && *api.ScheduledTimezone != "" {
		model.ScheduledTimezone = types.StringValue(*api.ScheduledTimezone)
	} else {
		model.ScheduledTimezone = types.StringNull()
	}

	model.Status = optionalString(api.Status)
	model.CreateTime = optionalString(api.CreateTime)
	model.NextRemediation = optionalString(api.NextRemediation)
	if api.ServerCount != nil {
		model.ServerCount = types.Int64Value(*api.ServerCount)
	} else {
		model.ServerCount = types.Int64Value(0)
	}

	cfg, cfgDiags := configurationFromAPI(ctx, api.Configuration)
	diags.Append(cfgDiags...)
	cfg, cfgDiags = mirrorLegacyAdvancedFilterOp(cfg, prior.Configuration)
	diags.Append(cfgDiags...)
	model.Configuration = cfg

	// Recorded so a binding changed in the console is visible; see secretBindings.
	bindings, bindingDiags := types.MapValueFrom(ctx, types.StringType, secretBindings(api.Configuration))
	diags.Append(bindingDiags...)
	model.SecretBindings = bindings

	return model, diags
}

// mirrorNamedSchedule keeps the named form populated only when it was configured,
// so a practitioner using the encoded form does not acquire a second
// representation in state.
func mirrorNamedSchedule(
	ctx context.Context,
	prior types.Set,
	mask int64,
	decode func(int64) ([]string, error),
	diags *diag.Diagnostics,
) types.Set {
	if prior.IsNull() || prior.IsUnknown() {
		return types.SetNull(types.StringType)
	}

	names, err := decode(mask)
	if err != nil {
		diags.AddError(
			"Could not decode the schedule Automox returned",
			fmt.Sprintf("Automox returned the encoded schedule %d, which does not map to named "+
				"values: %s", mask, err),
		)
		return types.SetNull(types.StringType)
	}

	set, setDiags := types.SetValueFrom(ctx, types.StringType, names)
	diags.Append(setDiags...)
	return set
}

// configurationFromAPI builds the configuration object from the loose map the
// API returns, keeping only attributes the schema declares.
//
// Anything Automox adds that the schema does not know about is dropped rather
// than erroring: the API has already been observed returning fields absent from
// its own documentation, and a provider that refuses to read a policy because
// the vendor added an attribute would be worse than one that ignores it.
func configurationFromAPI(ctx context.Context, api map[string]any) (types.Object, diag.Diagnostics) {
	attrTypes := configurationAttrTypes()
	values := make(map[string]attr.Value, len(attrTypes))

	for name, typ := range attrTypes {
		raw, present := api[name]
		if !present || raw == nil {
			values[name] = nullOf(typ)
			continue
		}
		values[name] = goToAttr(ctx, typ, raw)
	}

	return types.ObjectValue(attrTypes, values)
}

// The deprecated advanced_filter `op` attribute exists only so state and
// configurations written against v0.1.3 stay readable. Automox never stores it
// (it returns op: null on every entry), so its value is the provider's choice,
// and that choice has to agree with what Terraform planned.
//
// It is Optional+Computed with UseStateForUnknown, and the framework's
// UseStateForUnknown copies the prior element's value without checking it
// exists: an entry appended to the list plans op as null, while an existing
// entry plans whatever state held. Mirroring condition into op on every read,
// as v0.1.4 to v0.1.8 did, therefore broke both an appended entry
// (PLA-464: "advanced_filter[6].op: was null, but now
// cty.StringVal("does-not-contain")") and a condition change on an existing
// one (op: was "is", but now "is-not").
//
// So op is decided per entry from what Terraform already holds for it:
//   - after a read, mirrorLegacyAdvancedFilterOp mirrors condition only into
//     entries whose prior op was set, which keeps a v0.1.3 configuration that
//     sets op in step with the live condition and leaves every other entry null;
//   - after an apply, keepPlannedAdvancedFilterOp returns exactly the planned op.

// mirrorLegacyAdvancedFilterOp sets op on each advanced_filter entry of cfg,
// freshly read from Automox, to that entry's condition when the same entry in
// prior state had a non-null op, and to null otherwise. An import has no prior
// entries, so it leaves op null throughout.
func mirrorLegacyAdvancedFilterOp(cfg, prior types.Object) (types.Object, diag.Diagnostics) {
	priorOps := advancedFilterOps(prior)
	return setAdvancedFilterOps(cfg, func(i int, rule map[string]attr.Value) attr.Value {
		if i < len(priorOps) && !priorOps[i].IsNull() && !priorOps[i].IsUnknown() {
			return rule["condition"]
		}
		return types.StringNull()
	})
}

// keepPlannedAdvancedFilterOp sets op on each advanced_filter entry of cfg,
// read back after an apply, to the op Terraform planned for that entry. An
// unknown plan (a create, where there is no state to carry forward) resolves to
// null. op is never sent to Automox, so the plan is the only source for it.
func keepPlannedAdvancedFilterOp(cfg, plan types.Object) (types.Object, diag.Diagnostics) {
	plannedOps := advancedFilterOps(plan)
	return setAdvancedFilterOps(cfg, func(i int, _ map[string]attr.Value) attr.Value {
		if i < len(plannedOps) && !plannedOps[i].IsUnknown() {
			return plannedOps[i]
		}
		return types.StringNull()
	})
}

// advancedFilterOps returns the op of each advanced_filter entry in cfg, in
// list order. A null or unknown configuration or list yields none.
func advancedFilterOps(cfg types.Object) []types.String {
	if cfg.IsNull() || cfg.IsUnknown() {
		return nil
	}
	list, ok := cfg.Attributes()["advanced_filter"].(types.List)
	if !ok || list.IsNull() || list.IsUnknown() {
		return nil
	}
	ops := make([]types.String, 0, len(list.Elements()))
	for _, elem := range list.Elements() {
		rule, ok := elem.(types.Object)
		if !ok || rule.IsNull() || rule.IsUnknown() {
			ops = append(ops, types.StringUnknown())
			continue
		}
		op, ok := rule.Attributes()["op"].(types.String)
		if !ok {
			op = types.StringUnknown()
		}
		ops = append(ops, op)
	}
	return ops
}

// setAdvancedFilterOps rebuilds cfg with op on each advanced_filter entry
// replaced by opFor(index, entry attributes).
func setAdvancedFilterOps(
	cfg types.Object, opFor func(int, map[string]attr.Value) attr.Value,
) (types.Object, diag.Diagnostics) {
	var diags diag.Diagnostics
	if cfg.IsNull() || cfg.IsUnknown() {
		return cfg, diags
	}
	attrs := cfg.Attributes()
	list, ok := attrs["advanced_filter"].(types.List)
	if !ok || list.IsNull() || list.IsUnknown() {
		return cfg, diags
	}
	ruleType, ok := list.ElementType(context.Background()).(types.ObjectType)
	if !ok {
		return cfg, diags
	}

	elems := make([]attr.Value, 0, len(list.Elements()))
	for i, elem := range list.Elements() {
		rule, ok := elem.(types.Object)
		if !ok || rule.IsNull() || rule.IsUnknown() {
			elems = append(elems, elem)
			continue
		}
		values := maps.Clone(rule.Attributes())
		values["op"] = opFor(i, values)
		updated, d := types.ObjectValue(ruleType.AttrTypes, values)
		diags.Append(d...)
		elems = append(elems, updated)
	}
	if diags.HasError() {
		return cfg, diags
	}

	newList, d := types.ListValue(ruleType, elems)
	diags.Append(d...)
	if diags.HasError() {
		return cfg, diags
	}
	newAttrs := maps.Clone(attrs)
	newAttrs["advanced_filter"] = newList
	out, d := types.ObjectValue(cfg.AttributeTypes(context.Background()), newAttrs)
	diags.Append(d...)
	if diags.HasError() {
		return cfg, diags
	}
	return out, diags
}

func optionalString(v *string) types.String {
	if v == nil || *v == "" {
		return types.StringNull()
	}
	return types.StringValue(*v)
}

// secretBindings reduces the API's `secrets` object to the name and ID of each
// Automox Shared Secret bound to a policy.
//
// The provider does not model `secrets` as configuration — it cannot create a
// binding — but it records this much so that a binding changed in the console is
// visible. An attribute absent from state cannot appear in a plan, so without
// this the change would be invisible rather than merely unmanaged.
//
// numPolicies and description are dropped on purpose. numPolicies counts how
// many policies share the secret, so binding that secret to an unrelated policy
// would otherwise report as drift here; description is free text that does not
// change what a worklet can do. The secret's value is never returned by the API.
func secretBindings(cfg map[string]any) map[string]string {
	out := map[string]string{}

	raw, ok := cfg["secrets"].(map[string]any)
	if !ok {
		return out
	}

	for name, entry := range raw {
		fields, ok := entry.(map[string]any)
		if !ok {
			// A shape the provider does not recognise is recorded by name with an
			// empty ID rather than dropped, so the binding still shows up.
			out[name] = ""
			continue
		}
		id, _ := fields["id"].(string)
		out[name] = id
	}

	return out
}

// warnSecretBindingDrift reports a binding that changed outside Terraform.
//
// This is a warning rather than a planned change because there is nothing to
// plan: the provider cannot write bindings, so it can report the difference but
// not reconcile it. Saying so is still worth doing — the alternative is that a
// worklet quietly loses the secret it needs and the next run fails with a
// missing-secret error that points nowhere near the cause.
func warnSecretBindingDrift(ctx context.Context, policy string, prior types.Map, current map[string]string, diags *diag.Diagnostics) {
	if prior.IsNull() || prior.IsUnknown() {
		// Nothing recorded yet — first read, or a policy imported before this
		// attribute existed. There is no baseline to compare against.
		return
	}

	var before map[string]string
	if d := prior.ElementsAs(ctx, &before, false); d.HasError() {
		return
	}

	var changes []string
	for name, id := range current {
		was, had := before[name]
		switch {
		case !had:
			changes = append(changes, fmt.Sprintf("%q was bound", name))
		case was != id:
			changes = append(changes, fmt.Sprintf("%q now points at a different secret", name))
		}
	}
	for name := range before {
		if _, still := current[name]; !still {
			changes = append(changes, fmt.Sprintf("%q was unbound", name))
		}
	}

	if len(changes) == 0 {
		return
	}

	sort.Strings(changes)
	diags.AddWarning(
		fmt.Sprintf("Shared Secret bindings changed outside Terraform on policy %q", policy),
		strings.Join(changes, "; ")+".\n\n"+
			"A worklet reads a secret by the name it is bound under, so this changes what "+
			"the policy can do. Terraform cannot reconcile it: Automox does not let a "+
			"binding be created through the API this provider uses, so the change has to be "+
			"made in the console. This warning exists because the alternative is silence.",
	)
}
