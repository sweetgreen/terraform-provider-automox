package policy

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func TestScriptSemanticEquality(t *testing.T) {
	ctx := context.Background()

	cases := []struct {
		name       string
		configured string
		returned   string
		equal      bool
	}{
		{"identical", "exit 0", "exit 0", true},
		{"LF", "exit 0\n", "exit 0", true},
		{"CRLF", "exit 0\r\n", "exit 0", true},
		{"CR", "exit 0\r", "exit 0", true},
		{"several newlines", "exit 0\n\n\n", "exit 0", true},
		{"several CRLF", "exit 0\r\n\r\n", "exit 0", true},
		{"trailing spaces and tabs", "exit 0 \t \n", "exit 0", true},
		{"leading whitespace", "\n  exit 0", "exit 0", true},
		{"byte-order mark", "\ufeffexit 0\r\n", "exit 0", true},
		{"zero-width space", "exit 0\u200b", "exit 0", true},
		{"no-break space", "exit 0\u00a0", "exit 0", true},
		{"NUL", "exit 0\x00", "exit 0", true},
		{"multi-line body kept", "a\r\nb\r\n", "a\r\nb", true},
		{"both sides carry whitespace", "exit 0\n", "exit 0 ", true},

		{"body change", "exit 0\n", "exit 1", false},
		{"body change without whitespace", "exit 0", "exit 1", false},
		{"interior newline added", "exit\n0", "exit 0", false},
		{"interior whitespace changed", "a \nb", "a\nb", false},
		{"interior zero-width space", "exit\u200b 0", "exit 0", false},
		{"DEL is kept by Automox", "exit 0\x7f", "exit 0", false},
		{"U+2066 is kept by Automox", "exit 0\u2066", "exit 0", false},
		{"C0 control is kept by Automox", "exit 0\x1c", "exit 0", false},
		{"text appended", "exit 0\n", "exit 0\nexit 1", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			equal, diags := scriptString(tc.configured).StringSemanticEquals(ctx, scriptString(tc.returned))
			if diags.HasError() {
				t.Fatalf("unexpected diagnostics: %v", diags)
			}
			if equal != tc.equal {
				t.Fatalf("StringSemanticEquals(%q, %q) = %v, want %v", tc.configured, tc.returned, equal, tc.equal)
			}

			// The framework passes the prior value as the receiver; the rule must
			// not depend on which side is which.
			reverse, _ := scriptString(tc.returned).StringSemanticEquals(ctx, scriptString(tc.configured))
			if reverse != equal {
				t.Fatalf("semantic equality is not symmetric for %q and %q", tc.configured, tc.returned)
			}
		})
	}
}

// TestAutomoxTrimSet pins the measured set from both sides: every rune Automox
// was observed to trim, and the neighbouring runes it was observed to keep.
func TestAutomoxTrimSet(t *testing.T) {
	trimmed := []rune{
		0x00, 0x09, 0x0A, 0x0B, 0x0C, 0x0D, 0x20,
		0x85, 0xA0, 0xAD,
		0x034F, 0x061C, 0x115F, 0x1160, 0x1680, 0x17B4, 0x17B5, 0x180E,
		0x2028, 0x2029, 0x202F, 0x205F,
		0x2800, 0x3000, 0x3164, 0xFEFF, 0xFFA0,
		0x1D159, 0xE0020,
	}
	for r := rune(0x2000); r <= 0x200F; r++ {
		trimmed = append(trimmed, r)
	}
	for r := rune(0x2060); r <= 0x2065; r++ {
		trimmed = append(trimmed, r)
	}
	for r := rune(0x206A); r <= 0x206F; r++ {
		trimmed = append(trimmed, r)
	}
	for r := rune(0x1D173); r <= 0x1D17A; r++ {
		trimmed = append(trimmed, r)
	}

	kept := []rune{
		0x01, 0x0E, 0x1B, 0x1C, 0x1F, 0x7F, 0x0600, 0x17B6,
		0x2066, 0x2069, 0xFFF9, 0xE000, 0xE0001, 0xE0021, 0x1D17B,
		'a', '0', '}',
	}

	for _, r := range trimmed {
		if !isAutomoxTrimmed(r) {
			t.Errorf("U+%04X is trimmed by Automox but not by the provider", r)
		}
		if got := automoxTrim(string(r) + "x" + string(r)); got != "x" {
			t.Errorf("automoxTrim around U+%04X = %q, want %q", r, got, "x")
		}
	}
	for _, r := range kept {
		if isAutomoxTrimmed(r) {
			t.Errorf("U+%04X is kept by Automox but trimmed by the provider", r)
		}
	}
}

func TestScriptStringTypeReadsExistingState(t *testing.T) {
	ctx := context.Background()

	// State written by v0.1.6 stores these attributes as plain strings.
	if got := (scriptStringType{}).TerraformType(ctx); !got.Equal(tftypes.String) {
		t.Fatalf("TerraformType = %s, want tftypes.String", got)
	}

	value, err := scriptStringType{}.ValueFromTerraform(ctx, tftypes.NewValue(tftypes.String, "exit 0\n"))
	if err != nil {
		t.Fatalf("ValueFromTerraform: %v", err)
	}
	script, ok := value.(scriptStringValue)
	if !ok {
		t.Fatalf("ValueFromTerraform returned %T, want scriptStringValue", value)
	}
	if script.ValueString() != "exit 0\n" {
		t.Fatalf("ValueFromTerraform = %q, want the stored string unchanged", script.ValueString())
	}

	null, err := scriptStringType{}.ValueFromTerraform(ctx, tftypes.NewValue(tftypes.String, nil))
	if err != nil {
		t.Fatalf("ValueFromTerraform(null): %v", err)
	}
	if !null.IsNull() {
		t.Fatalf("ValueFromTerraform(null) = %v, want null", null)
	}
}

// TestScriptAttributesRoundTrip guards every place that converts configuration
// values by Go type. One that misses the custom type silently turns a script
// into null on read, or leaves it out of the request body.
func TestScriptAttributesRoundTrip(t *testing.T) {
	ctx := context.Background()
	scripts := []string{"evaluation_code", "remediation_code", "installation_code"}

	for _, name := range scripts {
		attribute := configurationSchema().GetAttributes()[name]
		if _, ok := attribute.GetType().(scriptStringType); !ok {
			t.Errorf("%s has type %T, want scriptStringType", name, attribute.GetType())
		}
	}

	fromAPI, diags := configurationFromAPI(ctx, map[string]any{
		"os_family":         "Windows",
		"evaluation_code":   "exit 0",
		"remediation_code":  "exit 1",
		"installation_code": "exit 2",
	})
	if diags.HasError() {
		t.Fatalf("configurationFromAPI: %v", diags)
	}
	attrs := fromAPI.Attributes()
	for name, want := range map[string]string{
		"evaluation_code": "exit 0", "remediation_code": "exit 1", "installation_code": "exit 2",
	} {
		got, ok := attrs[name].(scriptStringValue)
		if !ok {
			t.Fatalf("%s read as %T, want scriptStringValue", name, attrs[name])
		}
		if got.ValueString() != want {
			t.Errorf("%s = %q, want %q", name, got.ValueString(), want)
		}
	}

	// A script absent from the response is null, of the script type.
	absent, diags := configurationFromAPI(ctx, map[string]any{"os_family": "Windows"})
	if diags.HasError() {
		t.Fatalf("configurationFromAPI: %v", diags)
	}
	if v, ok := absent.Attributes()["installation_code"].(scriptStringValue); !ok || !v.IsNull() {
		t.Fatalf("absent installation_code = %#v, want a null scriptStringValue", absent.Attributes()["installation_code"])
	}

	// The request carries the script as configured; Automox does the trimming.
	configured := map[string]attr.Value{}
	for name, value := range fromAPI.Attributes() {
		configured[name] = value
	}
	configured["evaluation_code"] = scriptString("exit 0\r\n")
	configured["remediation_code"] = scriptString("\ufeffexit 1\n")
	configured["installation_code"] = scriptStringNull()
	obj, diags := types.ObjectValue(configurationAttrTypes(), configured)
	if diags.HasError() {
		t.Fatalf("ObjectValue: %v", diags)
	}

	body, diags := configurationToAPI(ctx, obj)
	if diags.HasError() {
		t.Fatalf("configurationToAPI: %v", diags)
	}
	if body["evaluation_code"] != "exit 0\r\n" {
		t.Errorf("evaluation_code sent as %#v, want the configured script", body["evaluation_code"])
	}
	if body["remediation_code"] != "\ufeffexit 1\n" {
		t.Errorf("remediation_code sent as %#v, want the configured script", body["remediation_code"])
	}
	if _, present := body["installation_code"]; present {
		t.Errorf("unset installation_code was sent: %#v", body["installation_code"])
	}
}
