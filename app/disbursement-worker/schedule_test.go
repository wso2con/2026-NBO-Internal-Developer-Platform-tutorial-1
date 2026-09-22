package main

import (
	"testing"
	"time"
)

// §5's flat-interest rule, cross-checked against the figures the seed data and
// the golden fixtures already depend on.
func TestTotalRepayable(t *testing.T) {
	cases := []struct {
		name      string
		amountKES float64
		term      int
		wantKES   float64
	}{
		// 250,000 x (1 + 0.18 x 24/12) = 250,000 x 1.36  — APP-100244
		{"APP-100244 250k/24mo", 250000, 24, 340000},
		// 300,000 x 1.36 — APP-100245
		{"APP-100245 300k/24mo", 300000, 24, 408000},
		// 150,000 x 1.36 — seeded APP-100301
		{"seed 150k/24mo", 150000, 24, 204000},
		// 120,000 x (1 + 0.18 x 12/12) = 120,000 x 1.18 — seeded APP-100303
		{"seed 120k/12mo", 120000, 12, 141600},
		// 480,000 x (1 + 0.18 x 36/12) = 480,000 x 1.54 — seeded APP-100304
		{"seed 480k/36mo", 480000, 36, 739200},
		// 6-month term: 100,000 x (1 + 0.18 x 0.5) = 100,000 x 1.09
		{"100k/6mo", 100000, 6, 109000},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := TotalRepayableCents(ToCents(c.amountKES), c.term)
			if want := ToCents(c.wantKES); got != want {
				t.Errorf("total = %d cents (%.2f), want %d (%.2f)",
					got, FromCents(got), want, c.wantKES)
			}
		})
	}
}

// The schedule must sum to EXACTLY the total repayable — no cent lost to
// rounding. This is the property most likely to break quietly.
func TestScheduleSumsToTotalExactly(t *testing.T) {
	disbursed := time.Date(2026, 9, 11, 0, 0, 0, 0, time.UTC)
	for _, amount := range []float64{10000, 96750, 100000, 120000, 150000, 250000, 300000, 480000, 1999999.99} {
		for _, term := range []int{6, 12, 24, 36} {
			total := TotalRepayableCents(ToCents(amount), term)
			schedule, err := BuildSchedule(ToCents(amount), term, disbursed)
			if err != nil {
				t.Fatalf("BuildSchedule(%v, %d) error = %v", amount, term, err)
			}
			var sum int64
			for _, i := range schedule {
				sum += i.AmountKES
			}
			if sum != total {
				t.Errorf("amount %.2f term %d: schedule sums to %d, total is %d (off by %d cents)",
					amount, term, sum, total, sum-total)
			}
		}
	}
}

// APP-100244: 340,000 over 24 = 14,166.666... -> 14,166.67 per instalment, with
// the remainder on the last. These are the numbers §6 and the seed both use.
func TestScheduleForApp100244(t *testing.T) {
	disbursed := time.Date(2026, 9, 11, 0, 0, 0, 0, time.UTC)
	schedule, err := BuildSchedule(ToCents(250000), 24, disbursed)
	if err != nil {
		t.Fatalf("BuildSchedule error = %v", err)
	}

	if len(schedule) != 24 {
		t.Fatalf("len = %d, want 24", len(schedule))
	}
	if got := FromCents(schedule[0].AmountKES); got != 14166.66 && got != 14166.67 {
		t.Errorf("instalment 1 = %.2f, want ~14166.67", got)
	}
	// First instalment falls one month after disbursement (§5).
	wantFirstDue := time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC)
	if !schedule[0].DueDate.Equal(wantFirstDue) {
		t.Errorf("first due = %v, want %v", schedule[0].DueDate, wantFirstDue)
	}
	// Last instalment 24 months out.
	wantLastDue := time.Date(2028, 9, 11, 0, 0, 0, 0, time.UTC)
	if !schedule[23].DueDate.Equal(wantLastDue) {
		t.Errorf("last due = %v, want %v", schedule[23].DueDate, wantLastDue)
	}
}

// A loan disbursed today cannot be overdue — the reason APP-100244 classifies as
// CURRENT in §7.4 rather than appearing in the collections queue.
func TestFirstInstalmentIsNeverDueOnTheDayOfDisbursement(t *testing.T) {
	today := time.Now().UTC().Truncate(24 * time.Hour)
	schedule, err := BuildSchedule(ToCents(250000), 24, today)
	if err != nil {
		t.Fatalf("BuildSchedule error = %v", err)
	}
	if !schedule[0].DueDate.After(today) {
		t.Errorf("first instalment due %v is not after disbursement %v",
			schedule[0].DueDate, today)
	}
}

func TestScheduleDueDatesAreMonthlyAndAscending(t *testing.T) {
	disbursed := time.Date(2026, 1, 31, 0, 0, 0, 0, time.UTC)
	schedule, err := BuildSchedule(ToCents(120000), 12, disbursed)
	if err != nil {
		t.Fatalf("BuildSchedule error = %v", err)
	}
	for i := 1; i < len(schedule); i++ {
		if !schedule[i].DueDate.After(schedule[i-1].DueDate) {
			t.Errorf("instalment %d due %v is not after instalment %d due %v",
				i+1, schedule[i].DueDate, i, schedule[i-1].DueDate)
		}
	}
	if schedule[0].Number != 1 || schedule[11].Number != 12 {
		t.Error("instalment numbers should run 1..12")
	}
}

func TestScheduleRejectsBadInput(t *testing.T) {
	d := time.Now()
	if _, err := BuildSchedule(ToCents(250000), 0, d); err == nil {
		t.Error("term 0 should be rejected")
	}
	if _, err := BuildSchedule(0, 24, d); err == nil {
		t.Error("amount 0 should be rejected")
	}
}

func TestCentsRoundTrip(t *testing.T) {
	for _, kes := range []float64{0.01, 1.005, 14166.67, 250000, 739200} {
		if got := FromCents(ToCents(kes)); got != roundTo2(kes) {
			t.Errorf("round trip of %.2f gave %.2f", kes, got)
		}
	}
}

func roundTo2(v float64) float64 {
	return float64(int64(v*100+0.5)) / 100
}
