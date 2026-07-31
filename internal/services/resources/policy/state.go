package policy

import (
	"context"
	"fmt"

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
		out[name] = converted
	}

	return out, diags
}

func attrToGo(ctx context.Context, value attr.Value) (any, diag.Diagnostics) {
	var diags diag.Diagnostics

	switch v := value.(type) {
	case types.String:
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

	groups, groupDiags := types.ListValueFrom(ctx, types.Int64Type, api.ServerGroups)
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
	model.Configuration = cfg

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

func optionalString(v *string) types.String {
	if v == nil || *v == "" {
		return types.StringNull()
	}
	return types.StringValue(*v)
}
