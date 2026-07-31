// Package policy_file implements the automox_policy_file resource: a file
// attached to a worklet policy, such as an installer or a script the worklet
// invokes.
//
// The endpoint behaves unlike the rest of the API in four ways, each established
// against the live service rather than taken from the vendor document:
//
//  1. Upload is multipart/form-data under the field name "file". A JSON body
//     with the same content is rejected with "The file field is required."
//  2. Files are identified by `uuid`. The `id` field exists in every response
//     and is permanently null -- not, as the task description assumed, null
//     initially and populated asynchronously. It was still null after thirty
//     seconds of polling, so there is nothing to wait for.
//  3. Content cannot be read back. There is no download endpoint, so Terraform
//     can confirm a file exists and its size, but never that its bytes still
//     match the configuration. Drift is tracked with a hash instead.
//  4. Requesting an absent uuid returns 500, not 404, so absence is confirmed
//     against the file listing rather than a direct read.
//
// One further hazard is worth stating because it is invisible in configuration:
// uploading two files with the same name to one policy concurrently -- which is
// what Terraform does by default when both resources are ready at once --
// races inside Automox and returns 500 rather than the ordinary "A file with
// that name already exists" rejection. Filenames are unique per policy, so this
// only arises from a configuration that was already wrong, but the resulting
// message is unhelpful enough to be worth recognising.
package policy_file

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/sweetgreen/terraform-provider-automox/internal/client"
)

const ResourceName = "automox_policy_file"

var (
	_ resource.Resource                   = &policyFileResource{}
	_ resource.ResourceWithConfigure      = &policyFileResource{}
	_ resource.ResourceWithValidateConfig = &policyFileResource{}
)

// New returns the resource constructor for the provider registry.
func New() resource.Resource { return &policyFileResource{} }

type policyFileResource struct {
	client *client.Client
}

func (r *policyFileResource) Metadata(_ context.Context, _ resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = ResourceName
}

func (r *policyFileResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	c, ok := req.ProviderData.(*client.Client)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected provider data",
			fmt.Sprintf("Expected *client.Client, got %T. This is a bug in the provider.",
				req.ProviderData),
		)
		return
	}
	r.client = c
}

func (r *policyFileResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "A file attached to a worklet policy — an installer, or a " +
			"script the worklet calls.\n\n" +
			"Automox provides no way to read a file's contents back, so Terraform " +
			"cannot detect that a file was edited outside Terraform. It tracks the " +
			"content hash of what it uploaded instead: changing `content` in " +
			"configuration replaces the file, but a change made in the Automox console " +
			"is invisible.\n\n" +
			"Every attribute forces replacement. Automox rejects a second upload under " +
			"an existing filename with \"A file with that name already exists\", so a " +
			"change is a delete followed by an upload either way — Terraform does it " +
			"explicitly rather than pretending to update in place.",
		Attributes: map[string]schema.Attribute{
			"policy_id": schema.Int64Attribute{
				Required:            true,
				MarkdownDescription: "The worklet policy this file belongs to.",
				PlanModifiers:       []planmodifier.Int64{int64planmodifier.RequiresReplace()},
			},
			"filename": schema.StringAttribute{
				Required: true,
				MarkdownDescription: "The name the file is stored under. Must be unique " +
					"within the policy.",
				Validators:    []validator.String{stringvalidator.LengthAtLeast(1)},
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"content": schema.StringAttribute{
				Required: true,
				MarkdownDescription: "The file's contents. Stored in Terraform state in " +
					"plaintext, like any configured attribute, so treat a script here the " +
					"same way you would treat the worklet source in `automox_policy`.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"uuid": schema.StringAttribute{
				Computed: true,
				MarkdownDescription: "Automox's identifier for the file. The `id` field " +
					"this API also returns is always null and is not exposed.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"size": schema.Int64Attribute{
				Computed:            true,
				MarkdownDescription: "Size in bytes, as Automox recorded it.",
			},
			"content_sha256": schema.StringAttribute{
				Computed: true,
				MarkdownDescription: "SHA-256 of the uploaded content. Recorded because " +
					"the contents cannot be read back, so this is the only evidence of " +
					"what was uploaded.",
			},
		},
	}
}

// ValidateConfig rejects an empty filename before the request.
//
// Automox accepts an upload with a blank filename and stores something
// unaddressable, which is worse than a rejection.
func (r *policyFileResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var config policyFileModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if !config.Filename.IsNull() && !config.Filename.IsUnknown() {
		if name := config.Filename.ValueString(); name == "" {
			resp.Diagnostics.AddAttributeError(
				path.Root("filename"),
				"Empty filename",
				"filename must name the file as it should be stored on the policy.",
			)
		}
	}
}

type policyFileModel struct {
	PolicyID types.Int64  `tfsdk:"policy_id"`
	Filename types.String `tfsdk:"filename"`
	Content  types.String `tfsdk:"content"`
	UUID     types.String `tfsdk:"uuid"`
	Size     types.Int64  `tfsdk:"size"`
	SHA256   types.String `tfsdk:"content_sha256"`
}

// apiPolicyFile is the wire shape. `id` is deliberately absent: it is null in
// every response this provider has observed, and modelling it would invite
// configuration to depend on a field that is never populated.
type apiPolicyFile struct {
	UUID     string `json:"uuid"`
	Filename string `json:"filename"`
	Size     int64  `json:"size"`
}

func sha256Of(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}
