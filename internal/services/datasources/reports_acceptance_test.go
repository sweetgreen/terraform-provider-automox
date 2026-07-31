package datasources_test

import (
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/sweetgreen/terraform-provider-automox/internal/acceptance"
)

// TestAccReports covers both report data sources.
//
// Reports are the one place the API returns an object rather than an array, and
// where device records are camelCase instead of snake_case. Both mistakes
// produce a decode that succeeds into zero values rather than failing, so these
// checks assert real content: a report whose totals are all zero and whose
// device list is empty is what a wrongly-tagged struct looks like.
func TestAccReports(t *testing.T) {
	acceptance.SkipUnlessAcceptance(t)
	acceptance.PreCheck(t)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acceptance.PreCheck(t) },
		ProtoV6ProviderFactories: protoV6(),
		Steps: []resource.TestStep{
			{
				Config: `
data "automox_needs_attention_report" "current" {}
data "automox_prepatch_report" "current" {}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					// The wrapper key was unwrapped: totals are populated.
					checkPositive("data.automox_needs_attention_report.current", "total"),
					checkPositive("data.automox_prepatch_report.current", "total"),
					resource.TestCheckResourceAttrSet("data.automox_needs_attention_report.current", "devices.#"),
					resource.TestCheckResourceAttrSet("data.automox_prepatch_report.current", "devices.#"),

					// camelCase device fields decoded. name and os_family come from
					// different conventions in the same record -- os_family is
					// snake_case even here -- so both are checked.
					checkFirstDeviceDecoded("data.automox_needs_attention_report.current"),
					checkFirstDeviceDecoded("data.automox_prepatch_report.current"),
					// The fields that are actually camelCase in the report. id, name and
					// os_family above are not, so on their own they prove nothing about
					// the tags this file had to get right.
					checkCamelCaseDecoded("data.automox_needs_attention_report.current",
						"group_id", "last_refresh_time"),
					checkCamelCaseDecoded("data.automox_prepatch_report.current",
						"group", "create_time"),

					// The severity breakdown must add up to something, not sit at zero.
					checkSeveritySumsPositive("data.automox_needs_attention_report.current"),

					// Nested collections decoded, including their camelCase fields.
					checkNestedDecoded("data.automox_needs_attention_report.current",
						"devices", "policies", "reason_for_fail"),
					checkNestedDecoded("data.automox_prepatch_report.current",
						"devices", "patches", "severity"),
					// Automox sends CVEs under a singular `cve` key but as a list, and
					// as null when there are none. Decoding it as a string fails the
					// whole report, so assert a populated list really does decode.
					checkSomePatchHasCVEs("data.automox_prepatch_report.current"),
				),
			},
		},
	})
}

func checkPositive(name, attr string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		attrs, err := attributesOf(s, name)
		if err != nil {
			return err
		}
		v, err := strconv.Atoi(attrs[attr])
		if err != nil {
			return fmt.Errorf("%s.%s is %q, not a number", name, attr, attrs[attr])
		}
		if v <= 0 {
			return fmt.Errorf("%s.%s is %d; a zero total usually means the report "+
				"wrapper key was not unwrapped", name, attr, v)
		}
		return nil
	}
}

// checkFirstDeviceDecoded proves the camelCase JSON tags are right. A device
// with an empty name is what a mis-tagged struct produces.
func checkFirstDeviceDecoded(name string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		attrs, err := attributesOf(s, name)
		if err != nil {
			return err
		}
		count, _ := strconv.Atoi(attrs["devices.#"])
		if count == 0 {
			return fmt.Errorf("%s returned no devices", name)
		}
		for _, attr := range []string{"id", "name", "os_family"} {
			key := "devices.0." + attr
			if attrs[key] == "" || attrs[key] == "0" {
				return fmt.Errorf("%s.%s is empty; the report's field naming differs "+
					"from /servers and the JSON tag may be wrong", name, key)
			}
		}
		return nil
	}
}

func checkSeveritySumsPositive(name string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		attrs, err := attributesOf(s, name)
		if err != nil {
			return err
		}
		sum := 0
		for _, sev := range []string{"critical", "high", "medium", "low", "none", "unknown", "no_known_cves"} {
			v, _ := strconv.Atoi(attrs[sev])
			sum += v
		}
		if sum == 0 {
			return fmt.Errorf("%s: every severity count is zero, so the breakdown did not decode", name)
		}
		return nil
	}
}

// checkNestedDecoded walks devices until it finds one with a non-empty nested
// collection, then asserts the named field on it is present.
//
// It searches rather than checking index 0 because not every device has an
// outstanding patch or a failing policy, and asserting on a device that has none
// would be a test that passes for the wrong reason.
func checkNestedDecoded(name, outer, inner, field string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		attrs, err := attributesOf(s, name)
		if err != nil {
			return err
		}

		count, _ := strconv.Atoi(attrs[outer+".#"])
		for i := 0; i < count; i++ {
			n, _ := strconv.Atoi(attrs[fmt.Sprintf("%s.%d.%s.#", outer, i, inner)])
			if n == 0 {
				continue
			}
			// Found one. Its identifying fields must have decoded.
			idKey := fmt.Sprintf("%s.%d.%s.0.id", outer, i, inner)
			if attrs[idKey] == "" || attrs[idKey] == "0" {
				return fmt.Errorf("%s.%s did not decode", name, idKey)
			}
			fieldKey := fmt.Sprintf("%s.%d.%s.0.%s", outer, i, inner, field)
			if _, ok := attrs[fieldKey]; !ok {
				return fmt.Errorf("%s.%s is absent; the nested camelCase tag may be wrong",
					name, fieldKey)
			}
			return nil
		}
		return fmt.Errorf("%s: no device had any %s, so the nested decode is untested", name, inner)
	}
}

// checkSomePatchHasCVEs finds a patch with a non-empty CVE list and checks the
// identifiers look like CVEs.
//
// Most patches have none, so this searches rather than checking index 0 -- an
// assertion on an empty list would pass whatever the element type decoded to.
func checkSomePatchHasCVEs(name string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		attrs, err := attributesOf(s, name)
		if err != nil {
			return err
		}

		devices, _ := strconv.Atoi(attrs["devices.#"])
		for i := 0; i < devices; i++ {
			patches, _ := strconv.Atoi(attrs[fmt.Sprintf("devices.%d.patches.#", i)])
			for j := 0; j < patches; j++ {
				n, _ := strconv.Atoi(attrs[fmt.Sprintf("devices.%d.patches.%d.cves.#", i, j)])
				if n == 0 {
					continue
				}
				first := attrs[fmt.Sprintf("devices.%d.patches.%d.cves.0", i, j)]
				if !strings.HasPrefix(first, "CVE-") {
					return fmt.Errorf("%s: first CVE is %q, which is not a CVE identifier", name, first)
				}
				return nil
			}
		}
		return fmt.Errorf("%s: no patch carried any CVEs, so the list decode is untested", name)
	}
}

// checkCamelCaseDecoded asserts fields the report spells in camelCase arrived
// populated.
//
// A wrong JSON tag does not fail the decode; it leaves the field at its zero
// value. Nothing else in this test would notice.
func checkCamelCaseDecoded(name string, fields ...string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		attrs, err := attributesOf(s, name)
		if err != nil {
			return err
		}
		count, _ := strconv.Atoi(attrs["devices.#"])
		for _, field := range fields {
			populated := false
			for i := 0; i < count; i++ {
				v := attrs[fmt.Sprintf("devices.%d.%s", i, field)]
				if v != "" && v != "0" {
					populated = true
					break
				}
			}
			if !populated {
				return fmt.Errorf("%s: no device has %s set across %d devices; "+
					"the report spells this field in camelCase and the tag is probably wrong",
					name, field, count)
			}
		}
		return nil
	}
}
