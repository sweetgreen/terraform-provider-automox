package device_group_assignment

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/types"
)

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
		return 0, fmt.Errorf("device IDs are positive")
	}
	return id, nil
}

// flatten maps an API device onto the Terraform model.
//
// organization_id comes from the configured client rather than the API
// response, the same as every other resource's computed organization_id: the
// model attribute is still unknown the first time this runs during Create, so
// a value has to come from somewhere other than a prior state read.
func flatten(api apiDevice, orgID int64) deviceGroupAssignmentModel {
	return deviceGroupAssignmentModel{
		DeviceID:       types.Int64Value(api.ID),
		ServerGroupID:  types.Int64Value(api.ServerGroupID),
		OrganizationID: types.Int64Value(orgID),
	}
}
