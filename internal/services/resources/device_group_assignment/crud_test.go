package device_group_assignment

import (
	"reflect"
	"testing"
)

// TestUnitConstructPreservesUnownedFields pins the hazard the issue that added
// this resource called out explicitly: the update endpoint is a full
// replacement, so a body that only set server_group_id would read back as
// having cleared exception, tags, custom_name, and ip_addrs. construct must
// echo back exactly what was read, with only server_group_id changed.
func TestUnitConstructPreservesUnownedFields(t *testing.T) {
	current := apiDevice{
		ID:            5818967,
		Name:          "USSG01RG0100DEV",
		ServerGroupID: 230685, // Default Group
		Exception:     true,
		Tags:          []string{"pos", "ring-1"},
		CustomName:    "Register 1",
		IPAddrs:       []string{"10.0.0.5"},
	}

	got := construct(current, 242983) // Retail POS Lab Windows Devices

	want := map[string]any{
		"server_group_id": int64(242983),
		"exception":       true,
		"tags":            []string{"pos", "ring-1"},
		"custom_name":     "Register 1",
		"ip_addrs":        []string{"10.0.0.5"},
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("construct(%+v, 242983) = %#v, want %#v", current, got, want)
	}
}

// TestUnitConstructNormalizesNilSlices confirms a device with no tags or IPs
// sends empty JSON arrays rather than null, so a full-replacement write does
// not hand the API a value it must guess how to interpret.
func TestUnitConstructNormalizesNilSlices(t *testing.T) {
	current := apiDevice{ID: 1, ServerGroupID: 100}

	got := construct(current, 200)

	if got["tags"] == nil {
		t.Error("tags = nil, want an empty slice")
	}
	if tags, ok := got["tags"].([]string); !ok || len(tags) != 0 {
		t.Errorf("tags = %#v, want an empty []string", got["tags"])
	}
	if ips, ok := got["ip_addrs"].([]string); !ok || len(ips) != 0 {
		t.Errorf("ip_addrs = %#v, want an empty []string", got["ip_addrs"])
	}
	if got["exception"] != false {
		t.Errorf("exception = %v, want false", got["exception"])
	}
}

// TestUnitConstructOnlyChangesServerGroupID guards against a future edit
// adding a field this resource does not own to the fields it actually sends.
func TestUnitConstructOnlyChangesServerGroupID(t *testing.T) {
	current := apiDevice{
		ID: 1, ServerGroupID: 100, Exception: false,
		Tags: []string{"a"}, CustomName: "c", IPAddrs: []string{"1.2.3.4"},
	}

	got := construct(current, 999)

	wantKeys := map[string]bool{
		"server_group_id": true, "exception": true, "tags": true,
		"custom_name": true, "ip_addrs": true,
	}
	if len(got) != len(wantKeys) {
		t.Fatalf("construct sent %d fields %#v, want exactly %v", len(got), got, wantKeys)
	}
	for k := range got {
		if !wantKeys[k] {
			t.Errorf("construct sent unexpected field %q", k)
		}
	}
}

func TestUnitParseImportID(t *testing.T) {
	cases := []struct {
		raw     string
		want    int64
		wantErr bool
	}{
		{raw: "5818967", want: 5818967},
		{raw: "  242983  ", want: 242983},
		{raw: "", wantErr: true},
		{raw: "not-a-number", wantErr: true},
		{raw: "0", wantErr: true},
		{raw: "-1", wantErr: true},
	}

	for _, c := range cases {
		got, err := parseImportID(c.raw)
		if c.wantErr {
			if err == nil {
				t.Errorf("parseImportID(%q) = %d, nil, want an error", c.raw, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseImportID(%q) returned error: %v", c.raw, err)
			continue
		}
		if got != c.want {
			t.Errorf("parseImportID(%q) = %d, want %d", c.raw, got, c.want)
		}
	}
}

func TestUnitFlatten(t *testing.T) {
	api := apiDevice{ID: 5818967, ServerGroupID: 242983}

	got := flatten(api, 183625)

	if got.DeviceID.ValueInt64() != 5818967 {
		t.Errorf("DeviceID = %d, want 5818967", got.DeviceID.ValueInt64())
	}
	if got.ServerGroupID.ValueInt64() != 242983 {
		t.Errorf("ServerGroupID = %d, want 242983", got.ServerGroupID.ValueInt64())
	}
	if got.OrganizationID.ValueInt64() != 183625 {
		t.Errorf("OrganizationID = %d, want 183625", got.OrganizationID.ValueInt64())
	}
}
