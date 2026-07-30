package scheduled_window

import (
	"context"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"

	"github.com/sweetgreen/terraform-provider-automox/internal/client"
)

func (r *windowResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan windowModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	orgUUID, err := r.client.DefaultOrganizationUUID(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Could not resolve the Automox organization UUID", err.Error())
		return
	}

	body, diags := construct(ctx, plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Unlike policies and server groups, this endpoint returns the complete
	// object on create, so no read-back is needed.
	var created apiWindow
	if err := r.client.Do(ctx, client.Request{
		Method:   "POST",
		Path:     fmt.Sprintf("/policy-windows/org/%s", orgUUID),
		OrgScope: client.OrgScopeNone,
		Body:     body,
	}, &created); err != nil {
		resp.Diagnostics.AddError("Could not create the Automox scheduled window", err.Error())
		return
	}

	if created.WindowUUID == "" {
		resp.Diagnostics.AddError(
			"Automox created the window but returned no UUID",
			"The window may exist without being tracked by Terraform. Check the Automox console "+
				"for a window named "+plan.Name.ValueString()+".",
		)
		return
	}

	state, diags := flatten(ctx, &created)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	tflog.Debug(ctx, "created an Automox scheduled window", map[string]any{
		"window_uuid": created.WindowUUID, "name": created.Name,
	})

	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (r *windowResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state windowModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	orgUUID, err := r.client.DefaultOrganizationUUID(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Could not resolve the Automox organization UUID", err.Error())
		return
	}

	uuid := state.WindowUUID.ValueString()
	fetched, err := r.read(ctx, orgUUID, uuid)
	if err != nil {
		// This endpoint reports an absent window with a clean 404, so no listing
		// lookup is needed to tell deletion from lost access.
		gone, goneErr := client.IsGone(ctx, err, client.GoneByNotFound, nil)
		if gone {
			tflog.Debug(ctx, "the Automox scheduled window no longer exists; removing it from state",
				map[string]any{"window_uuid": uuid})
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Could not read the Automox scheduled window", goneErr.Error())
		return
	}

	refreshed, diags := flatten(ctx, fetched)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, refreshed)...)
}

func (r *windowResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan windowModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var state windowModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	orgUUID, err := r.client.DefaultOrganizationUUID(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Could not resolve the Automox organization UUID", err.Error())
		return
	}

	uuid := state.WindowUUID.ValueString()

	body, diags := construct(ctx, plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	// The API expects the UUID in the body as well as the path.
	body["window_uuid"] = uuid

	// PUT returns the updated object here, unlike the 204 that policies and
	// server groups return, so state is built from the response directly.
	var updated apiWindow
	if err := r.client.Do(ctx, client.Request{
		Method:   "PUT",
		Path:     fmt.Sprintf("/policy-windows/org/%s/window/%s", orgUUID, uuid),
		OrgScope: client.OrgScopeNone,
		Body:     body,
	}, &updated); err != nil {
		resp.Diagnostics.AddError("Could not update the Automox scheduled window", err.Error())
		return
	}

	newState, diags := flatten(ctx, &updated)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, newState)...)
}

func (r *windowResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state windowModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	orgUUID, err := r.client.DefaultOrganizationUUID(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Could not resolve the Automox organization UUID", err.Error())
		return
	}

	uuid := state.WindowUUID.ValueString()
	err = r.client.Do(ctx, client.Request{
		Method:   "DELETE",
		Path:     fmt.Sprintf("/policy-windows/org/%s/window/%s", orgUUID, uuid),
		OrgScope: client.OrgScopeNone,
	}, nil)
	if err == nil {
		tflog.Debug(ctx, "deleted an Automox scheduled window", map[string]any{"window_uuid": uuid})
		return
	}

	gone, goneErr := client.IsGone(ctx, err, client.GoneByNotFound, nil)
	if gone {
		return
	}
	resp.Diagnostics.AddError("Could not delete the Automox scheduled window", goneErr.Error())
}

func (r *windowResource) read(ctx context.Context, orgUUID, windowUUID string) (*apiWindow, error) {
	var w apiWindow
	if err := r.client.Do(ctx, client.Request{
		Method:   "GET",
		Path:     fmt.Sprintf("/policy-windows/org/%s/window/%s", orgUUID, windowUUID),
		OrgScope: client.OrgScopeNone,
	}, &w); err != nil {
		return nil, err
	}
	return &w, nil
}

// construct builds the create/update body.
func construct(ctx context.Context, model windowModel) (map[string]any, diag.Diagnostics) {
	var diags diag.Diagnostics

	windowType := model.WindowType.ValueString()
	if windowType == "" {
		windowType = WindowTypeExclude
	}

	body := map[string]any{
		"window_name":        model.Name.ValueString(),
		"window_description": model.Description.ValueString(),
		"window_type":        windowType,
		// Automox uppercases this itself; sending it uppercased keeps the request
		// and the stored value identical so no diff appears on the next read.
		"recurrence": strings.ToUpper(model.Recurrence.ValueString()),
		"dtstart":    model.DTStart.ValueString(),
		"rrule":      model.RRule.ValueString(),
	}

	// Only sent for RECURRING windows; ValidateConfig has already rejected it on
	// a ONCE window, where the API derives the duration instead.
	if !model.DurationMinutes.IsNull() && !model.DurationMinutes.IsUnknown() {
		body["duration_minutes"] = model.DurationMinutes.ValueInt64()
	}

	if !model.Status.IsNull() && !model.Status.IsUnknown() {
		body["status"] = model.Status.ValueString()
	}

	groups := []string{}
	if !model.GroupUUIDs.IsNull() && !model.GroupUUIDs.IsUnknown() {
		diags.Append(model.GroupUUIDs.ElementsAs(ctx, &groups, false)...)
		if diags.HasError() {
			return nil, diags
		}
	}
	body["group_uuids"] = groups

	return body, diags
}

// flatten maps the API response onto the Terraform model.
//
// No prior model is needed. Unlike server groups, this endpoint echoes back
// exactly what was written, with no absent-becomes-empty normalisation to undo.
func flatten(ctx context.Context, api *apiWindow) (windowModel, diag.Diagnostics) {
	var diags diag.Diagnostics

	model := windowModel{
		WindowUUID:  types.StringValue(api.WindowUUID),
		Name:        types.StringValue(api.Name),
		Description: types.StringValue(api.Description),
		WindowType:  types.StringValue(api.WindowType),
		Recurrence:  types.StringValue(strings.ToUpper(api.Recurrence)),
		DTStart:     types.StringValue(api.DTStart),
		RRule:       types.StringValue(api.RRule),
		Status:      types.StringValue(api.Status),
		OrgUUID:     types.StringValue(api.OrgUUID),
		CreatedAt:   types.StringValue(api.CreatedAt),
		UpdatedAt:   types.StringValue(api.UpdatedAt),
	}

	// Null on a ONCE window, where Automox derives the duration rather than
	// storing one.
	if api.DurationMinutes != nil {
		model.DurationMinutes = types.Int64Value(*api.DurationMinutes)
	} else {
		model.DurationMinutes = types.Int64Null()
	}

	groups := api.GroupUUIDs
	if groups == nil {
		groups = []string{}
	}
	set, setDiags := types.SetValueFrom(ctx, types.StringType, groups)
	diags.Append(setDiags...)
	model.GroupUUIDs = set

	return model, diags
}
