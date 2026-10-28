// Numeric requirements live only in .agents/rules/coverage.json.
export function validateCoveragePolicy(policy) {
  for (const [scope, metrics] of Object.entries({
    frontend: ["branches"],
    backend: ["branches"],
    incremental: ["statements", "branches"],
  })) {
    for (const metric of metrics) {
      const value = policy?.[scope]?.[metric];
      if (typeof value !== "number" || !Number.isFinite(value) || value <= 0 || value > 100)
        throw new Error(`Invalid coverage threshold ${scope}.${metric}: expected a number greater than 0 and at most 100`);
    }
  }
  if (policy.active_scope !== undefined && !["backend-service", "all"].includes(policy.active_scope))
    throw new Error("Invalid coverage admission scope");
  if (policy.initial_commit !== undefined && policy.initial_commit !== "full-coverage-baseline")
    throw new Error("Invalid initial-commit coverage policy");
  return policy;
}
