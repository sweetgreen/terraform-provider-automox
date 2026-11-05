package device_group_assignment

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-log/tflog"

	"github.com/sweetgreen/terraform-provider-automox/internal/client"
)

func (r *deviceGroupAssignmentResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan deviceGroupAssignmentModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	deviceID := plan.DeviceID.ValueInt64()
	targetGroupID := plan.ServerGroupID.ValueInt64()

	fetched, err := r.assign(ctx, deviceID, targetGroupID)
	if err != nil {
		resp.Diagnostics.AddError(
			"Could not place the Automox device in the server group",
			fmt.Sprintf("Moving device %d to server group %d failed: %s", deviceID, targetGroupID, err),
		)
		return
	}

	tflog.Debug(ctx, "placed an Automox device in a server group", map[string]any{
		"device_id": fetched.ID, "server_group_id": fetched.ServerGroupID,
	})

	resp.Diagnostics.Append(resp.State.Set(ctx, flatten(*fetched, r.client.OrganizationID()))...)
}

func (r *deviceGroupAssignmentResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state deviceGroupAssignmentModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	deviceID := state.DeviceID.ValueInt64()

	fetched, err := r.read(ctx, deviceID)
	if err != nil {
		// Unlike /servergroups, Automox reports an absent device with a clean 404,
		// so no listing fallback is needed to disambiguate it from a permissions
		// failure.
		gone, goneErr := client.IsGone(ctx, err, client.GoneByNotFound, nil)
		if gone {
			tflog.Debug(ctx, "the Automox device no longer exists; removing the assignment from state",
				map[string]any{"device_id": deviceID})
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Could not read the Automox device", goneErr.Error())
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, flatten(*fetched, r.client.OrganizationID()))...)
}

func (r *deviceGroupAssignmentResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan deviceGroupAssignmentModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	deviceID := plan.DeviceID.ValueInt64()
	targetGroupID := plan.ServerGroupID.ValueInt64()

	fetched, err := r.assign(ctx, deviceID, targetGroupID)
	if err != nil {
		resp.Diagnostics.AddError(
			"Could not move the Automox device to the server group",
			fmt.Sprintf("Moving device %d to server group %d failed: %s", deviceID, targetGroupID, err),
		)
		return
	}

	tflog.Debug(ctx, "moved an Automox device to a different server group", map[string]any{
		"device_id": fetched.ID, "server_group_id": fetched.ServerGroupID,
	})

	resp.Diagnostics.Append(resp.State.Set(ctx, flatten(*fetched, r.client.OrganizationID()))...)
}

// Delete removes the assignment from Terraform state without calling the
// Automox API at all.
//
// Every other resource's Delete undoes what Create did. This one cannot: the
// device existed before Terraform touched it and keeps existing afterward, and
// "undoing" a group placement has no well-defined target. Moving it to the
// organization's default group would mean guessing that the default group is
// the right destination, and it is exactly the unpatched, policy-less group
// this resource exists to move devices out of. Leaving the device in whatever
// group it was last placed into is the only option that cannot turn a
// `terraform destroy` into a fleet-wide loss of patching.
func (r *deviceGroupAssignmentResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state deviceGroupAssignmentModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	tflog.Debug(ctx, "removing an automox_device_group_assignment from state without moving the device",
		map[string]any{
			"device_id":       state.DeviceID.ValueInt64(),
			"server_group_id": state.ServerGroupID.ValueInt64(),
		})
}

// read fetches one device.
func (r *deviceGroupAssignmentResource) read(ctx context.Context, deviceID int64) (*apiDevice, error) {
	var dev apiDevice
	if err := r.client.Do(ctx, client.Request{
		Method:   "GET",
		Path:     fmt.Sprintf("/servers/%d", deviceID),
		OrgScope: client.OrgScopeQuery,
	}, &dev); err != nil {
		return nil, err
	}
	return &dev, nil
}

// assign moves a device to targetGroupID and returns the device as Automox
// has it afterward.
//
// The update endpoint is a full replacement, so every writable field is sent
// on every call, not just server_group_id. The device is read immediately
// before the write so that exception, tags, custom_name, and ip_addrs --
// none of which this resource owns -- travel through unchanged. The response
// to the write itself is empty (204), so the read after the write is what
// builds the state that gets stored, and it also confirms Automox actually
// applied the move rather than trusting the request that asked for it.
func (r *deviceGroupAssignmentResource) assign(ctx context.Context, deviceID, targetGroupID int64) (*apiDevice, error) {
	current, err := r.read(ctx, deviceID)
	if err != nil {
		return nil, fmt.Errorf("reading the device before moving it: %w", err)
	}

	if err := r.client.Do(ctx, client.Request{
		Method:   "PUT",
		Path:     fmt.Sprintf("/servers/%d", deviceID),
		OrgScope: client.OrgScopeQuery,
		Body:     construct(*current, targetGroupID),
	}, nil); err != nil {
		return nil, fmt.Errorf("moving the device to server group %d: %w", targetGroupID, err)
	}

	updated, err := r.read(ctx, deviceID)
	if err != nil {
		return nil, fmt.Errorf("the move was accepted but the device could not be read back: %w", err)
	}

	return updated, nil
}

// construct builds the PUT body for assign. See the comment on apiDevice for
// why every writable field is included rather than just server_group_id.
func construct(current apiDevice, targetGroupID int64) map[string]any {
	tags := current.Tags
	if tags == nil {
		tags = []string{}
	}
	ipAddrs := current.IPAddrs
	if ipAddrs == nil {
		ipAddrs = []string{}
	}

	return map[string]any{
		"server_group_id": targetGroupID,
		"exception":       current.Exception,
		"tags":            tags,
		"custom_name":     current.CustomName,
		"ip_addrs":        ipAddrs,
	}
}
