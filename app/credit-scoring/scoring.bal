// scoring.bal — the rule engine (BUILD-SPEC.md §6).
//
// Deterministic and stateless: no database, no randomness, no clock dependence.
// The same input always produces the same score. Every function here is pure and
// `isolated`, which is what makes that guarantee checkable by the compiler.
//
// The golden fixtures in tests/scoring_test.bal pin the two numbers the demo
// narrative depends on (712 APPROVED, 480 DECLINED). If a change here moves them,
// fix the change -- not the test (§6).

// --- thresholds -------------------------------------------------------------
// Every constant is also served by GET /rules, so what the audience sees and what
// the engine uses cannot drift apart.

const int BASE_POINTS = 300;
const int SCORE_MIN = 300;
const int SCORE_MAX = 850;
const int APPROVAL_THRESHOLD = 650;

// §5: flat interest at 18% per annum, the same formula loan-api uses to build schedules
const decimal ANNUAL_FLAT_RATE = 0.18d;

const int INCOME_POINTS_CAP = 200;
const decimal INCOME_POINTS_DIVISOR = 500d;
const int EMPLOYMENT_POINTS_CAP = 60;
const int KYC_VERIFIED_POINTS = 40;
const int KYC_UNVERIFIED_POINTS = -80;
const int DEFAULT_PENALTY_PER_OCCURRENCE = 150;

const decimal DTI_BAND_1 = 0.20d;
const decimal DTI_BAND_2 = 0.35d;
const decimal DTI_BAND_3 = 0.50d;
const int DTI_POINTS_1 = 150;
const int DTI_POINTS_2 = 100;
const int DTI_POINTS_3 = 40;
const int DTI_POINTS_4 = -100;

// --- money ------------------------------------------------------------------

// Total repayable under flat interest (§5): amount x (1 + 0.18 x term/12).
public isolated function totalRepayable(decimal amountKes, int termMonths) returns decimal {
    return amountKes * (1d + ANNUAL_FLAT_RATE * <decimal>termMonths / 12d);
}

// The monthly instalment, rounded to 2dp. Affordability is judged on this figure,
// and loan-api builds the repayment schedule from the same formula (§5).
public isolated function monthlyInstalment(decimal amountKes, int termMonths) returns decimal {
    return (totalRepayable(amountKes, termMonths) / <decimal>termMonths).round(2);
}

// Debt-to-income: monthly instalment over monthly income (§6).
public isolated function debtToIncome(decimal amountKes, int termMonths, decimal monthlyIncome)
        returns decimal {
    return monthlyInstalment(amountKes, termMonths) / monthlyIncome;
}

// --- points -----------------------------------------------------------------

// min(200, floor(monthly_income / 500))
public isolated function incomePoints(decimal monthlyIncome) returns int {
    int raw = <int>(monthlyIncome / INCOME_POINTS_DIVISOR).floor();
    return raw < INCOME_POINTS_CAP ? raw : INCOME_POINTS_CAP;
}

// min(60, employment_months)
public isolated function employmentPoints(int employmentMonths) returns int {
    return employmentMonths < EMPLOYMENT_POINTS_CAP ? employmentMonths : EMPLOYMENT_POINTS_CAP;
}

// Bands are lower-inclusive and upper-exclusive: [0, 0.20) [0.20, 0.35) [0.35, 0.50) [0.50, inf).
// §6 writes 0.20-0.35 and 0.35-0.50, which overlap at the boundary; treating the lower
// bound as inclusive is the only reading that makes each DTI land in exactly one band.
public isolated function affordabilityPoints(decimal dti) returns int {
    if dti < DTI_BAND_1 {
        return DTI_POINTS_1;
    }
    if dti < DTI_BAND_2 {
        return DTI_POINTS_2;
    }
    if dti < DTI_BAND_3 {
        return DTI_POINTS_3;
    }
    return DTI_POINTS_4;
}

public isolated function kycPoints(boolean kycVerified) returns int {
    return kycVerified ? KYC_VERIFIED_POINTS : KYC_UNVERIFIED_POINTS;
}

public isolated function defaultsPenalty(int existingDefaults) returns int {
    return -DEFAULT_PENALTY_PER_OCCURRENCE * existingDefaults;
}

// --- decision ---------------------------------------------------------------

isolated function affordabilityPhrase(decimal dti) returns string {
    string label = dti < DTI_BAND_1 ? "strong"
        : dti < DTI_BAND_2 ? "adequate"
        : dti < DTI_BAND_3 ? "tight"
        : "insufficient";
    return string `Affordability ${label} (DTI ${dti.round(2)})`;
}

// Builds the human-readable reason shown beside the decision (§7.2).
isolated function buildReason(decimal dti, int employmentMonths, boolean kycVerified,
        int existingDefaults) returns string {
    string[] parts = [
        affordabilityPhrase(dti),
        string `employment history ${employmentMonths} months`,
        kycVerified ? "KYC verified" : "KYC not verified"
    ];
    if existingDefaults > 0 {
        string plural = existingDefaults == 1 ? "" : "s";
        parts.push(string `${existingDefaults} prior default${plural}`);
    }
    return string:'join("; ", ...parts);
}

// Score an application. Pure: same input, same output, always.
public isolated function score(ScoreRequest req) returns ScoreResponse {
    decimal dti = debtToIncome(req.amount_kes, req.term_months, req.monthly_income);

    int income = incomePoints(req.monthly_income);
    int employment = employmentPoints(req.employment_months);
    int affordability = affordabilityPoints(dti);
    int kyc = kycPoints(req.kyc_verified);
    int defaults = defaultsPenalty(req.existing_defaults);

    int raw = BASE_POINTS + income + employment + affordability + kyc + defaults;

    // clamp to [300, 850] (§6)
    int clamped = raw < SCORE_MIN ? SCORE_MIN : (raw > SCORE_MAX ? SCORE_MAX : raw);

    Factor[] factors = [
        {name: "base", points: BASE_POINTS},
        {name: "income", points: income},
        {name: "employment", points: employment},
        {name: "affordability", points: affordability},
        {name: "kyc", points: kyc}
    ];
    // Only shown when it applies, so the happy-path breakdown stays the five lines
    // printed in §7.2's example response.
    if req.existing_defaults > 0 {
        factors.push({name: "defaults", points: defaults});
    }

    return {
        application_id: req.application_id,
        score: clamped,
        decision: clamped >= APPROVAL_THRESHOLD ? "APPROVED" : "DECLINED",
        reason: buildReason(dti, req.employment_months, req.kyc_verified, req.existing_defaults),
        factors: factors
    };
}

// The live thresholds, so GET /rules cannot drift from the engine (§7.2).
public isolated function currentRules() returns Rules {
    return {
        base_points: BASE_POINTS,
        approval_threshold: APPROVAL_THRESHOLD,
        score_min: SCORE_MIN,
        score_max: SCORE_MAX,
        annual_flat_rate: ANNUAL_FLAT_RATE,
        income_points_cap: INCOME_POINTS_CAP,
        income_points_divisor: INCOME_POINTS_DIVISOR,
        employment_points_cap: EMPLOYMENT_POINTS_CAP,
        kyc_verified_points: KYC_VERIFIED_POINTS,
        kyc_unverified_points: KYC_UNVERIFIED_POINTS,
        default_penalty_per_occurrence: DEFAULT_PENALTY_PER_OCCURRENCE,
        dti_bands: [
            {range: string `< ${DTI_BAND_1}`, points: DTI_POINTS_1},
            {range: string `${DTI_BAND_1} - ${DTI_BAND_2}`, points: DTI_POINTS_2},
            {range: string `${DTI_BAND_2} - ${DTI_BAND_3}`, points: DTI_POINTS_3},
            {range: string `>= ${DTI_BAND_3}`, points: DTI_POINTS_4}
        ]
    };
}
