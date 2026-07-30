// Package schedule encodes and decodes the Automox policy schedule bitmasks.
//
// Automox stores a policy's schedule as three integers whose bits select days of
// the week, weeks of the month, and months of the year. The API reference calls
// these the "decimal value of binary day/week/month schedule" and documents no bit
// order at all.
//
// The orders below were established empirically against the live API (org 120547,
// 2026-07-30) by cross-checking every policy's mask against the API-computed
// next_remediation date: a remediation must fall on a day whose bit is set.
//
//	schedule_days            bit 1 = Monday  … bit 7 = Sunday    (18/18 policies, 0 mismatches)
//	schedule_months          bit 1 = January … bit 12 = December
//	schedule_weeks_of_month  bit 1 = week 1  … bit 5 = week 5    (10/11 policies — see below)
//
// Bit 0 is unused in all three. Sanity anchors: 254 (0b11111110) is all seven days,
// 8190 is all twelve months, 62 is all five weeks.
//
// An earlier reading of bit 1 as Sunday was disproved: days=4 resolves to Tuesday
// and days=16 to Thursday, neither consistent with a Sunday-first order.
//
// Weeks of the month are deliberately NOT exposed in named form. One policy
// contradicts the ordering — a single-month policy with only bit 2 set whose
// next_remediation (2027-03-05) falls in week 1 — and the cause is unresolved.
// Shipping a friendly form on an unverified ordering risks silently shifting when
// patching runs across production endpoints, so callers handle the raw integer
// until a round-trip settles it.
package schedule

import (
	"fmt"
	"sort"
	"strings"
)

// AllDays is the mask selecting every day of the week (bits 1-7).
const AllDays = 254

// AllMonths is the mask selecting every month of the year (bits 1-12).
const AllMonths = 8190

// AllWeeks is the mask selecting every week of the month (bits 1-5).
const AllWeeks = 62

// Never is the mask selecting nothing. A policy scheduled with Never cannot run,
// which is what the acceptance-test fixtures rely on.
const Never = 0

// dayNames maps bit position to day name. Index 0 is unused.
var dayNames = [...]string{"", "monday", "tuesday", "wednesday", "thursday", "friday", "saturday", "sunday"}

// monthNames maps bit position to month name. Index 0 is unused.
var monthNames = [...]string{
	"", "january", "february", "march", "april", "may", "june",
	"july", "august", "september", "october", "november", "december",
}

// DayNames returns the accepted day names in schedule order (Monday first).
func DayNames() []string { return append([]string(nil), dayNames[1:]...) }

// MonthNames returns the accepted month names in calendar order.
func MonthNames() []string { return append([]string(nil), monthNames[1:]...) }

// EncodeDays converts day names to the schedule_days bitmask. Names are matched
// case-insensitively. A duplicate name is an error rather than a silent no-op,
// because a duplicate usually means the caller generated the list programmatically
// and has a bug worth surfacing.
func EncodeDays(names []string) (int64, error) {
	return encode(names, dayNames[:], "day")
}

// DecodeDays converts a schedule_days bitmask to day names in schedule order.
func DecodeDays(mask int64) ([]string, error) {
	return decode(mask, dayNames[:], "day")
}

// EncodeMonths converts month names to the schedule_months bitmask.
func EncodeMonths(names []string) (int64, error) {
	return encode(names, monthNames[:], "month")
}

// DecodeMonths converts a schedule_months bitmask to month names in calendar order.
func DecodeMonths(mask int64) ([]string, error) {
	return decode(mask, monthNames[:], "month")
}

func encode(names []string, table []string, kind string) (int64, error) {
	var mask int64
	seen := make(map[string]bool, len(names))

	for _, raw := range names {
		name := strings.ToLower(strings.TrimSpace(raw))
		if name == "" {
			return 0, fmt.Errorf("empty %s name in schedule", kind)
		}
		if seen[name] {
			return 0, fmt.Errorf("%s %q appears more than once in schedule", kind, name)
		}
		seen[name] = true

		bit := indexOf(table, name)
		if bit < 1 {
			return 0, fmt.Errorf("unknown %s %q; valid values are %s",
				kind, raw, strings.Join(table[1:], ", "))
		}
		mask |= 1 << uint(bit)
	}

	return mask, nil
}

func decode(mask int64, table []string, kind string) ([]string, error) {
	if mask < 0 {
		return nil, fmt.Errorf("%s schedule mask %d is negative", kind, mask)
	}

	// Bit 0 is unused by the API. Its presence means the value did not come from
	// Automox, so report it instead of quietly dropping it.
	if mask&1 != 0 {
		return nil, fmt.Errorf(
			"%s schedule mask %d sets bit 0, which Automox does not use; masks are 1-indexed",
			kind, mask)
	}

	maxBit := int64(len(table) - 1)
	if high := mask >> uint(maxBit+1); high != 0 {
		return nil, fmt.Errorf(
			"%s schedule mask %d sets bits above %d, which Automox does not define",
			kind, mask, maxBit)
	}

	var out []string
	for bit := 1; bit <= int(maxBit); bit++ {
		if mask&(1<<uint(bit)) != 0 {
			out = append(out, table[bit])
		}
	}

	return out, nil
}

func indexOf(table []string, name string) int {
	for i := 1; i < len(table); i++ {
		if table[i] == name {
			return i
		}
	}
	return -1
}

// NormalizeNames lower-cases and sorts names into schedule order, so that a
// practitioner writing ["Friday","monday"] and the provider reading back
// ["monday","friday"] do not produce a spurious diff.
func NormalizeNames(names []string, table []string) []string {
	out := make([]string, 0, len(names))
	for _, n := range names {
		out = append(out, strings.ToLower(strings.TrimSpace(n)))
	}
	sort.SliceStable(out, func(i, j int) bool {
		return indexOf(table, out[i]) < indexOf(table, out[j])
	})
	return out
}

// DayTable and MonthTable expose the lookup tables for NormalizeNames.
func DayTable() []string   { return dayNames[:] }
func MonthTable() []string { return monthNames[:] }
