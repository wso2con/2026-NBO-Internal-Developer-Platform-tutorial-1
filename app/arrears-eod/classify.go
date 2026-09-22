// classify.go — arrears bucketing (BUILD-SPEC.md §7.4).
//
// Pure functions: given a days-past-due figure, which bucket and which bank
// action. No database, no clock — the clock is passed in as AS_OF_DATE so a past
// EOD run can be reproduced exactly.
package main

import (
	"fmt"
	"strings"
	"time"
)

// The five buckets, worst first. Order matters: the report prints in this order
// because it is the collections team's priority order (§7.4).
const (
	BucketNPL90Plus = "NPL_90_PLUS"
	BucketDPD6190   = "DPD_61_90"
	BucketDPD3160   = "DPD_31_60"
	BucketDPD130    = "DPD_1_30"
	BucketCurrent   = "CURRENT"
)

// BucketOrder ranks buckets worst-first for reporting.
var BucketOrder = map[string]int{
	BucketNPL90Plus: 0,
	BucketDPD6190:   1,
	BucketDPD3160:   2,
	BucketDPD130:    3,
	BucketCurrent:   4,
}

// Classify maps days past due to a bucket (§7.4).
//
//	0      CURRENT       no action
//	1-30   DPD_1_30      SMS reminder
//	31-60  DPD_31_60     collections call
//	61-90  DPD_61_90     demand letter
//	91+    NPL_90_PLUS   non-performing: provision and report
func Classify(daysPastDue int) string {
	switch {
	case daysPastDue <= 0:
		return BucketCurrent
	case daysPastDue <= 30:
		return BucketDPD130
	case daysPastDue <= 60:
		return BucketDPD3160
	case daysPastDue <= 90:
		return BucketDPD6190
	default:
		return BucketNPL90Plus
	}
}

// BankAction is the collections step each bucket implies (§7.4). Printed in the
// JSON log line so the portal can show it without duplicating the mapping.
func BankAction(bucket string) string {
	switch bucket {
	case BucketDPD130:
		return "SMS reminder"
	case BucketDPD3160:
		return "collections call"
	case BucketDPD6190:
		return "demand letter"
	case BucketNPL90Plus:
		return "non-performing: provision and report"
	default:
		return "none"
	}
}

// DaysPastDue is the whole-day gap between the oldest unpaid instalment and the
// as-of date. Nothing overdue gives 0, never a negative number: a loan whose
// first instalment is still in the future is CURRENT, not "minus 20 days late".
func DaysPastDue(oldestUnpaidDue *time.Time, asOf time.Time) int {
	if oldestUnpaidDue == nil {
		return 0
	}
	days := int(asOf.Sub(*oldestUnpaidDue).Hours() / 24)
	if days < 0 {
		return 0
	}
	return days
}

// ParseAsOfDate reads AS_OF_DATE. Empty means today (§7.4).
//
// It exists for two reasons: reproducing a past EOD run when investigating, and
// letting the presenter fast-forward the clock on stage to move a loan between
// buckets. The smoke test leaves it unset (§10.6).
func ParseAsOfDate(raw string, now time.Time) (time.Time, error) {
	if strings.TrimSpace(raw) == "" {
		return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC), nil
	}
	d, err := time.Parse("2006-01-02", strings.TrimSpace(raw))
	if err != nil {
		return time.Time{}, fmt.Errorf("AS_OF_DATE must be YYYY-MM-DD, got %q", raw)
	}
	return d, nil
}
