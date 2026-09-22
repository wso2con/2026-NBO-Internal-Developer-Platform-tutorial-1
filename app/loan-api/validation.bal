// validation.bal — request validation (BUILD-SPEC.md §7.1).
//
// Every rule here is named in §7.1. Pure functions, so they are unit-testable
// without a database or a running listener.

import ballerina/lang.regexp;

const decimal MIN_AMOUNT_KES = 10000d;
const decimal MAX_AMOUNT_KES = 2000000d;

final readonly & int[] VALID_TERMS = [6, 12, 24, 36];

// §7.1: msisdn matches ^254[17]\d{8}$. isFullMatch anchors the pattern, so the
// ^ and $ are implicit.
final regexp:RegExp MSISDN_PATTERN = re `254[17][0-9]{8}`;

public isolated function validateApplication(ApplicationRequest req) returns FieldError[] {
    FieldError[] errors = [];

    if req.applicant_name.trim() == "" {
        errors.push({'field: "applicant_name", message: "must not be empty"});
    }
    if req.national_id.trim() == "" {
        errors.push({'field: "national_id", message: "must not be empty"});
    }
    if !regexp:isFullMatch(MSISDN_PATTERN, req.wallet_msisdn) {
        errors.push({
            'field: "wallet_msisdn",
            message: "must match 254 followed by 1 or 7 and 8 digits"
        });
    }
    if req.amount_kes < MIN_AMOUNT_KES || req.amount_kes > MAX_AMOUNT_KES {
        errors.push({
            'field: "amount_kes",
            message: string `must be between ${MIN_AMOUNT_KES} and ${MAX_AMOUNT_KES}`
        });
    }
    if VALID_TERMS.indexOf(req.term_months) is () {
        errors.push({'field: "term_months", message: "must be one of 6, 12, 24, 36"});
    }
    if req.monthly_income <= 0d {
        errors.push({'field: "monthly_income", message: "must be greater than 0"});
    }
    if req.employment_months < 0 {
        errors.push({'field: "employment_months", message: "must not be negative"});
    }
    int defaults = req.existing_defaults ?: 0;
    if defaults < 0 {
        errors.push({'field: "existing_defaults", message: "must not be negative"});
    }

    return errors;
}
