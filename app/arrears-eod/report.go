// report.go — the summary table (BUILD-SPEC.md §7.4).
//
// The presenter shows this on screen, so it is formatted for a projector:
// thousands separators, aligned columns, worst bucket first. The JSON log lines
// are the machine-readable version; this table is a human-readable extra, not a
// replacement.
package main

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
)

// Classification is one assessed loan.
type Classification struct {
	ApplicationID  string
	ApplicantName  string
	OutstandingKES float64
	DaysPastDue    int
	Bucket         string
}

// Column layout, chosen so the widest realistic values still line up:
// a 14-char application id, a 19-char name, and amounts into the millions.
const (
	colApplication = 15
	colApplicant   = 20
	colOutstanding = 12
	colDPD         = 7
)

// SortWorstFirst orders by bucket severity, then by days past due descending
// within a bucket, so the most urgent case is always the top line.
func SortWorstFirst(rows []Classification) {
	sort.SliceStable(rows, func(i, j int) bool {
		bi, bj := BucketOrder[rows[i].Bucket], BucketOrder[rows[j].Bucket]
		if bi != bj {
			return bi < bj
		}
		if rows[i].DaysPastDue != rows[j].DaysPastDue {
			return rows[i].DaysPastDue > rows[j].DaysPastDue
		}
		return rows[i].ApplicationID < rows[j].ApplicationID
	})
}

// WriteReport prints the §7.4 table.
func WriteReport(w io.Writer, rows []Classification, asOf time.Time, elapsed time.Duration) {
	fmt.Fprintln(w, "=== Kifaru Bank — End of Day Arrears Classification ===")
	fmt.Fprintf(w, "As of: %s        Loans assessed: %d\n\n",
		asOf.Format("2006-01-02"), len(rows))

	fmt.Fprintf(w, "%-*s%-*s%*s%*s   %s\n",
		colApplication, "APPLICATION",
		colApplicant, "APPLICANT",
		colOutstanding, "OUTSTANDING",
		colDPD, "DPD",
		"BUCKET")

	for _, r := range rows {
		fmt.Fprintf(w, "%-*s%-*s%*s%*d   %s\n",
			colApplication, r.ApplicationID,
			colApplicant, truncate(r.ApplicantName, colApplicant-1),
			colOutstanding, FormatMoney(r.OutstandingKES),
			colDPD, r.DaysPastDue,
			r.Bucket)
	}

	// The number the room actually cares about: how much is non-performing.
	var nplTotal float64
	var nplCount int
	for _, r := range rows {
		if r.Bucket == BucketNPL90Plus {
			nplTotal += r.OutstandingKES
			nplCount++
		}
	}
	loanWord := "loans"
	if nplCount == 1 {
		loanWord = "loan"
	}
	fmt.Fprintf(w, "\nNon-performing exposure: KES %s (%d %s)\n",
		FormatMoney(nplTotal), nplCount, loanWord)
	fmt.Fprintf(w, "Completed in %dms\n", elapsed.Milliseconds())
}

// FormatMoney renders 616000.02 as "616,000.02" — always 2dp, comma-grouped.
func FormatMoney(v float64) string {
	neg := v < 0
	if neg {
		v = -v
	}
	whole := int64(v)
	cents := int64((v-float64(whole))*100 + 0.5)
	if cents == 100 { // rounding carried into the next shilling
		whole++
		cents = 0
	}

	digits := fmt.Sprintf("%d", whole)
	var grouped strings.Builder
	for i, d := range digits {
		if i > 0 && (len(digits)-i)%3 == 0 {
			grouped.WriteByte(',')
		}
		grouped.WriteRune(d)
	}

	out := fmt.Sprintf("%s.%02d", grouped.String(), cents)
	if neg {
		return "-" + out
	}
	return out
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	if max <= 1 {
		return s[:max]
	}
	return s[:max-1] + "…"
}
