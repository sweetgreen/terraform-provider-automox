package policy

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-log/tflog"

	"github.com/sweetgreen/terraform-provider-automox/internal/client"
)

func (r *policyResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan policyModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	name := plan.Name.ValueString()

	// Automox returns 201 with an empty object and no Location header, so the new
	// policy's ID has to be recovered by name. Checking for a pre-existing policy
	// of the same name first means a collision is reported before anything is
	// created, rather than leaving an untracked duplicate behind.
	existing, err := r.findByName(ctx, name)
	if err != nil {
		resp.Diagnostics.AddError("Could not check for an existing Automox policy", err.Error())
		return
	}
	if len(existing) > 0 {
		resp.Diagnostics.AddError(
			"An Automox policy with this name already exists",
			fmt.Sprintf("Policy %d is already named %q.\n\n"+
				"Automox returns no identifier when a policy is created, so the provider locates "+
				"the new policy by name and cannot tell two policies with the same name apart. "+
				"Either choose a different name, or import the existing policy with "+
				"`terraform import automox_policy.<name> %d`.", existing[0].ID, name, existing[0].ID),
		)
		return
	}

	body, diags := construct(ctx, plan, r.client.OrganizationID())
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := r.client.Do(ctx, client.Request{
		Method:   "POST",
		Path:     "/policies",
		OrgScope: client.OrgScopeQuery,
		Body:     body,
	}, nil); err != nil {
		resp.Diagnostics.AddError("Could not create the Automox policy", err.Error())
		return
	}

	// Now find what was just created. A name that matches nothing, or more than
	// one policy, is a hard error: adopting an arbitrary match would put a policy
	// under Terraform's control that it did not create, and policies decide when
	// code runs on endpoints.
	matches, err := r.findByName(ctx, name)
	if err != nil {
		resp.Diagnostics.AddError(
			"Created the policy but could not locate it",
			fmt.Sprintf("Automox accepted the policy %q but listing policies to find its ID "+
				"failed: %s\n\nThe policy exists and is not tracked by Terraform. Find it in the "+
				"console and import it, or delete it before retrying.", name, err),
		)
		return
	}

	switch len(matches) {
	case 1:
		// The expected path.
	case 0:
		resp.Diagnostics.AddError(
			"Created the policy but it does not appear in the policy list",
			fmt.Sprintf("Automox accepted the creation of %q but no policy with that name was "+
				"found afterwards. It may have been created in a different organization, or "+
				"renamed concurrently.", name),
		)
		return
	default:
		ids := make([]int64, 0, len(matches))
		for _, m := range matches {
			ids = append(ids, m.ID)
		}
		resp.Diagnostics.AddError(
			"Several Automox policies now share this name",
			fmt.Sprintf("After creating %q, %d policies have that name: %v.\n\n"+
				"The provider cannot tell which one it just created, and will not guess. Remove "+
				"the duplicates in the Automox console, then import the one to keep.",
				name, len(matches), ids),
		)
		return
	}

	created := &matches[0]
	state, diags := flatten(ctx, created, plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	tflog.Debug(ctx, "created an Automox policy", map[string]any{
		"id": created.ID, "name": created.Name, "type": created.PolicyTypeName,
	})

	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (r *policyResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state policyModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	id := state.ID.ValueInt64()
	fetched, err := r.read(ctx, id)
	if err != nil {
		// Unlike server groups, /policies reports an absent policy with a clean
		// 404, so no listing lookup is needed to disambiguate.
		gone, goneErr := client.IsGone(ctx, err, client.GoneByNotFound, nil)
		if gone {
			tflog.Debug(ctx, "the Automox policy no longer exists; removing it from state",
				map[string]any{"id": id})
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Could not read the Automox policy", goneErr.Error())
		return
	}

	refreshed, diags := flatten(ctx, fetched, state)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, refreshed)...)
}

func (r *policyResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan policyModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var state policyModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	id := state.ID.ValueInt64()

	body, diags := construct(ctx, plan, r.client.OrganizationID())
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	// The ID goes in the body as well as the path, and it is load-bearing rather
	// than redundant: Automox re-checks name uniqueness on update, and `id` is what
	// tells it to exclude this policy from that check. Without it, any update that
	// keeps the policy's current name -- that is, any change which is not also a
	// rename -- is rejected with "A policy with this name already exists".
	// Verified live on both patch and worklet policies. Covered by the
	// rename-free update step in TestAccPolicy_WorkletLifecycle.
	body["id"] = id

	// Carry through configuration keys the schema does not model, which would
	// otherwise be dropped by this write. See mergeConfiguration for why this is
	// necessary and why it is correct whichever way Automox treats PUT.
	//
	// The read is deliberately fatal rather than best-effort. Continuing without
	// it would send a configuration known to be missing keys, and the loss would
	// be invisible: an unmodeled key has no attribute to appear in a plan. A
	// failed update that says so is recoverable; a successful one that quietly
	// strips a worklet's secrets is not.
	existing, err := r.read(ctx, id)
	if err != nil {
		resp.Diagnostics.AddError(
			"Could not read the Automox policy before updating it",
			fmt.Sprintf("Reading policy %d failed, and the update was abandoned rather than "+
				"risk dropping configuration this provider does not model: %s", id, err),
		)
		return
	}
	if writing, ok := body["configuration"].(map[string]any); ok {
		body["configuration"] = mergeConfiguration(existing.Configuration, writing)
	}

	if err := r.client.Do(ctx, client.Request{
		Method:   "PUT",
		Path:     fmt.Sprintf("/policies/%d", id),
		OrgScope: client.OrgScopeQuery,
		Body:     body,
	}, nil); err != nil {
		resp.Diagnostics.AddError("Could not update the Automox policy", err.Error())
		return
	}

	fetched, err := r.read(ctx, id)
	if err != nil {
		resp.Diagnostics.AddError(
			"Updated the policy but could not read it back",
			fmt.Sprintf("The update was accepted, so Automox and Terraform state may now "+
				"differ: %s\n\nRun `terraform refresh` to reconcile.", err),
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

func (r *policyResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state policyModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	id := state.ID.ValueInt64()
	err := r.client.Do(ctx, client.Request{
		Method:   "DELETE",
		Path:     fmt.Sprintf("/policies/%d", id),
		OrgScope: client.OrgScopeQuery,
	}, nil)
	if err == nil {
		tflog.Debug(ctx, "deleted an Automox policy", map[string]any{"id": id})
		return
	}

	gone, goneErr := client.IsGone(ctx, err, client.GoneByNotFound, nil)
	if gone {
		return
	}
	resp.Diagnostics.AddError("Could not delete the Automox policy", goneErr.Error())
}

func (r *policyResource) read(ctx context.Context, id int64) (*apiPolicy, error) {
	var p apiPolicy
	if err := r.client.Do(ctx, client.Request{
		Method:   "GET",
		Path:     fmt.Sprintf("/policies/%d", id),
		OrgScope: client.OrgScopeQuery,
	}, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

// findByName returns every policy with the given name.
//
// It returns all matches rather than the first, so callers can refuse to act on
// an ambiguous result instead of silently adopting one.
func (r *policyResource) findByName(ctx context.Context, name string) ([]apiPolicy, error) {
	var policies []apiPolicy
	if err := r.client.List(ctx, client.ListOptions{
		Path:     "/policies",
		OrgScope: client.OrgScopeQuery,
		Envelope: client.EnvelopeArray,
	}, &policies); err != nil {
		return nil, err
	}

	var matches []apiPolicy
	for _, p := range policies {
		if p.Name == name {
			matches = append(matches, p)
		}
	}
	return matches, nil
}
