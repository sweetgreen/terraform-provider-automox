package schedule_test

import (
	"strings"
	"testing"

	"github.com/sweetgreen/terraform-provider-automox/internal/schedule"
)

// The cases below are not invented. Each mask was read from a live Automox policy
// in org 120547 on 2026-07-30, and the day it resolves to was confirmed against
// that policy's API-computed next_remediation date. A regression here silently
// changes when patching runs on production endpoints, which is why these are
// pinned as literals rather than derived.
func TestUnitDecodeDays_LiveVerifiedAnchors(t *testing.T) {
	cases := []struct {
		name string
		mask int64
		want []string
		note string
	}{
		{
			name: "all seven days",
			mask: 254, // 0b11111110
			want: []string{"monday", "tuesday", "wednesday", "thursday", "friday", "saturday", "sunday"},
			note: "most common live value; next_remediation 2026-07-30 (Thu)",
		},
		{
			name: "tuesday only",
			mask: 4,
			want: []string{"tuesday"},
			note: "patch-tuesday policies; next_remediation 2026-08-11, the 2nd Tuesday of Aug 2026",
		},
		{
			name: "thursday only",
			mask: 16,
			want: []string{"thursday"},
			note: "next_remediation 2026-07-30 (Thu)",
		},
		{
			name: "thursday and friday",
			mask: 48,
			want: []string{"thursday", "friday"},
			note: "next_remediation 2026-07-30 (Thu)",
		},
		{
			name: "wednesday through saturday",
			mask: 120,
			want: []string{"wednesday", "thursday", "friday", "saturday"},
			note: "next_remediation 2027-03-05 (Fri)",
		},
		{
			name: "never",
			mask: 0,
			want: nil,
			note: "used by acceptance fixtures so a test policy cannot execute",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := schedule.DecodeDays(tc.mask)
			if err != nil {
				t.Fatalf("DecodeDays(%d) returned error: %v", tc.mask, err)
			}
			if !equal(got, tc.want) {
				t.Errorf("DecodeDays(%d) = %v, want %v\n  live evidence: %s", tc.mask, got, tc.want, tc.note)
			}
		})
	}
}

// Guards against the specific misreading that was disproved during research: an
// initial hypothesis of bit 1 = Sunday. If someone "fixes" the table by shifting
// it, these assertions fail.
func TestUnitDecodeDays_RejectsSundayFirstOrdering(t *testing.T) {
	got, err := schedule.DecodeDays(4)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected exactly one day, got %v", got)
	}
	if got[0] == "monday" {
		t.Fatal("mask 4 decoded to Monday, which means bit 1 was treated as Sunday; " +
			"live policies with mask 4 remediate on Tuesday (next_remediation 2026-08-11)")
	}
	if got[0] != "tuesday" {
		t.Errorf("mask 4 decoded to %q, want tuesday", got[0])
	}
}

func TestUnitDecodeMonths_LiveVerifiedAnchors(t *testing.T) {
	all, err := schedule.DecodeMonths(8190)
	if err != nil {
		t.Fatalf("DecodeMonths(8190) returned error: %v", err)
	}
	if len(all) != 12 {
		t.Errorf("DecodeMonths(8190) returned %d months, want all 12: %v", len(all), all)
	}
	if all[0] != "january" || all[11] != "december" {
		t.Errorf("DecodeMonths(8190) bounds = %q..%q, want january..december", all[0], all[11])
	}

	// months=8 is bit 3. The live policy carrying it has next_remediation
	// 2027-03-05, i.e. March, which fixes bit 1 as January.
	march, err := schedule.DecodeMonths(8)
	if err != nil {
		t.Fatalf("DecodeMonths(8) returned error: %v", err)
	}
	if !equal(march, []string{"march"}) {
		t.Errorf("DecodeMonths(8) = %v, want [march]; live next_remediation was 2027-03-05", march)
	}
}

func TestUnitEncodeDays_RoundTrip(t *testing.T) {
	for _, mask := range []int64{0, 4, 16, 48, 120, 254} {
		names, err := schedule.DecodeDays(mask)
		if err != nil {
			t.Fatalf("DecodeDays(%d): %v", mask, err)
		}
		back, err := schedule.EncodeDays(names)
		if err != nil {
			t.Fatalf("EncodeDays(%v): %v", names, err)
		}
		if back != mask {
			t.Errorf("round trip of %d produced %d via %v", mask, back, names)
		}
	}
}

func TestUnitEncodeDays_CaseAndWhitespaceInsensitive(t *testing.T) {
	got, err := schedule.EncodeDays([]string{"  Monday ", "TUESDAY"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := int64(1<<1 | 1<<2)
	if got != want {
		t.Errorf("EncodeDays = %d, want %d", got, want)
	}
}

func TestUnitEncodeDays_RejectsBadInput(t *testing.T) {
	cases := []struct {
		name   string
		input  []string
		expect string
	}{
		{"unknown name", []string{"funday"}, "unknown day"},
		{"duplicate", []string{"monday", "Monday"}, "more than once"},
		{"empty", []string{""}, "empty day name"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := schedule.EncodeDays(tc.input)
			if err == nil {
				t.Fatalf("EncodeDays(%v) succeeded, want an error", tc.input)
			}
			if !strings.Contains(err.Error(), tc.expect) {
				t.Errorf("error %q does not mention %q", err, tc.expect)
			}
		})
	}
}

// Bit 0 is unused by Automox. A mask that sets it did not come from the API, so it
// must surface rather than be silently truncated — Constitution V.
func TestUnitDecode_RejectsUnusedBitZero(t *testing.T) {
	if _, err := schedule.DecodeDays(255); err == nil {
		t.Error("DecodeDays(255) succeeded; bit 0 is undefined and must be reported")
	} else if !strings.Contains(err.Error(), "bit 0") {
		t.Errorf("error %q should name bit 0", err)
	}

	if _, err := schedule.DecodeMonths(8191); err == nil {
		t.Error("DecodeMonths(8191) succeeded; bit 0 is undefined and must be reported")
	}
}

func TestUnitDecode_RejectsOutOfRangeBits(t *testing.T) {
	// Bit 8 is beyond the seven days Automox defines.
	if _, err := schedule.DecodeDays(1 << 8); err == nil {
		t.Error("DecodeDays(256) succeeded; bit 8 exceeds the defined day range")
	}
	// Bit 13 is beyond the twelve months.
	if _, err := schedule.DecodeMonths(1 << 13); err == nil {
		t.Error("DecodeMonths(8192) succeeded; bit 13 exceeds the defined month range")
	}
}

func TestUnitConstantsMatchDecodedBreadth(t *testing.T) {
	days, err := schedule.DecodeDays(schedule.AllDays)
	if err != nil || len(days) != 7 {
		t.Errorf("AllDays should decode to 7 days, got %v (err %v)", days, err)
	}
	months, err := schedule.DecodeMonths(schedule.AllMonths)
	if err != nil || len(months) != 12 {
		t.Errorf("AllMonths should decode to 12 months, got %v (err %v)", months, err)
	}
	if schedule.Never != 0 {
		t.Errorf("Never = %d, want 0; fixtures depend on it meaning 'cannot run'", schedule.Never)
	}
}

func TestUnitNormalizeNames_SortsIntoScheduleOrder(t *testing.T) {
	got := schedule.NormalizeNames([]string{"Friday", "monday", " WEDNESDAY "}, schedule.DayTable())
	want := []string{"monday", "wednesday", "friday"}
	if !equal(got, want) {
		t.Errorf("NormalizeNames = %v, want %v", got, want)
	}
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
