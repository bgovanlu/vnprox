// T-2409: the canary title the cross-contamination pair shares.
//
// In its own module because Playwright refuses to let one spec file import
// another — a rule that exists to stop specs coupling, and which this pair
// would otherwise violate to share one string.
export const CONTAMINATION_TITLE = "t2409-contamination-canary";
