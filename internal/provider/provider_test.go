package provider

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
)

// The schema must be valid protocol v6, or `tofu init` fails with an error that
// points at the registry rather than at the schema.
func TestUnitProviderSchemaIsValid(t *testing.T) {
	p := New("test")()
	server := providerserver.NewProtocol6(p)()

	resp, err := server.GetProviderSchema(context.Background(), &tfprotov6.GetProviderSchemaRequest{})
	if err != nil {
		t.Fatalf("GetProviderSchema: %v", err)
	}
	if len(resp.Diagnostics) > 0 {
		t.Fatalf("schema diagnostics: %v", resp.Diagnostics)
	}
	if resp.Provider == nil {
		t.Fatal("no provider schema returned")
	}

	want := map[string]bool{"api_key": false, "organization_id": false, "base_url": false}
	for _, attr := range resp.Provider.Block.Attributes {
		if _, ok := want[attr.Name]; !ok {
			t.Errorf("unexpected provider attribute %q", attr.Name)
			continue
		}
		want[attr.Name] = true
		if attr.Required {
			t.Errorf("%s is Required; it must be Optional so the environment variable can supply it", attr.Name)
		}
		if attr.Name == "api_key" && !attr.Sensitive {
			t.Error("api_key must be marked Sensitive so it is redacted from plan output")
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("provider schema is missing %q", name)
		}
	}
}
