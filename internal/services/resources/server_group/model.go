package server_group

import (
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// serverGroupModel is the Terraform-facing shape of automox_server_group.
type serverGroupModel struct {
	ID                  types.Int64  `tfsdk:"id"`
	UUID                types.String `tfsdk:"uuid"`
	Name                types.String `tfsdk:"name"`
	RefreshInterval     types.Int64  `tfsdk:"refresh_interval"`
	ParentServerGroupID types.Int64  `tfsdk:"parent_server_group_id"`
	UIColor             types.String `tfsdk:"ui_color"`
	Notes               types.String `tfsdk:"notes"`
	EnableOSAutoUpdate  types.Bool   `tfsdk:"enable_os_auto_update"`
	EnableWSUS          types.Bool   `tfsdk:"enable_wsus"`
	WSUSServer          types.String `tfsdk:"wsus_server"`
	Policies            types.List   `tfsdk:"policies"`
	OrganizationID      types.Int64  `tfsdk:"organization_id"`
	ServerCount         types.Int64  `tfsdk:"server_count"`
}

// apiServerGroup is the wire shape. Nullable fields are pointers so the
// difference between "false" and "not set" survives decoding — that distinction
// is the whole point of the tri-state attributes.
type apiServerGroup struct {
	ID                  int64   `json:"id"`
	UUID                string  `json:"uuid"`
	Name                string  `json:"name"`
	RefreshInterval     int64   `json:"refresh_interval"`
	ParentServerGroupID int64   `json:"parent_server_group_id"`
	UIColor             *string `json:"ui_color"`
	Notes               *string `json:"notes"`
	EnableOSAutoUpdate  *bool   `json:"enable_os_auto_update"`
	OrganizationID      int64   `json:"organization_id"`
	ServerCount         *int64  `json:"server_count"`
	Policies            []int64 `json:"policies"`

	// WSUSConfig is the read side of enable_wsus and wsus_server. The API accepts
	// those two fields on write and returns this nested object on read; neither
	// write field appears in a read response at all.
	WSUSConfig *apiWSUSConfig `json:"wsus_config"`
}

type apiWSUSConfig struct {
	ID            int64   `json:"id"`
	ServerGroupID int64   `json:"server_group_id"`
	IsManaged     *bool   `json:"is_managed"`
	ServerURL     *string `json:"server_url"`
}

// EnableWSUS and WSUSServer read through the nested object, so callers do not
// have to know about the asymmetry.
func (g *apiServerGroup) enableWSUS() *bool {
	if g.WSUSConfig == nil {
		return nil
	}
	return g.WSUSConfig.IsManaged
}

func (g *apiServerGroup) wsusServer() *string {
	if g.WSUSConfig == nil {
		return nil
	}
	return g.WSUSConfig.ServerURL
}
