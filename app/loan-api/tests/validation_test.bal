// validation_test.bal — the §7.1 validation rules.
//
// These are pure, so they run without a database, NATS or credit-scoring.

import ballerina/test;

isolated function validRequest() returns ApplicationRequest => {
    application_id: "APP-100244",
    applicant_name: "Amina W.",
    national_id: "12345678",
    wallet_msisdn: "254712345678",
    amount_kes: 250000d,
    term_months: 24,
    monthly_income: 96000d,
    employment_months: 30,
    kyc_verified: true,
    existing_defaults: 0
};

isolated function fieldsOf(FieldError[] errors) returns string[] =>
    from FieldError e in errors
        select e.'field;

@test:Config {}
function testValidRequestPasses() {
    test:assertEquals(validateApplication(validRequest()), <FieldError[]>[]);
}

// --- amount: 10,000 - 2,000,000 KES -----------------------------------------

@test:Config {}
function testAmountBoundaries() {
    ApplicationRequest r = validRequest();

    r.amount_kes = 10000d;      // lower bound, inclusive
    test:assertEquals(validateApplication(r).length(), 0);

    r.amount_kes = 2000000d;    // upper bound, inclusive
    test:assertEquals(validateApplication(r).length(), 0);

    r.amount_kes = 9999.99d;
    test:assertEquals(fieldsOf(validateApplication(r)), ["amount_kes"]);

    r.amount_kes = 2000000.01d;
    test:assertEquals(fieldsOf(validateApplication(r)), ["amount_kes"]);
}

// --- term: one of 6, 12, 24, 36 ---------------------------------------------

@test:Config {}
function testTermMustBeOneOfFour() {
    ApplicationRequest r = validRequest();
    foreach int t in [6, 12, 24, 36] {
        r.term_months = t;
        test:assertEquals(validateApplication(r).length(), 0, string `term ${t} should be valid`);
    }
    foreach int t in [0, 1, 18, 48, 60] {
        r.term_months = t;
        test:assertEquals(fieldsOf(validateApplication(r)), ["term_months"],
                string `term ${t} should be rejected`);
    }
}

// --- msisdn: ^254[17]\d{8}$ -------------------------------------------------

@test:Config {}
function testMsisdnPattern() {
    ApplicationRequest r = validRequest();

    foreach string m in ["254712345678", "254112345678", "254799999999"] {
        r.wallet_msisdn = m;
        test:assertEquals(validateApplication(r).length(), 0, string `${m} should be valid`);
    }

    foreach string m in [
        "254812345678",     // third digit must be 1 or 7
        "25471234567",      // too short
        "2547123456789",    // too long
        "0712345678",       // local format
        "+254712345678",    // leading plus
        "254712345abc",     // non-numeric
        ""
    ] {
        r.wallet_msisdn = m;
        test:assertEquals(fieldsOf(validateApplication(r)), ["wallet_msisdn"],
                string `'${m}' should be rejected`);
    }
}

// --- income, names, defaults ------------------------------------------------

@test:Config {}
function testIncomeMustBePositive() {
    ApplicationRequest r = validRequest();
    r.monthly_income = 0d;
    test:assertEquals(fieldsOf(validateApplication(r)), ["monthly_income"]);
    r.monthly_income = -1d;
    test:assertEquals(fieldsOf(validateApplication(r)), ["monthly_income"]);
}

@test:Config {}
function testNamesMustNotBeEmpty() {
    ApplicationRequest r = validRequest();
    r.applicant_name = "   ";
    test:assertEquals(fieldsOf(validateApplication(r)), ["applicant_name"]);

    r = validRequest();
    r.national_id = "";
    test:assertEquals(fieldsOf(validateApplication(r)), ["national_id"]);
}

@test:Config {}
function testExistingDefaultsIsOptionalAndNonNegative() {
    ApplicationRequest r = validRequest();
    _ = r.remove("existing_defaults");
    test:assertEquals(validateApplication(r).length(), 0, "existing_defaults should default to 0");

    r.existing_defaults = -1;
    test:assertEquals(fieldsOf(validateApplication(r)), ["existing_defaults"]);
}

// --- several at once --------------------------------------------------------

@test:Config {}
function testAllFailuresReportedTogether() {
    // §7.1 returns a field-level error LIST, not the first failure.
    ApplicationRequest r = {
        applicant_name: "",
        national_id: "",
        wallet_msisdn: "07123",
        amount_kes: 100d,
        term_months: 7,
        monthly_income: 0d,
        employment_months: -1,
        kyc_verified: false,
        existing_defaults: -2
    };
    test:assertEquals(validateApplication(r).length(), 8);
}

// --- DATABASE_URL parsing ---------------------------------------------------

@test:Config {}
function testParseDatabaseUrl() returns error? {
    DbConfig c = check parseDatabaseUrl(
            "postgres://kifaru:secret@postgres:5432/kifaru?sslmode=disable");
    test:assertEquals(c.host, "postgres");
    test:assertEquals(c.port, 5432);
    test:assertEquals(c.user, "kifaru");
    test:assertEquals(c.password, "secret");
    test:assertEquals(c.database, "kifaru");
}

@test:Config {}
function testParseDatabaseUrlWithoutPortOrQuery() returns error? {
    DbConfig c = check parseDatabaseUrl("postgresql://u:p@db-host/mydb");
    test:assertEquals(c.host, "db-host");
    test:assertEquals(c.port, 5432, "should default to 5432");
    test:assertEquals(c.database, "mydb");
}

@test:Config {}
function testParseDatabaseUrlRejectsRubbish() {
    test:assertTrue(parseDatabaseUrl("mysql://u:p@h/db") is error, "wrong scheme");
    test:assertTrue(parseDatabaseUrl("postgres://nohost") is error, "no credentials");
    test:assertTrue(parseDatabaseUrl("postgres://u:p@host") is error, "no database");
}
