// Package provider wires the Automox API client into Terraform.
package provider

import (
	"context"
	"os"
	"strconv"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"

	"github.com/sweetgreen/terraform-provider-automox/internal/client"
)

// Environment variables, matching the convention the Automox console documents
// for its own tooling.
const (
	envAPIKey         = "AUTOMOX_API_KEY"
	envOrganizationID = "AUTOMOX_ORGANIZATION_ID"
	envBaseURL        = "AUTOMOX_BASE_URL"
)

var (
	_ provider.Provider = &automoxProvider{}
)

// New returns the provider constructor goreleaser stamps a version into.
func New(version string) func() provider.Provider {
	return func() provider.Provider {
		return &automoxProvider{version: version}
	}
}

type automoxProvider struct {
	version string
}

// providerModel mirrors the schema below.
type providerModel struct {
	APIKey         types.String `tfsdk:"api_key"`
	OrganizationID types.Int64  `tfsdk:"organization_id"`
	BaseURL        types.String `tfsdk:"base_url"`
}

func (p *automoxProvider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "automox"
	resp.Version = p.version
}

func (p *automoxProvider) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manage [Automox](https://www.automox.com/) patch policies, worklets, " +
			"server groups, maintenance windows, and devices.",
		Attributes: map[string]schema.Attribute{
			"api_key": schema.StringAttribute{
				Optional:  true,
				Sensitive: true,
				MarkdownDescription: "Automox API key. May also be set with the `" + envAPIKey +
					"` environment variable, which is preferred so the key stays out of configuration " +
					"files and state.\n\n" +
					"An **Organization** key reaches policies, server groups, devices, and maintenance " +
					"windows. Account-level operations additionally require a **Global** key held by a " +
					"Full Administrator.",
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
				},
			},
			"organization_id": schema.Int64Attribute{
				Optional: true,
				MarkdownDescription: "Automox organization (zone) ID. May also be set with the `" +
					envOrganizationID + "` environment variable.\n\n" +
					"Most Automox endpoints are scoped by this integer. A few are scoped by the " +
					"organization's UUID instead; the provider resolves that automatically, so only " +
					"the integer is configured here.",
				Validators: []validator.Int64{
					int64validator.AtLeast(1),
				},
			},
			"base_url": schema.StringAttribute{
				Optional: true,
				MarkdownDescription: "Automox Console API base URL. Defaults to `" +
					client.DefaultBaseURL + "`. May also be set with the `" + envBaseURL +
					"` environment variable. Override only when targeting a regional console.",
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
				},
			},
		},
	}
}

func (p *automoxProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var config providerModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// An unknown value here means it depends on another resource that has not
	// been applied yet. Reported per-attribute so the practitioner knows which
	// one to make concrete, rather than a generic "provider misconfigured".
	if config.APIKey.IsUnknown() {
		resp.Diagnostics.AddAttributeError(
			path.Root("api_key"),
			"Automox API key is not known at plan time",
			"The api_key value depends on something not yet applied. Set it from the "+
				envAPIKey+" environment variable, or apply the resource it depends on first.",
		)
	}
	if config.OrganizationID.IsUnknown() {
		resp.Diagnostics.AddAttributeError(
			path.Root("organization_id"),
			"Automox organization ID is not known at plan time",
			"The organization_id value depends on something not yet applied. Set it from the "+
				envOrganizationID+" environment variable, or apply the resource it depends on first.",
		)
	}
	if resp.Diagnostics.HasError() {
		return
	}

	// Configuration wins over environment, which is the convention practitioners
	// expect: an explicit value in a file is a deliberate override.
	apiKey := os.Getenv(envAPIKey)
	if !config.APIKey.IsNull() {
		apiKey = config.APIKey.ValueString()
	}

	baseURL := os.Getenv(envBaseURL)
	if !config.BaseURL.IsNull() {
		baseURL = config.BaseURL.ValueString()
	}

	orgID, orgDiag := resolveOrganizationID(config.OrganizationID)
	if orgDiag != nil {
		resp.Diagnostics.Append(orgDiag)
		return
	}

	if apiKey == "" {
		resp.Diagnostics.AddAttributeError(
			path.Root("api_key"),
			"Missing Automox API key",
			"Set the api_key provider attribute or the "+envAPIKey+" environment variable. "+
				"Create a key in the Automox console under Settings then API Keys.",
		)
		return
	}

	// The key is registered for masking before anything else can log, and never
	// appears in a diagnostic. Only whether it was supplied is worth reporting.
	ctx = tflog.MaskAllFieldValuesStrings(ctx, apiKey)
	tflog.Debug(ctx, "configuring the Automox client", map[string]any{
		"organization_id":  orgID,
		"base_url":         baseURLOrDefault(baseURL),
		"api_key_provided": true,
	})

	c, err := client.New(client.Options{
		BaseURL:        baseURL,
		APIKey:         apiKey,
		OrganizationID: orgID,
	})
	if err != nil {
		resp.Diagnostics.AddError(
			"Could not configure the Automox client",
			err.Error(),
		)
		return
	}

	// Resources and data sources share one client, so the organization lookup it
	// caches is performed once per run rather than per resource.
	resp.ResourceData = c
	resp.DataSourceData = c
}

// resolveOrganizationID prefers configuration over environment and rejects an
// unparseable environment value rather than silently falling back to unscoped.
func resolveOrganizationID(configured types.Int64) (int64, diag.Diagnostic) {
	if !configured.IsNull() {
		return configured.ValueInt64(), nil
	}

	raw := os.Getenv(envOrganizationID)
	if raw == "" {
		// Not an error. Endpoints needing an organization report it precisely at
		// the point of use, naming the operation that required one.
		return 0, nil
	}

	parsed, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, diag.NewAttributeErrorDiagnostic(
			path.Root("organization_id"),
			"Invalid "+envOrganizationID+" environment variable",
			"Expected an integer organization ID, got "+strconv.Quote(raw)+". "+
				"The Automox console shows this on the organization's settings page.",
		)
	}
	if parsed < 1 {
		return 0, diag.NewAttributeErrorDiagnostic(
			path.Root("organization_id"),
			"Invalid "+envOrganizationID+" environment variable",
			"Organization IDs are positive integers, got "+raw+".",
		)
	}
	return parsed, nil
}

func baseURLOrDefault(v string) string {
	if v == "" {
		return client.DefaultBaseURL
	}
	return v
}

func (p *automoxProvider) Resources(_ context.Context) []func() resource.Resource {
	return resources()
}

func (p *automoxProvider) DataSources(_ context.Context) []func() datasource.DataSource {
	return dataSources()
}
