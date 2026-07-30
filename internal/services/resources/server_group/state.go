package server_group

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var hexColour = regexp.MustCompile(`^#[0-9A-Fa-f]{6}$`)

func parseImportID(raw string) (int64, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return 0, fmt.Errorf("the ID is empty")
	}
	id, err := strconv.ParseInt(trimmed, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("not a number")
	}
	if id < 1 {
		return 0, fmt.Errorf("server group IDs are positive")
	}
	return id, nil
}

// construct builds the create/update request body.
//
// The API treats PUT as a full replacement, so every managed field is sent on
// every write; omitting one would clear it rather than leave it alone.
func construct(ctx context.Context, model serverGroupModel) (map[string]any, diag.Diagnostics) {
	var diags diag.Diagnostics

	body := map[string]any{
		"name":                   model.Name.ValueString(),
		"refresh_interval":       model.RefreshInterval.ValueInt64(),
		"parent_server_group_id": model.ParentServerGroupID.ValueInt64(),
	}

	// Null is meaningful for the tri-state fields, so they are sent only when
	// set. Sending an explicit JSON null would ask Automox to clear the setting,
	// which is a different intent from "leave it alone".
	if !model.UIColor.IsNull() && !model.UIColor.IsUnknown() {
		body["ui_color"] = model.UIColor.ValueString()
	}
	if !model.Notes.IsNull() && !model.Notes.IsUnknown() {
		body["notes"] = model.Notes.ValueString()
	}
	if !model.EnableOSAutoUpdate.IsNull() && !model.EnableOSAutoUpdate.IsUnknown() {
		body["enable_os_auto_update"] = model.EnableOSAutoUpdate.ValueBool()
	}
	if !model.EnableWSUS.IsNull() && !model.EnableWSUS.IsUnknown() {
		body["enable_wsus"] = model.EnableWSUS.ValueBool()
	}
	if !model.WSUSServer.IsNull() && !model.WSUSServer.IsUnknown() {
		body["wsus_server"] = model.WSUSServer.ValueString()
	}

	if !model.Policies.IsNull() && !model.Policies.IsUnknown() {
		var policies []int64
		diags.Append(model.Policies.ElementsAs(ctx, &policies, false)...)
		if diags.HasError() {
			return nil, diags
		}
		body["policies"] = policies
	}

	return body, diags
}

// flatten maps an API response onto the Terraform model.
//
// `prior` is the configured or previously-stored model. It is needed because
// Automox normalises two absent values into present-but-empty ones: an omitted
// `notes` reads back as "" and omitted `policies` as []. Writing those into
// state when the configuration left them null would produce "Provider produced
// inconsistent result after apply", and on refresh a permanent diff. Where the
// API value is the empty form and the practitioner wrote nothing, the null is
// preserved.
func flatten(ctx context.Context, api *apiServerGroup, prior serverGroupModel) (serverGroupModel, diag.Diagnostics) {
	var diags diag.Diagnostics

	model := serverGroupModel{
		ID:                  types.Int64Value(api.ID),
		Name:                types.StringValue(api.Name),
		RefreshInterval:     types.Int64Value(api.RefreshInterval),
		ParentServerGroupID: types.Int64Value(api.ParentServerGroupID),
		OrganizationID:      types.Int64Value(api.OrganizationID),
	}

	if api.UUID != "" {
		model.UUID = types.StringValue(api.UUID)
	} else {
		// Absent from create responses; a subsequent read fills it in.
		model.UUID = types.StringNull()
	}

	if api.ServerCount != nil {
		model.ServerCount = types.Int64Value(*api.ServerCount)
	} else {
		model.ServerCount = types.Int64Value(0)
	}

	model.UIColor = optionalString(api.UIColor, prior.UIColor)
	model.Notes = optionalString(api.Notes, prior.Notes)
	model.EnableOSAutoUpdate = optionalBool(api.EnableOSAutoUpdate, prior.EnableOSAutoUpdate)
	model.EnableWSUS = optionalBool(api.enableWSUS(), prior.EnableWSUS)
	model.WSUSServer = optionalString(api.wsusServer(), prior.WSUSServer)

	policies, policyDiags := policiesFromAPI(ctx, api.Policies)
	diags.Append(policyDiags...)
	model.Policies = policies

	return model, diags
}

// optionalString keeps null when the API returned nothing meaningful and the
// practitioner configured nothing. An empty string the practitioner actually
// wrote is preserved as an empty string.
func optionalString(apiValue *string, prior types.String) types.String {
	if apiValue == nil {
		return types.StringNull()
	}
	if *apiValue == "" && prior.IsNull() {
		return types.StringNull()
	}
	return types.StringValue(*apiValue)
}

func optionalBool(apiValue *bool, prior types.Bool) types.Bool {
	if apiValue == nil {
		return types.BoolNull()
	}
	// A tri-state field genuinely carries false, so unlike the string case there
	// is no "empty means absent" collapse. prior is accepted for symmetry and to
	// keep the call sites uniform.
	_ = prior
	return types.BoolValue(*apiValue)
}

// policiesFromAPI always reflects what Automox reports.
//
// Unlike notes, this attribute is Optional AND Computed, because the membership
// is also written from the policy side: creating an automox_policy that targets
// this group makes Automox add the policy here. Preserving a configured null
// would make the group perpetually plan to detach policies it never attached,
// and the two resources would fight on every apply.
func policiesFromAPI(ctx context.Context, apiValue []int64) (types.List, diag.Diagnostics) {
	if apiValue == nil {
		apiValue = []int64{}
	}
	return types.ListValueFrom(ctx, types.Int64Type, apiValue)
}
