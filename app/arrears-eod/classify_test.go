package main

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

// §7.4's bucket table, boundary by boundary. Getting 30/31, 60/61 or 90/91 wrong
// puts a loan in the wrong collections queue.
func TestClassifyBoundaries(t *testing.T) {
	cases := []struct {
		dpd  int
		want string
	}{
		{0, BucketCurrent},
		{1, BucketDPD130},
		{30, BucketDPD130},
		{31, BucketDPD3160},
		{60, BucketDPD3160},
		{61, BucketDPD6190},
		{90, BucketDPD6190},
		{91, BucketNPL90Plus},
		{365, BucketNPL90Plus},
		// the four the seed produces
		{12, BucketDPD130},
		{47, BucketDPD3160},
		{78, BucketDPD6190},
		{124, BucketNPL90Plus},
	}
	for _, c := range cases {
		if got := Classify(c.dpd); got != c.want {
			t.Errorf("Classify(%d) = %s, want %s", c.dpd, got, c.want)
		}
	}
}

// A loan whose first instalment is still in the future is CURRENT, never
// negative days late. This is what makes APP-100244 CURRENT on the day it is
// disbursed (§7.4).
func TestDaysPastDueNeverNegative(t *testing.T) {
	asOf := time.Date(2026, 9, 12, 0, 0, 0, 0, time.UTC)

	if got := DaysPastDue(nil, asOf); got != 0 {
		t.Errorf("nil oldest-unpaid gave %d, want 0", got)
	}

	future := asOf.AddDate(0, 1, 0)
	if got := DaysPastDue(&future, asOf); got != 0 {
		t.Errorf("future due date gave %d, want 0", got)
	}

	past := asOf.AddDate(0, 0, -47)
	if got := DaysPastDue(&past, asOf); got != 47 {
		t.Errorf("47 days ago gave %d, want 47", got)
	}
}

func TestBankActionPerBucket(t *testing.T) {
	cases := map[string]string{
		BucketCurrent:   "none",
		BucketDPD130:    "SMS reminder",
		BucketDPD3160:   "collections call",
		BucketDPD6190:   "demand letter",
		BucketNPL90Plus: "non-performing: provision and report",
	}
	for bucket, want := range cases {
		if got := BankAction(bucket); got != want {
			t.Errorf("BankAction(%s) = %q, want %q", bucket, got, want)
		}
	}
}

// §7.4: AS_OF_DATE is optional and defaults to today; the smoke test leaves it
// unset (§10.6).
func TestParseAsOfDate(t *testing.T) {
	now := time.Date(2026, 9, 12, 14, 30, 0, 0, time.UTC)

	got, err := ParseAsOfDate("", now)
	if err != nil {
		t.Fatalf("empty AS_OF_DATE errored: %v", err)
	}
	if !got.Equal(time.Date(2026, 9, 12, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("empty gave %v, want today at midnight", got)
	}

	got, err = ParseAsOfDate("2026-01-15", now)
	if err != nil {
		t.Fatalf("valid AS_OF_DATE errored: %v", err)
	}
	if !got.Equal(time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("parsed %v, want 2026-01-15", got)
	}

	for _, bad := range []string{"15/01/2026", "2026-13-01", "yesterday", "2026-1-5x"} {
		if _, err := ParseAsOfDate(bad, now); err == nil {
			t.Errorf("AS_OF_DATE %q should have been rejected", bad)
		} else if !strings.Contains(err.Error(), "AS_OF_DATE") {
			t.Errorf("error for %q does not name the variable: %v", bad, err)
		}
	}
}

// --- report -----------------------------------------------------------------

func TestSortWorstFirst(t *testing.T) {
	rows := []Classification{
		{ApplicationID: "APP-100244", DaysPastDue: 0, Bucket: BucketCurrent},
		{ApplicationID: "APP-100301", DaysPastDue: 12, Bucket: BucketDPD130},
		{ApplicationID: "APP-100304", DaysPastDue: 124, Bucket: BucketNPL90Plus},
		{ApplicationID: "APP-100302", DaysPastDue: 47, Bucket: BucketDPD3160},
		{ApplicationID: "APP-100303", DaysPastDue: 78, Bucket: BucketDPD6190},
	}
	SortWorstFirst(rows)

	want := []string{"APP-100304", "APP-100303", "APP-100302", "APP-100301", "APP-100244"}
	for i, id := range want {
		if rows[i].ApplicationID != id {
			t.Errorf("position %d = %s, want %s", i, rows[i].ApplicationID, id)
		}
	}
}

func TestFormatMoney(t *testing.T) {
	cases := map[float64]string{
		0:          "0.00",
		0.5:        "0.50",
		1234.5:     "1,234.50",
		136000:     "136,000.00",
		106200:     "106,200.00",
		269166.65:  "269,166.65",
		616000.02:  "616,000.02",
		340000:     "340,000.00",
		1234567.89: "1,234,567.89",
	}
	for in, want := range cases {
		if got := FormatMoney(in); got != want {
			t.Errorf("FormatMoney(%v) = %q, want %q", in, got, want)
		}
	}
}

// The table is what goes on the projector, so its shape is part of the contract.
func TestReportMatchesSpecLayout(t *testing.T) {
	rows := []Classification{
		{"APP-100304", "Halima S.", 616000.02, 124, BucketNPL90Plus},
		{"APP-100303", "Peter O.", 106200.00, 78, BucketDPD6190},
		{"APP-100302", "Grace N.", 269166.65, 47, BucketDPD3160},
		{"APP-100301", "Joseph M.", 136000.00, 12, BucketDPD130},
		{"APP-100244", "Amina W.", 340000.00, 0, BucketCurrent},
	}
	var buf bytes.Buffer
	WriteReport(&buf, rows, time.Date(2026, 9, 12, 0, 0, 0, 0, time.UTC), 340*time.Millisecond)
	out := buf.String()

	for _, want := range []string{
		"=== Kifaru Bank — End of Day Arrears Classification ===",
		"As of: 2026-09-12        Loans assessed: 5",
		"APPLICATION",
		"616,000.02",
		"NPL_90_PLUS",
		// exactly one loan is non-performing, and the exposure is its outstanding
		"Non-performing exposure: KES 616,000.02 (1 loan)",
		"Completed in 340ms",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("report is missing %q\n---\n%s", want, out)
		}
	}

	// worst bucket first
	npl := strings.Index(out, "NPL_90_PLUS")
	current := strings.Index(out, "CURRENT")
	if npl > current {
		t.Error("NPL_90_PLUS should be printed before CURRENT")
	}
}

func TestReportPluralisesLoans(t *testing.T) {
	var buf bytes.Buffer
	WriteReport(&buf, []Classification{
		{"APP-1", "A", 100, 100, BucketNPL90Plus},
		{"APP-2", "B", 200, 120, BucketNPL90Plus},
	}, time.Now(), time.Millisecond)

	if !strings.Contains(buf.String(), "(2 loans)") {
		t.Errorf("expected '(2 loans)', got:\n%s", buf.String())
	}
}

func TestReportHandlesNoNonPerformingLoans(t *testing.T) {
	var buf bytes.Buffer
	WriteReport(&buf, []Classification{
		{"APP-1", "A", 100, 0, BucketCurrent},
	}, time.Now(), time.Millisecond)

	if !strings.Contains(buf.String(), "Non-performing exposure: KES 0.00 (0 loans)") {
		t.Errorf("expected a zero exposure line, got:\n%s", buf.String())
	}
}
