package provider

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/resource"
)

// TestUnitRegistration_NamesAreUniqueAndPrefixed catches the two registration
// mistakes that produce confusing failures far from their cause: a duplicate
// type name, where the later registration silently wins, and a name missing the
// provider prefix, which Terraform cannot address at all.
func TestUnitRegistration_NamesAreUniqueAndPrefixed(t *testing.T) {
	ctx := context.Background()
	seen := map[string]string{}

	for _, ctor := range resources() {
		var resp resource.MetadataResponse
		ctor().Metadata(ctx, resource.MetadataRequest{ProviderTypeName: "automox"}, &resp)
		record(t, seen, resp.TypeName, "resource")
	}
	for _, ctor := range dataSources() {
		var resp datasource.MetadataResponse
		ctor().Metadata(ctx, datasource.MetadataRequest{ProviderTypeName: "automox"}, &resp)
		record(t, seen, resp.TypeName, "data source")
	}

	t.Logf("registered %d type names", len(seen))
	if len(seen) < 17 {
		t.Errorf("only %d types registered; expected at least 17", len(seen))
	}
}

func record(t *testing.T, seen map[string]string, name, kind string) {
	t.Helper()
	if name == "" {
		t.Fatalf("a %s registered an empty type name", kind)
	}
	if len(name) < 8 || name[:8] != "automox_" {
		t.Errorf("%s %q is not prefixed with automox_", kind, name)
	}
	if prev, dup := seen[name]; dup {
		t.Errorf("%q is registered twice (as %s and %s); the second silently wins", name, prev, kind)
	}
	seen[name] = kind
}

var _ provider.Provider = &automoxProvider{}
