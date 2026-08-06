package policy

import (
	"context"
	"regexp"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	clockTime = regexp.MustCompile(`^([01][0-9]|2[0-3]):[0-5][0-9]$`)
	utcOffset = regexp.MustCompile(`^UTC[+-][0-9]{4}$`)
)

// configurationSchema declares the policy body.
//
// Automox's document models this as three mutually exclusive shapes selected by
// policy type, with a further three for patch policies selected by patch_rule.
// The live API does not behave that way: patch policies return worklet fields
// such as evaluation_code and remediation_code, and four attributes the document
// omits entirely. Modelling the documented variants would fail to round-trip real
// policies.
//
// So this is one flat object where everything is optional and computed, and
// legality is enforced by ValidateConfig against the rules the API actually
// applies. Optional+Computed matters: Automox fills in defaults for most of these
// and a plain Optional attribute would show a permanent diff against them.
func configurationSchema() schema.SingleNestedAttribute {
	return schema.SingleNestedAttribute{
		Required: true,
		MarkdownDescription: "Policy behaviour. Which attributes apply depends on `policy_type` " +
			"and, for patch policies, on `patch_rule`; the provider validates the combination " +
			"before calling the API.",
		Attributes: map[string]schema.Attribute{
			// --- patch ---
			"patch_rule": schema.StringAttribute{
				Optional: true,
				Computed: true,
				MarkdownDescription: "How a patch policy selects patches: `all`, `filter`, " +
					"`manual`, or `advanced`. Patch policies only.",
				Validators: []validator.String{
					stringvalidator.OneOf(PatchRuleAll, PatchRuleFilter, PatchRuleManual, PatchRuleAdvanced),
				},
			},
			"auto_patch": schema.BoolAttribute{
				Optional:            true,
				Computed:            true,
				MarkdownDescription: "Install patches automatically when the policy runs.",
			},
			"auto_reboot": schema.BoolAttribute{
				Optional:            true,
				Computed:            true,
				MarkdownDescription: "Reboot devices automatically after patching.",
			},
			"filter_type": schema.StringAttribute{
				Optional: true,
				Computed: true,
				MarkdownDescription: "`all`, `include`, `exclude`, or `severity`.\n\n" +
					"Automox requires this on **every** patch policy, not only when " +
					"`patch_rule = \"filter\"` as its error message suggests.\n\n" +
					"`all` is what Automox stores for a policy whose `patch_rule` is " +
					"`advanced`, where the selection is expressed by `advanced_filter` " +
					"instead.",
				Validators: []validator.String{
					stringvalidator.OneOf(FilterTypeAll, FilterTypeInclude, FilterTypeExclude, FilterTypeSeverity),
				},
			},
			"filters": schema.ListAttribute{
				Optional:            true,
				Computed:            true,
				ElementType:         types.StringType,
				MarkdownDescription: "Package names to include or exclude, used with `filter_type`.",
			},
			"severity_filter": schema.ListAttribute{
				Optional:    true,
				Computed:    true,
				ElementType: types.StringType,
				MarkdownDescription: "Severities to act on when `filter_type = \"severity\"`: " +
					"`no_known_cves`, `none`, `unknown`, `low`, `medium`, `high`, `critical`.",
			},
			"advanced_filter": schema.ListNestedAttribute{
				Optional: true,
				Computed: true,
				MarkdownDescription: "Filter expressions for `patch_rule = \"advanced\"`. " +
					"Automox requires at least one when that rule is selected.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"left": schema.StringAttribute{
							Required: true,
							MarkdownDescription: "Field to filter on: `display-name`, `severity`, " +
								"`patch-source`, `patch-os`, `type`, or `patch-days-old`.",
						},
						"condition": schema.StringAttribute{
							Optional: true,
							Computed: true,
							MarkdownDescription: "Comparison operation. Valid values depend on `left`; " +
								"for example, `patch-source` supports `is` and `is-not`, while " +
								"`display-name` supports `contains` and `does-not-contain`.",
						},
						// Kept only so state and configurations written against v0.1.3 remain
						// readable. Automox calls this field `condition`; sending `op` causes every
						// policy update to fail validation, so new configurations must use condition.
						"op": schema.StringAttribute{
							Optional:           true,
							Computed:           true,
							DeprecationMessage: "Use condition. Automox does not accept op in advanced filters.",
						},
						"right": schema.StringAttribute{Required: true},
					},
				},
			},
			"include_optional": schema.BoolAttribute{
				Optional:            true,
				Computed:            true,
				MarkdownDescription: "Include optional Windows updates.",
			},
			"missed_patch_window": schema.BoolAttribute{Optional: true, Computed: true},
			"is_patch_tuesday": schema.BoolAttribute{
				Optional:            true,
				Computed:            true,
				MarkdownDescription: "Run only on the second Tuesday of the month.",
			},
			"patch_tuesday_offset": schema.Int64Attribute{
				Optional:            true,
				Computed:            true,
				MarkdownDescription: "Days after Patch Tuesday to run, 0 to 26.",
			},

			// --- worklet, and returned on patch policies too ---
			"os_family": schema.StringAttribute{
				Optional: true,
				Computed: true,
				MarkdownDescription: "`Windows`, `Mac`, or `Linux`. Required for worklet and " +
					"required-software policies.\n\n" +
					"Matched exactly: `macOS` and lowercase `windows` are both rejected.",
				Validators: []validator.String{
					// Case-sensitive deliberately. OneOf is case-sensitive and the API
					// rejects "windows" and "macOS", so an incorrectly cased value is
					// caught at plan time rather than becoming a 400 during apply.
					stringvalidator.OneOf(OSFamilyWindows, OSFamilyMac, OSFamilyLinux),
				},
			},
			"evaluation_code": schema.StringAttribute{
				Optional: true,
				Computed: true,
				MarkdownDescription: "Script deciding whether remediation is needed. Despite the " +
					"vendor documentation, the API returns this on patch policies as well.",
			},
			"remediation_code": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				MarkdownDescription: "Script run when evaluation indicates remediation is needed.",
			},
			"installation_code": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				MarkdownDescription: "Installation script. Required for `required_software` policies.",
			},
			"refresh_before_remediation": schema.BoolAttribute{Optional: true, Computed: true},

			// --- required software ---
			"package_name":    schema.StringAttribute{Optional: true, Computed: true},
			"package_version": schema.StringAttribute{Optional: true, Computed: true},

			// --- notification and deferral ---
			"notify_user":                     schema.BoolAttribute{Optional: true, Computed: true},
			"notify_reboot_user":              schema.BoolAttribute{Optional: true, Computed: true},
			"notify_deferred_reboot_user":     schema.BoolAttribute{Optional: true, Computed: true},
			"install_deferral_enabled":        schema.BoolAttribute{Optional: true, Computed: true},
			"pending_reboot_deferral_enabled": schema.BoolAttribute{Optional: true, Computed: true},
			"notify_user_message_timeout": schema.Int64Attribute{
				Optional: true, Computed: true,
				MarkdownDescription: "Minutes a notification stays up, 15 to 480.",
			},
			"notify_deferred_reboot_user_message_timeout": schema.Int64Attribute{Optional: true, Computed: true},
			"custom_notification_max_delays":              schema.Int64Attribute{Optional: true, Computed: true},
			"custom_notification_deferment_periods": schema.ListAttribute{
				Optional: true, Computed: true, ElementType: types.Int64Type,
				MarkdownDescription: "Deferral options offered to the user, in hours. At most three, each 24 or fewer.",
			},
			"custom_notification_patch_message": schema.StringAttribute{
				Optional: true, Computed: true,
				MarkdownDescription: "Windows patch notification text, up to 125 characters.",
			},
			"custom_notification_patch_message_mac": schema.StringAttribute{
				Optional: true, Computed: true,
				MarkdownDescription: "macOS patch notification text, up to 70 characters.",
			},
			"custom_notification_reboot_message":                schema.StringAttribute{Optional: true, Computed: true},
			"custom_notification_reboot_message_mac":            schema.StringAttribute{Optional: true, Computed: true},
			"notify_user_auto_deferral_enabled":                 schema.BoolAttribute{Optional: true, Computed: true},
			"notify_deferred_reboot_user_auto_deferral_enabled": schema.BoolAttribute{Optional: true, Computed: true},
			"custom_pending_reboot_notification_message":        schema.StringAttribute{Optional: true, Computed: true},
			"custom_pending_reboot_notification_message_mac":    schema.StringAttribute{Optional: true, Computed: true},
			"custom_pending_reboot_notification_max_delays":     schema.Int64Attribute{Optional: true, Computed: true},
			"custom_pending_reboot_notification_deferment_periods": schema.ListAttribute{
				Optional: true, Computed: true, ElementType: types.Int64Type,
			},

			// --- targeting ---
			"device_filters_enabled": schema.BoolAttribute{Optional: true, Computed: true},
			"device_filters": schema.ListNestedAttribute{
				Optional:            true,
				Computed:            true,
				MarkdownDescription: "Narrows the policy to a subset of the devices in its server groups.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"field": schema.StringAttribute{
							Required: true,
							MarkdownDescription: "`tag`, `hostname`, `ip_addr`, `os_family`, " +
								"`os_version_id`, or `organizational_unit`.",
							Validators: []validator.String{
								stringvalidator.OneOf("tag", "hostname", "ip_addr", "os_family",
									"os_version_id", "organizational_unit"),
							},
						},
						"op": schema.StringAttribute{
							Required: true,
							MarkdownDescription: "`in` (is), `not_in` (is not), `like_any` (contains), " +
								"or `not_like_any` (does not contain).",
							Validators: []validator.String{
								stringvalidator.OneOf("in", "not_in", "like_any", "not_like_any"),
							},
						},
						"value": schema.ListAttribute{Required: true, ElementType: types.StringType},
					},
				},
			},

			// --- undocumented, but returned live ---
			"install_notification_deadline": schema.Int64Attribute{
				Optional: true, Computed: true,
				MarkdownDescription: "Hours before installation is forced. Not present in Automox's " +
					"published schema, but returned by the API.",
			},
			"pending_reboot_notification_deadline": schema.Int64Attribute{
				Optional: true, Computed: true,
				MarkdownDescription: "Hours before a pending reboot is forced. Not present in " +
					"Automox's published schema, but returned by the API.",
			},
			"install_do_not_disturb_honored": schema.BoolAttribute{Optional: true, Computed: true},
			"reboot_do_not_disturb_honored":  schema.BoolAttribute{Optional: true, Computed: true},
		},
	}
}

// configurationAttrTypes mirrors configurationSchema for building object values.
func configurationAttrTypes() map[string]attr.Type {
	return configurationSchema().GetAttributes().Type().(types.ObjectType).AttrTypes
}

var _ = context.Background
