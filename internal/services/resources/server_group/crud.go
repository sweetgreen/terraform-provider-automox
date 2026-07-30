package server_group

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-log/tflog"

	"github.com/sweetgreen/terraform-provider-automox/internal/client"
)

func (r *serverGroupResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan serverGroupModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	body, diags := construct(ctx, plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	var created apiServerGroup
	if err := r.client.Do(ctx, client.Request{
		Method:   "POST",
		Path:     "/servergroups",
		OrgScope: client.OrgScopeQuery,
		Body:     body,
	}, &created); err != nil {
		resp.Diagnostics.AddError("Could not create the Automox server group", err.Error())
		return
	}

	if created.ID == 0 {
		resp.Diagnostics.AddError(
			"Automox created the server group but returned no ID",
			"The group may exist without being tracked by Terraform. Check the Automox console for "+
				"a group named "+plan.Name.ValueString()+" and import it, or delete it before retrying.",
		)
		return
	}

	// The create response is partial: it carries the ID but not uuid,
	// server_count, or a populated wsus_config. Reading back is what makes the
	// state complete, and it also confirms how Automox stored what was sent.
	fetched, err := r.read(ctx, created.ID)
	if err != nil {
		resp.Diagnostics.AddError(
			"Created the server group but could not read it back",
			fmt.Sprintf("Server group %d exists in Automox but its full state could not be "+
				"retrieved: %s\n\nRun `terraform refresh` or import it with "+
				"`terraform import automox_server_group.<name> %d`.", created.ID, err, created.ID),
		)
		return
	}

	state, diags := flatten(ctx, fetched, plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	tflog.Debug(ctx, "created an Automox server group", map[string]any{
		"id": fetched.ID, "uuid": fetched.UUID, "name": fetched.Name,
	})

	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (r *serverGroupResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state serverGroupModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	id := state.ID.ValueInt64()
	fetched, err := r.read(ctx, id)
	if err != nil {
		// Automox reports an absent server group as 403, not 404 — the same status
		// it uses for a credential that has lost access. IsGone disambiguates
		// against the group listing rather than guessing, because guessing either
		// way is harmful: reading a permissions failure as deletion would make
		// Terraform plan to recreate a live group, and reading a deletion as a
		// failure would break every plan after a normal destroy.
		gone, goneErr := client.IsGone(ctx, err, client.GoneByForbidden, r.exists(id))
		if gone {
			tflog.Debug(ctx, "the Automox server group no longer exists; removing it from state",
				map[string]any{"id": id})
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Could not read the Automox server group", goneErr.Error())
		return
	}

	refreshed, diags := flatten(ctx, fetched, state)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, refreshed)...)
}

func (r *serverGroupResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan serverGroupModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var state serverGroupModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	id := state.ID.ValueInt64()

	body, diags := construct(ctx, plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	// PUT returns 204 with no body, so the response cannot be used to build
	// state; the read below is not optional.
	if err := r.client.Do(ctx, client.Request{
		Method:   "PUT",
		Path:     fmt.Sprintf("/servergroups/%d", id),
		OrgScope: client.OrgScopeQuery,
		Body:     body,
	}, nil); err != nil {
		resp.Diagnostics.AddError("Could not update the Automox server group", err.Error())
		return
	}

	fetched, err := r.read(ctx, id)
	if err != nil {
		resp.Diagnostics.AddError(
			"Updated the server group but could not read it back",
			fmt.Sprintf("The update was accepted, so Automox and Terraform state may now differ: %s\n\n"+
				"Run `terraform refresh` to reconcile.", err),
		)
		return
	}

	updated, diags := flatten(ctx, fetched, plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, updated)...)
}

func (r *serverGroupResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state serverGroupModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	id := state.ID.ValueInt64()

	err := r.client.Do(ctx, client.Request{
		Method:   "DELETE",
		Path:     fmt.Sprintf("/servergroups/%d", id),
		OrgScope: client.OrgScopeQuery,
	}, nil)
	if err == nil {
		tflog.Debug(ctx, "deleted an Automox server group", map[string]any{"id": id})
		return
	}

	// Already gone is a successful delete. Anything else must surface, or a
	// failed destroy would look like a clean one and leave the group behind.
	gone, goneErr := client.IsGone(ctx, err, client.GoneByForbidden, r.exists(id))
	if gone {
		return
	}
	resp.Diagnostics.AddError(
		"Could not delete the Automox server group",
		goneErr.Error()+"\n\nNote that deleting a group moves its devices to the organization's "+
			"default group; it does not remove them from Automox.",
	)
}

// read fetches one group.
func (r *serverGroupResource) read(ctx context.Context, id int64) (*apiServerGroup, error) {
	var group apiServerGroup
	if err := r.client.Do(ctx, client.Request{
		Method:   "GET",
		Path:     fmt.Sprintf("/servergroups/%d", id),
		OrgScope: client.OrgScopeQuery,
	}, &group); err != nil {
		return nil, err
	}
	return &group, nil
}

// exists reports whether a group id appears in the collection listing, which is
// how a 403 is resolved into either "deleted" or "no longer permitted". The
// listing is authorized separately from the individual read, so if it succeeds
// the credential is working and an absent id genuinely means gone.
func (r *serverGroupResource) exists(id int64) client.ExistsFunc {
	return func(ctx context.Context) (bool, error) {
		var groups []apiServerGroup
		if err := r.client.List(ctx, client.ListOptions{
			Path:     "/servergroups",
			OrgScope: client.OrgScopeQuery,
			Envelope: client.EnvelopeArray,
		}, &groups); err != nil {
			return false, err
		}
		for _, g := range groups {
			if g.ID == id {
				return true, nil
			}
		}
		return false, nil
	}
}
