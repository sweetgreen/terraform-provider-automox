package policy

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
)

// JSON decoding gives float64 for every number and map[string]any for every
// object, so the conversion back into typed Terraform values is explicit.
//
// A value whose shape does not match the schema becomes null rather than an
// error. Automox has already been observed returning fields absent from its own
// documentation, and refusing to read an entire policy because one attribute
// arrived in an unexpected shape would be a worse failure than ignoring it. The
// attributes the provider actually manages are covered by acceptance tests, so a
// real mismatch surfaces as a diff rather than silently.

func nullOf(typ attr.Type) attr.Value {
	switch t := typ.(type) {
	case basetypes.StringType:
		return types.StringNull()
	case basetypes.BoolType:
		return types.BoolNull()
	case basetypes.Int64Type:
		return types.Int64Null()
	case basetypes.ListType:
		return types.ListNull(t.ElemType)
	case basetypes.ObjectType:
		return types.ObjectNull(t.AttrTypes)
	default:
		return types.StringNull()
	}
}

func goToAttr(ctx context.Context, typ attr.Type, raw any) attr.Value {
	switch t := typ.(type) {
	case basetypes.StringType:
		if s, ok := raw.(string); ok {
			return types.StringValue(s)
		}
	case basetypes.BoolType:
		if b, ok := raw.(bool); ok {
			return types.BoolValue(b)
		}
	case basetypes.Int64Type:
		switch n := raw.(type) {
		case float64:
			return types.Int64Value(int64(n))
		case int64:
			return types.Int64Value(n)
		case int:
			return types.Int64Value(int64(n))
		}
	case basetypes.ListType:
		items, ok := raw.([]any)
		if !ok {
			break
		}
		elems := make([]attr.Value, 0, len(items))
		for _, item := range items {
			elems = append(elems, goToAttr(ctx, t.ElemType, item))
		}
		list, diags := types.ListValue(t.ElemType, elems)
		if diags.HasError() {
			return types.ListNull(t.ElemType)
		}
		return list
	case basetypes.ObjectType:
		obj, ok := raw.(map[string]any)
		if !ok {
			break
		}
		values := make(map[string]attr.Value, len(t.AttrTypes))
		for name, attrType := range t.AttrTypes {
			nested, present := obj[name]
			if !present || nested == nil {
				values[name] = nullOf(attrType)
				continue
			}
			values[name] = goToAttr(ctx, attrType, nested)
		}
		result, diags := types.ObjectValue(t.AttrTypes, values)
		if diags.HasError() {
			return types.ObjectNull(t.AttrTypes)
		}
		return result
	}

	return nullOf(typ)
}
