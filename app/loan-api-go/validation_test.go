// validation_test.go — the §7.1 rules, unit-tested without a database or a
// listener. Mirrors the Ballerina service's test suite case for case.
package main

import "testing"

func validRequest() ApplicationRequest {
	return ApplicationRequest{
		ApplicantName:    "Amina W.",
		NationalID:       "31234567",
		WalletMSISDN:     "254712345678",
		AmountKES:        240000,
		TermMonths:       24,
		MonthlyIncome:    85000,
		EmploymentMonths: 30,
		KYCVerified:      true,
	}
}

func TestValidRequestPasses(t *testing.T) {
	if errs := validateApplication(validRequest()); len(errs) != 0 {
		t.Fatalf("expected no errors, got %v", errs)
	}
}

func TestNamesMustNotBeEmpty(t *testing.T) {
	req := validRequest()
	req.ApplicantName = "   "
	req.NationalID = ""
	errs := validateApplication(req)
	if len(errs) != 2 {
		t.Fatalf("expected 2 errors, got %v", errs)
	}
}

func TestMsisdnPattern(t *testing.T) {
	cases := map[string]bool{
		"254712345678":  true,
		"254112345678":  true,
		"254812345678":  false, // third digit must be 1 or 7
		"25471234567":   false, // too short
		"2547123456789": false, // too long
		"+254712345678": false, // no plus
		"0712345678":    false,
		"":              false,
	}
	for msisdn, want := range cases {
		req := validRequest()
		req.WalletMSISDN = msisdn
		got := len(validateApplication(req)) == 0
		if got != want {
			t.Errorf("msisdn %q: valid=%v, want %v", msisdn, got, want)
		}
	}
}

func TestAmountBounds(t *testing.T) {
	cases := map[float64]bool{
		10000:   true,
		2000000: true,
		9999.99: false,
		2000001: false,
		0:       false,
		-1:      false,
	}
	for amount, want := range cases {
		req := validRequest()
		req.AmountKES = amount
		got := len(validateApplication(req)) == 0
		if got != want {
			t.Errorf("amount %v: valid=%v, want %v", amount, got, want)
		}
	}
}

func TestTermMustBeOneOfFour(t *testing.T) {
	for _, term := range []int{6, 12, 24, 36} {
		req := validRequest()
		req.TermMonths = term
		if errs := validateApplication(req); len(errs) != 0 {
			t.Errorf("term %d should be valid, got %v", term, errs)
		}
	}
	for _, term := range []int{0, 1, 18, 48, -6} {
		req := validRequest()
		req.TermMonths = term
		if errs := validateApplication(req); len(errs) == 0 {
			t.Errorf("term %d should be rejected", term)
		}
	}
}

func TestIncomeAndNegatives(t *testing.T) {
	req := validRequest()
	req.MonthlyIncome = 0
	req.EmploymentMonths = -1
	req.ExistingDefaults = -2
	errs := validateApplication(req)
	if len(errs) != 3 {
		t.Fatalf("expected 3 errors, got %v", errs)
	}
}

func TestGeneratedIDShape(t *testing.T) {
	for i := 0; i < 200; i++ {
		id := generateApplicationID()
		if len(id) != 10 || id[:4] != "APP-" {
			t.Fatalf("unexpected generated id %q", id)
		}
	}
}
