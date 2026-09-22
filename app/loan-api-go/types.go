// types.go — wire contract for loan-api-go (BUILD-SPEC.md §7.1).
//
// Byte-for-byte the same JSON as the Ballerina loan-api: snake_case field names,
// same shapes, same statuses. The two implementations are interchangeable behind
// the portal and the console, which is the point of having both.
package main

// ApplicationRequest is the POST /applications body. ApplicationID is
// caller-supplied so the demo is repeatable; if omitted we generate APP- plus six
// digits. Unknown fields are ignored rather than rejected, matching the open
// record on the Ballerina side.
type ApplicationRequest struct {
	ApplicationID    string  `json:"application_id,omitempty"`
	ApplicantName    string  `json:"applicant_name"`
	NationalID       string  `json:"national_id"`
	WalletMSISDN     string  `json:"wallet_msisdn"`
	AmountKES        float64 `json:"amount_kes"`
	TermMonths       int     `json:"term_months"`
	MonthlyIncome    float64 `json:"monthly_income"`
	EmploymentMonths int     `json:"employment_months"`
	KYCVerified      bool    `json:"kyc_verified"`
	ExistingDefaults int     `json:"existing_defaults,omitempty"`
}

type Disbursement struct {
	DisbursementID string  `json:"disbursement_id"`
	AmountKES      float64 `json:"amount_kes"`
	WalletMSISDN   string  `json:"wallet_msisdn"`
	ProviderRef    *string `json:"provider_ref"`
	Status         string  `json:"status"`
	DisbursedAt    string  `json:"disbursed_at"`
}

type Instalment struct {
	InstalmentNo int     `json:"instalment_no"`
	DueDate      string  `json:"due_date"`
	AmountDueKES float64 `json:"amount_due_kes"`
	PaidDate     *string `json:"paid_date"`
}

// ApplicationRecord is returned by POST /applications and GET /applications/{id}.
type ApplicationRecord struct {
	ApplicationID    string        `json:"application_id"`
	ApplicantName    string        `json:"applicant_name"`
	NationalID       string        `json:"national_id"`
	WalletMSISDN     string        `json:"wallet_msisdn"`
	AmountKES        float64       `json:"amount_kes"`
	TermMonths       int           `json:"term_months"`
	MonthlyIncome    float64       `json:"monthly_income"`
	EmploymentMonths int           `json:"employment_months"`
	KYCVerified      bool          `json:"kyc_verified"`
	ExistingDefaults int           `json:"existing_defaults"`
	Score            *int          `json:"score"`
	Decision         *string       `json:"decision"`
	DecisionReason   *string       `json:"decision_reason"`
	SubmittedAt      string        `json:"submitted_at"`
	DecidedAt        *string       `json:"decided_at"`
	Disbursement     *Disbursement `json:"disbursement"`
	ArrearsBucket    *string       `json:"arrears_bucket"`
	DaysPastDue      *int          `json:"days_past_due"`
	Repayments       []Instalment  `json:"repayments,omitempty"`
	// Recomputed on read for the console's detail screen (§7.5): §5's schema has no
	// column for it and the console cannot call credit-scoring itself (§7.2). Sound
	// because the engine is deterministic (§6). Best-effort — a scoring outage must
	// not make a decided application unreadable.
	ScoreFactors []Factor `json:"score_factors,omitempty"`
}

// ApplicationSummary is the compact row for the list endpoint (§7.1).
type ApplicationSummary struct {
	ApplicationID string  `json:"application_id"`
	ApplicantName string  `json:"applicant_name"`
	AmountKES     float64 `json:"amount_kes"`
	TermMonths    int     `json:"term_months"`
	Score         *int    `json:"score"`
	Decision      *string `json:"decision"`
	SubmittedAt   string  `json:"submitted_at"`
	ArrearsBucket *string `json:"arrears_bucket"`
}

// --- credit-scoring contract (§7.2) -----------------------------------------

type Factor struct {
	Name   string `json:"name"`
	Points int    `json:"points"`
}

type ScoreResponse struct {
	ApplicationID string   `json:"application_id"`
	Score         int      `json:"score"`
	Decision      string   `json:"decision"`
	Reason        string   `json:"reason"`
	Factors       []Factor `json:"factors"`
}

// --- errors -----------------------------------------------------------------

type FieldError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

type ValidationErrors struct {
	Message string       `json:"message"`
	Errors  []FieldError `json:"errors"`
}

type errorBody struct {
	Message string `json:"message"`
}
