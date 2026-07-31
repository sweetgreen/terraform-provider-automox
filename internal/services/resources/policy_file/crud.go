package policy_file

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"

	"github.com/sweetgreen/terraform-provider-automox/internal/client"
)

func (r *policyFileResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan policyFileModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	policyID := plan.PolicyID.ValueInt64()
	filename := plan.Filename.ValueString()
	content := plan.Content.ValueString()

	var created apiPolicyFile
	err := r.client.Do(ctx, client.Request{
		Method:   "POST",
		Path:     fmt.Sprintf("/policies/%d/files", policyID),
		OrgScope: client.OrgScopeQuery,
		Upload: &client.Upload{
			// Automox requires this exact field name.
			FieldName: "file",
			Filename:  filename,
			Content:   []byte(content),
		},
	}, &created)
	if err != nil {
		resp.Diagnostics.AddError(
			"Could not upload the file to the Automox policy",
			fmt.Sprintf("Uploading %q to policy %d failed: %s\n\n"+
				"Automox rejects a filename already present on the policy. If this file "+
				"was uploaded outside Terraform, remove it in the console or import it "+
				"before applying.", filename, policyID, err),
		)
		return
	}

	if created.UUID == "" {
		resp.Diagnostics.AddError(
			"Uploaded the file but Automox returned no identifier",
			fmt.Sprintf("The upload of %q to policy %d was accepted but the response "+
				"carried no uuid, so Terraform cannot track the file. It exists and is "+
				"untracked; remove it in the console before retrying.", filename, policyID),
		)
		return
	}

	tflog.Debug(ctx, "uploaded a file to an Automox policy", map[string]any{
		"policy_id": policyID, "uuid": created.UUID, "filename": created.Filename,
	})

	plan.UUID = types.StringValue(created.UUID)
	plan.Size = types.Int64Value(created.Size)
	plan.SHA256 = types.StringValue(sha256Of(content))

	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *policyFileResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state policyFileModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	policyID := state.PolicyID.ValueInt64()
	uuid := state.UUID.ValueString()

	// Absence is confirmed against the listing rather than a direct read.
	// GET /policies/{id}/files/{uuid} answers 500 for a uuid that does not exist,
	// which is indistinguishable from the service being broken -- dropping a
	// resource from state on that basis would be guessing.
	files, err := r.list(ctx, policyID)
	if err != nil {
		resp.Diagnostics.AddError(
			"Could not read the files on the Automox policy",
			fmt.Sprintf("Listing files for policy %d failed: %s", policyID, err),
		)
		return
	}

	for _, f := range files {
		if f.UUID != uuid {
			continue
		}
		// The contents cannot be fetched, so content and content_sha256 keep what
		// was uploaded. A file edited in the console reads as unchanged here; that
		// limitation is stated in the schema rather than papered over.
		state.Filename = types.StringValue(f.Filename)
		state.Size = types.Int64Value(f.Size)
		resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
		return
	}

	tflog.Debug(ctx, "the file is no longer on the Automox policy; removing it from state",
		map[string]any{"policy_id": policyID, "uuid": uuid})
	resp.State.RemoveResource(ctx)
}

// Update cannot be reached: every attribute in the schema forces replacement.
// It exists because the framework requires the method, and it fails loudly
// rather than silently doing nothing, which would report success while leaving
// the file as it was.
func (r *policyFileResource) Update(_ context.Context, _ resource.UpdateRequest, resp *resource.UpdateResponse) {
	resp.Diagnostics.AddError(
		"Automox policy files cannot be updated in place",
		"Every attribute of automox_policy_file forces replacement, so this should be "+
			"unreachable. Reaching it means a plan modifier was removed. Report this as "+
			"a bug in the provider.",
	)
}

func (r *policyFileResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state policyFileModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	policyID := state.PolicyID.ValueInt64()
	uuid := state.UUID.ValueString()

	err := r.client.Do(ctx, client.Request{
		Method:   "DELETE",
		Path:     fmt.Sprintf("/policies/%d/files/%s", policyID, uuid),
		OrgScope: client.OrgScopeQuery,
	}, nil)
	if err == nil {
		tflog.Debug(ctx, "deleted a file from an Automox policy",
			map[string]any{"policy_id": policyID, "uuid": uuid})
		return
	}

	// Deleting something already gone should succeed. The direct endpoint cannot
	// be trusted to say so -- an unknown uuid is a 500 -- so the listing decides.
	files, listErr := r.list(ctx, policyID)
	if listErr == nil {
		present := false
		for _, f := range files {
			if f.UUID == uuid {
				present = true
				break
			}
		}
		if !present {
			return
		}
	}

	resp.Diagnostics.AddError(
		"Could not delete the file from the Automox policy",
		fmt.Sprintf("Deleting %s from policy %d failed: %s", uuid, policyID, err),
	)
}

func (r *policyFileResource) list(ctx context.Context, policyID int64) ([]apiPolicyFile, error) {
	var files []apiPolicyFile
	if err := r.client.List(ctx, client.ListOptions{
		Path:     fmt.Sprintf("/policies/%d/files", policyID),
		OrgScope: client.OrgScopeQuery,
		Envelope: client.EnvelopeArray,
	}, &files); err != nil {
		return nil, err
	}
	return files, nil
}
