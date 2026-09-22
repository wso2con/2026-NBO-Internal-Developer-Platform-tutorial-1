// validation.go — request validation (BUILD-SPEC.md §7.1).
//
// Every rule is named in §7.1, and every message matches the Ballerina service
// word for word so the console and the tests cannot tell the two apart. Pure
// functions: unit-testable without a database or a listener.
package main

import (
	"fmt"
	"regexp"
	"strings"
)

const (
	minAmountKES = 10000.0
	maxAmountKES = 2000000.0
)

var (
	validTerms = []int{6, 12, 24, 36}
	// §7.1: msisdn matches ^254[17]\d{8}$.
	msisdnPattern = regexp.MustCompile(`^254[17][0-9]{8}$`)
)

func validateApplication(req ApplicationRequest) []FieldError {
	errs := []FieldError{}

	if strings.TrimSpace(req.ApplicantName) == "" {
		errs = append(errs, FieldError{Field: "applicant_name", Message: "must not be empty"})
	}
	if strings.TrimSpace(req.NationalID) == "" {
		errs = append(errs, FieldError{Field: "national_id", Message: "must not be empty"})
	}
	if !msisdnPattern.MatchString(req.WalletMSISDN) {
		errs = append(errs, FieldError{
			Field:   "wallet_msisdn",
			Message: "must match 254 followed by 1 or 7 and 8 digits",
		})
	}
	if req.AmountKES < minAmountKES || req.AmountKES > maxAmountKES {
		errs = append(errs, FieldError{
			Field:   "amount_kes",
			Message: fmt.Sprintf("must be between %.1f and %.1f", minAmountKES, maxAmountKES),
		})
	}
	if !termIsValid(req.TermMonths) {
		errs = append(errs, FieldError{Field: "term_months", Message: "must be one of 6, 12, 24, 36"})
	}
	if req.MonthlyIncome <= 0 {
		errs = append(errs, FieldError{Field: "monthly_income", Message: "must be greater than 0"})
	}
	if req.EmploymentMonths < 0 {
		errs = append(errs, FieldError{Field: "employment_months", Message: "must not be negative"})
	}
	if req.ExistingDefaults < 0 {
		errs = append(errs, FieldError{Field: "existing_defaults", Message: "must not be negative"})
	}

	return errs
}

func termIsValid(term int) bool {
	for _, t := range validTerms {
		if t == term {
			return true
		}
	}
	return false
}
