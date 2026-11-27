-- Per-user model spend cap in USD. NULL means the server default (SONIC_BUDGET_USD);
-- 0 means no limit.
ALTER TABLE users ADD COLUMN budget_usd REAL;
