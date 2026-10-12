package policy

import (
	"context"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

// Automox trims the script fields of a policy when it saves them, so a script
// that ends in a newline -- what file() produces from almost any editor -- is
// read back shorter than it was planned. Recording that value fails the apply
// with "Provider produced inconsistent result after apply" and taints a policy
// that was in fact created.
//
// The scripts therefore use a string type whose semantic equality ignores what
// Automox trims. The framework compares the API's value with the plan after
// Create and Update and with prior state after Read, and keeps the configured
// value when the two are equal, so the script stays as written and the next plan
// is empty. A change anywhere else in the script still diffs.

// automoxTrim removes what Automox removes from both ends of a script.
func automoxTrim(s string) string {
	return strings.TrimFunc(s, isAutomoxTrimmed)
}

// isAutomoxTrimmed reports whether Automox strips r from the ends of a script.
//
// The set was measured against the live API on 2026-10-12 by writing each
// candidate to an unscheduled, untargeted policy and reading it back. It is
// Laravel's Str::trim: ASCII whitespace and NUL, Unicode whitespace, and Laravel's
// list of invisible characters. Characters next to that list (U+2066, U+FFF9,
// U+E0001, U+1D17B, U+007F, the C0 controls) were confirmed kept.
//
// It is spelled out rather than built on unicode.IsSpace, which disagrees with
// Automox on a byte-order mark, NUL, the zero-width characters and others. A
// rune listed here that Automox keeps would hide a real change to a script; a
// rune missing from here only leaves that rune's apply failing as it did before.
func isAutomoxTrimmed(r rune) bool {
	switch {
	case r == 0x00, r >= 0x09 && r <= 0x0D, r == 0x20:
		return true
	case r == 0x85, r == 0xA0, r == 0xAD:
		return true
	case r == 0x034F, r == 0x061C, r == 0x115F, r == 0x1160, r == 0x1680,
		r == 0x17B4, r == 0x17B5, r == 0x180E:
		return true
	case r >= 0x2000 && r <= 0x200F, r == 0x2028, r == 0x2029, r == 0x202F, r == 0x205F,
		r >= 0x2060 && r <= 0x2065, r >= 0x206A && r <= 0x206F:
		return true
	case r == 0x2800, r == 0x3000, r == 0x3164, r == 0xFEFF, r == 0xFFA0:
		return true
	case r == 0x1D159, r >= 0x1D173 && r <= 0x1D17A, r == 0xE0020:
		return true
	}
	return false
}

var (
	_ basetypes.StringTypable                    = scriptStringType{}
	_ basetypes.StringValuableWithSemanticEquals = scriptStringValue{}
)

// scriptStringType is a Terraform string, so state written before it existed
// reads back unchanged.
type scriptStringType struct {
	basetypes.StringType
}

func (t scriptStringType) Equal(o attr.Type) bool {
	other, ok := o.(scriptStringType)
	if !ok {
		return false
	}
	return t.StringType.Equal(other.StringType)
}

func (t scriptStringType) String() string {
	return "policy.scriptStringType"
}

func (t scriptStringType) ValueType(_ context.Context) attr.Value {
	return scriptStringValue{}
}

func (t scriptStringType) ValueFromString(_ context.Context, in basetypes.StringValue) (basetypes.StringValuable, diag.Diagnostics) {
	return scriptStringValue{StringValue: in}, nil
}

func (t scriptStringType) ValueFromTerraform(ctx context.Context, in tftypes.Value) (attr.Value, error) {
	value, err := t.StringType.ValueFromTerraform(ctx, in)
	if err != nil {
		return nil, err
	}
	stringValue, ok := value.(basetypes.StringValue)
	if !ok {
		return nil, fmt.Errorf("unexpected value type %T", value)
	}
	return scriptStringValue{StringValue: stringValue}, nil
}

// scriptStringValue is a script as configured, equal to any value that differs
// from it only by what Automox trims.
type scriptStringValue struct {
	basetypes.StringValue
}

func scriptStringNull() scriptStringValue {
	return scriptStringValue{StringValue: basetypes.NewStringNull()}
}

func scriptString(s string) scriptStringValue {
	return scriptStringValue{StringValue: basetypes.NewStringValue(s)}
}

func (v scriptStringValue) Equal(o attr.Value) bool {
	other, ok := o.(scriptStringValue)
	if !ok {
		return false
	}
	return v.StringValue.Equal(other.StringValue)
}

func (v scriptStringValue) Type(_ context.Context) attr.Type {
	return scriptStringType{}
}

func (v scriptStringValue) StringSemanticEquals(_ context.Context, newValuable basetypes.StringValuable) (bool, diag.Diagnostics) {
	var diags diag.Diagnostics

	newValue, ok := newValuable.(scriptStringValue)
	if !ok {
		diags.AddError(
			"Semantic Equality Check Error",
			fmt.Sprintf("Expected a %T, got %T. This is a bug in the provider.", v, newValuable),
		)
		return false, diags
	}

	return automoxTrim(v.ValueString()) == automoxTrim(newValue.ValueString()), diags
}
