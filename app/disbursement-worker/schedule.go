// schedule.go — repayment schedule generation (BUILD-SPEC.md §5).
//
// Flat interest at 18% per annum:
//
//	total repayable = amount x (1 + 0.18 x term_months/12)
//
// divided evenly across term_months instalments, first due one month after
// disbursement, rounded to 2dp with any remainder on the FINAL instalment.
//
// Money is handled in integer cents throughout. Using float64 for currency and
// then rounding at the end is how schedules end up a cent short of their total,
// which a banking audience will notice on the projector.
package main

import (
	"fmt"
	"time"
)

// AnnualFlatRateBps is 18% per annum expressed in basis points, so the arithmetic
// stays in integers (§5).
const AnnualFlatRateBps = 1800

// Instalment is one row of the repayment schedule.
type Instalment struct {
	Number    int
	DueDate   time.Time
	AmountKES int64 // cents
}

// TotalRepayableCents returns amount x (1 + 0.18 x term/12) in cents.
//
// The multiplication is done before the division so the only rounding happens
// once, at the end.
func TotalRepayableCents(amountCents int64, termMonths int) int64 {
	// amount * (10000 + 1800*term/12) / 10000, with rounding half-up
	numerator := amountCents * (int64(120000) + int64(AnnualFlatRateBps)*int64(termMonths))
	const denominator = 120000
	return (numerator + denominator/2) / denominator
}

// BuildSchedule returns the full schedule for a disbursement.
//
// Instalments 1..n-1 are the rounded even share; the last absorbs the remainder,
// so the schedule always sums to exactly the total repayable (§5).
func BuildSchedule(amountCents int64, termMonths int, disbursedOn time.Time) ([]Instalment, error) {
	if termMonths <= 0 {
		return nil, fmt.Errorf("term_months must be positive, got %d", termMonths)
	}
	if amountCents <= 0 {
		return nil, fmt.Errorf("amount must be positive, got %d cents", amountCents)
	}

	total := TotalRepayableCents(amountCents, termMonths)
	base := total / int64(termMonths)
	// Whatever the even split loses to truncation lands on the final instalment.
	remainder := total - base*int64(termMonths)

	schedule := make([]Instalment, 0, termMonths)
	for n := 1; n <= termMonths; n++ {
		amount := base
		if n == termMonths {
			amount += remainder
		}
		schedule = append(schedule, Instalment{
			Number: n,
			// First instalment falls due one month AFTER disbursement (§5), which
			// is why a loan disbursed today classifies as CURRENT rather than
			// overdue (§7.4).
			DueDate:   disbursedOn.AddDate(0, n, 0),
			AmountKES: amount,
		})
	}
	return schedule, nil
}

// Helpers for moving between the KES decimals the API speaks and the integer
// cents used here.

func ToCents(kes float64) int64 {
	if kes < 0 {
		return -int64(-kes*100 + 0.5)
	}
	return int64(kes*100 + 0.5)
}

func FromCents(cents int64) float64 {
	return float64(cents) / 100
}
