// scoring_test.bal — golden fixtures (BUILD-SPEC.md §6).
//
// The demo narrative depends on 712 and 480. If the implementation produces anything
// else, fix the implementation rather than these numbers (§6).
//
//   APP-100244  300 + 192 + 30 + 150 + 40 = 712  APPROVED
//   APP-100245  300 +  76 + 24 +  40 + 40 = 480  DECLINED

import ballerina/test;

final ScoreRequest APP_100244 = {
    application_id: "APP-100244",
    amount_kes: 250000d,
    term_months: 24,
    monthly_income: 96000d,
    employment_months: 30,
    kyc_verified: true,
    existing_defaults: 0
};

final ScoreRequest APP_100245 = {
    application_id: "APP-100245",
    amount_kes: 300000d,
    term_months: 24,
    monthly_income: 38000d,
    employment_months: 24,
    kyc_verified: true,
    existing_defaults: 0
};

// --- the two golden fixtures ------------------------------------------------

@test:Config {}
function testGoldenApp100244() {
    ScoreResponse r = score(APP_100244);
    test:assertEquals(r.score, 712, "APP-100244 must score exactly 712");
    test:assertEquals(r.decision, "APPROVED");
    test:assertEquals(r.application_id, "APP-100244");
}

@test:Config {}
function testGoldenApp100245() {
    ScoreResponse r = score(APP_100245);
    test:assertEquals(r.score, 480, "APP-100245 must score exactly 480");
    test:assertEquals(r.decision, "DECLINED");
    test:assertEquals(r.application_id, "APP-100245");
}

// §6 also pins the instalment and DTI behind each score.

@test:Config {}
function testGoldenInstalments() {
    test:assertEquals(monthlyInstalment(250000d, 24), 14166.67d, "APP-100244 instalment");
    test:assertEquals(monthlyInstalment(300000d, 24), 17000.00d, "APP-100245 instalment");
}

@test:Config {}
function testGoldenDti() {
    // 0.148 and 0.447 as printed in §6's table, at 3dp
    test:assertEquals(debtToIncome(250000d, 24, 96000d).round(3), 0.148d);
    test:assertEquals(debtToIncome(300000d, 24, 38000d).round(3), 0.447d);
}

@test:Config {}
function testTotalRepayableFlatInterest() {
    // 250,000 x (1 + 0.18 x 24/12) = 250,000 x 1.36
    test:assertEquals(totalRepayable(250000d, 24), 340000d);
    // 120,000 x (1 + 0.18 x 12/12) = 120,000 x 1.18
    test:assertEquals(totalRepayable(120000d, 12), 141600d);
    // 480,000 x (1 + 0.18 x 36/12) = 480,000 x 1.54
    test:assertEquals(totalRepayable(480000d, 36), 739200d);
}

// §7.2: the factors breakdown is what lets the presenter explain a decline without
// opening the code, so its shape is part of the contract.

@test:Config {}
function testFactorBreakdownMatchesSpecExample() {
    ScoreResponse r = score(APP_100244);
    test:assertEquals(r.factors, <Factor[]>[
        {name: "base", points: 300},
        {name: "income", points: 192},
        {name: "employment", points: 30},
        {name: "affordability", points: 150},
        {name: "kyc", points: 40}
    ]);
    // the breakdown must reconcile to the score
    int sum = 0;
    foreach Factor f in r.factors {
        sum += f.points;
    }
    test:assertEquals(sum, r.score);
}

@test:Config {}
function testReasonMentionsDtiEmploymentAndKyc() {
    ScoreResponse r = score(APP_100244);
    test:assertTrue(r.reason.includes("DTI 0.15"), "reason should show DTI to 2dp: " + r.reason);
    test:assertTrue(r.reason.includes("30 months"), r.reason);
    test:assertTrue(r.reason.includes("KYC verified"), r.reason);
}

// --- individual rules -------------------------------------------------------

@test:Config {}
function testIncomePointsCapAndFloor() {
    test:assertEquals(incomePoints(96000d), 192);      // floor(192.0)
    test:assertEquals(incomePoints(38000d), 76);
    test:assertEquals(incomePoints(99999d), 199);      // floor(199.998)
    test:assertEquals(incomePoints(200000d), 200);     // capped at 200
    test:assertEquals(incomePoints(1d), 0);
}

@test:Config {}
function testEmploymentPointsCap() {
    test:assertEquals(employmentPoints(30), 30);
    test:assertEquals(employmentPoints(60), 60);
    test:assertEquals(employmentPoints(120), 60);      // capped at 60
    test:assertEquals(employmentPoints(0), 0);
}

@test:Config {}
function testAffordabilityBandBoundaries() {
    // bands are lower-inclusive, upper-exclusive
    test:assertEquals(affordabilityPoints(0.199d), 150);
    test:assertEquals(affordabilityPoints(0.20d), 100);   // boundary -> lower band
    test:assertEquals(affordabilityPoints(0.349d), 100);
    test:assertEquals(affordabilityPoints(0.35d), 40);    // boundary -> lower band
    test:assertEquals(affordabilityPoints(0.499d), 40);
    test:assertEquals(affordabilityPoints(0.50d), -100);  // boundary -> lower band
    test:assertEquals(affordabilityPoints(1.20d), -100);
}

@test:Config {}
function testKycPoints() {
    test:assertEquals(kycPoints(true), 40);
    test:assertEquals(kycPoints(false), -80);
}

@test:Config {}
function testDefaultsPenalty() {
    test:assertEquals(defaultsPenalty(0), 0);
    test:assertEquals(defaultsPenalty(1), -150);
    test:assertEquals(defaultsPenalty(3), -450);
}

// --- clamping and the threshold ---------------------------------------------

@test:Config {}
function testScoreClampedToFloor() {
    // heavy defaults would take the raw score well below 300
    ScoreRequest req = {
        application_id: "APP-100245",
        amount_kes: 300000d,
        term_months: 24,
        monthly_income: 38000d,
        employment_months: 24,
        kyc_verified: true,
        existing_defaults: 5
    };
    ScoreResponse r = score(req);
    test:assertEquals(r.score, 300, "score must clamp at the 300 floor");
    test:assertEquals(r.decision, "DECLINED");
}

@test:Config {}
function testScoreClampedToCeiling() {
    ScoreRequest req = {
        application_id: "APP-CEIL",
        amount_kes: 10000d,
        term_months: 36,
        monthly_income: 500000d,   // 200 income points, tiny DTI
        employment_months: 240,    // 60 employment points
        kyc_verified: true,
        existing_defaults: 0
    };
    ScoreResponse r = score(req);
    test:assertTrue(r.score <= 850, "score must never exceed 850");
}

@test:Config {}
function testApprovalThresholdIsInclusive() {
    // 650 exactly must approve (§6: score >= 650 -> APPROVED)
    test:assertEquals(APPROVAL_THRESHOLD, 650);
}

@test:Config {}
function testDefaultsPushApprovedToDeclined() {
    // APP-100244 scores 712; one default costs 150, taking it to 562
    ScoreRequest req = {
        application_id: "APP-100244",
        amount_kes: 250000d,
        term_months: 24,
        monthly_income: 96000d,
        employment_months: 30,
        kyc_verified: true,
        existing_defaults: 1
    };
    ScoreResponse r = score(req);
    test:assertEquals(r.score, 562);
    test:assertEquals(r.decision, "DECLINED");
    // and the defaults factor appears only when it applies
    test:assertEquals(r.factors.length(), 6);
    test:assertEquals(r.factors[5], {name: "defaults", points: -150});
}

@test:Config {}
function testUnverifiedKycCosts120Points() {
    // +40 becomes -80, a 120-point swing: 712 -> 592
    ScoreRequest req = {
        application_id: "APP-100244",
        amount_kes: 250000d,
        term_months: 24,
        monthly_income: 96000d,
        employment_months: 30,
        kyc_verified: false,
        existing_defaults: 0
    };
    ScoreResponse r = score(req);
    test:assertEquals(r.score, 592);
    test:assertEquals(r.decision, "DECLINED");
}

// --- determinism ------------------------------------------------------------

@test:Config {}
function testScoringIsDeterministic() {
    ScoreResponse first = score(APP_100244);
    foreach int _ in 0 ..< 50 {
        test:assertEquals(score(APP_100244), first, "same input must always score the same");
    }
}

// --- rules endpoint ---------------------------------------------------------

@test:Config {}
function testRulesReflectEngineConstants() {
    Rules r = currentRules();
    test:assertEquals(r.base_points, 300);
    test:assertEquals(r.approval_threshold, 650);
    test:assertEquals(r.score_min, 300);
    test:assertEquals(r.score_max, 850);
    test:assertEquals(r.annual_flat_rate, 0.18d);
    test:assertEquals(r.dti_bands.length(), 4);
}
