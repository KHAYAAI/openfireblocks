package main

import (
	"testing"
	"time"
)

// When the next period ends.
//
// Small arithmetic with a customer-visible consequence: get it wrong and
// either somebody is billed twice in a month or a month goes unbilled, and
// both are found by the customer rather than by us.

func TestAMonthlyPeriodAdvancesByACalendarMonth(t *testing.T) {
	start := time.Date(2026, time.March, 15, 9, 0, 0, 0, time.UTC)

	end := NextPeriodEnd(start, "monthly")

	want := time.Date(2026, time.April, 15, 9, 0, 0, 0, time.UTC)
	if !end.Equal(want) {
		t.Errorf("a period from %s ends %s, want %s", start, end, want)
	}
}

// Not 30 days. A customer on a monthly plan expects the same date each
// month; 30-day periods walk the billing date backwards through the
// calendar until it means nothing.
func TestAMonthlyPeriodIsNotThirtyDays(t *testing.T) {
	start := time.Date(2026, time.January, 10, 0, 0, 0, 0, time.UTC)

	end := NextPeriodEnd(start, "monthly")

	if days := end.Sub(start).Hours() / 24; days != 31 {
		t.Errorf("January to February measured %v days; a calendar month was expected", days)
	}
	if end.Day() != start.Day() {
		t.Errorf("the billing date moved from the %dth to the %dth", start.Day(), end.Day())
	}
}

func TestAYearlyPeriodAdvancesByAYear(t *testing.T) {
	start := time.Date(2026, time.June, 1, 0, 0, 0, 0, time.UTC)

	end := NextPeriodEnd(start, "yearly")

	want := time.Date(2027, time.June, 1, 0, 0, 0, 0, time.UTC)
	if !end.Equal(want) {
		t.Errorf("a yearly period from %s ends %s, want %s", start, end, want)
	}
}

// An unrecognised cycle bills monthly rather than never. The alternative
// is a zero-length period, which would make the subscription due for
// billing on every sweep forever.
func TestAnUnknownCycleFallsBackToMonthly(t *testing.T) {
	start := time.Date(2026, time.June, 1, 0, 0, 0, 0, time.UTC)

	end := NextPeriodEnd(start, "fortnightly")

	if !end.After(start) {
		t.Fatalf("an unknown billing cycle produced a period ending %s, at or before its start %s", end, start)
	}
	if !end.Equal(NextPeriodEnd(start, "monthly")) {
		t.Error("an unknown billing cycle did not fall back to monthly")
	}
}

// The advance always moves forward, for every cycle and every start date
// in a year. A period that fails to advance leaves the subscription
// permanently due, and a nightly sweep would raise an invoice attempt for
// it every single night.
func TestEveryPeriodAdvancesStrictlyForward(t *testing.T) {
	for _, cycle := range []string{"monthly", "yearly"} {
		start := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
		for day := 0; day < 366; day++ {
			at := start.AddDate(0, 0, day)
			if end := NextPeriodEnd(at, cycle); !end.After(at) {
				t.Fatalf("%s period starting %s ends %s, which is not after it", cycle, at, end)
			}
		}
	}
}
