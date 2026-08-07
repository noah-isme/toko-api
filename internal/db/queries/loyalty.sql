-- name: GetLoyaltyProfile :one
SELECT user_id, points, tier, tier_progress, lifetime_points, joined_at, COALESCE(next_tier_threshold, 0) AS next_tier_threshold, COALESCE(next_tier_name, '') AS next_tier_name
FROM loyalty_profiles
WHERE user_id = $1;

-- name: CreateOrUpdateLoyaltyProfile :one
INSERT INTO loyalty_profiles (user_id, points, tier, tier_progress, lifetime_points, joined_at)
VALUES ($1, $2, $3, $4, $5, NOW())
ON CONFLICT (user_id) DO UPDATE SET
    points = EXCLUDED.points,
    tier = EXCLUDED.tier,
    tier_progress = EXCLUDED.tier_progress,
    lifetime_points = EXCLUDED.lifetime_points,
    next_tier_threshold = EXCLUDED.next_tier_threshold,
    next_tier_name = EXCLUDED.next_tier_name
RETURNING user_id, points, tier, tier_progress, lifetime_points, joined_at, COALESCE(next_tier_threshold, 0) AS next_tier_threshold, COALESCE(next_tier_name, '') AS next_tier_name;

-- name: GetLoyaltyTransactions :many
SELECT id, user_id, type, points, balance, description, COALESCE(reference_id, '00000000-0000-0000-0000-000000000000') AS reference_id, COALESCE(reference_type, '') AS reference_type, created_at
FROM loyalty_transactions
WHERE user_id = $1
ORDER BY created_at DESC
LIMIT $2 OFFSET $3;

-- name: CreateLoyaltyTransaction :one
INSERT INTO loyalty_transactions (user_id, type, points, balance, description, reference_id, reference_type)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING id, user_id, type, points, balance, description, COALESCE(reference_id, '00000000-0000-0000-0000-000000000000') AS reference_id, COALESCE(reference_type, '') AS reference_type, created_at;

-- name: GetLoyaltyTransactionCount :one
SELECT COUNT(*) FROM loyalty_transactions WHERE user_id = $1;

-- name: GetActiveRewards :many
SELECT id, name, description, points_cost, active, created_at, updated_at
FROM loyalty_rewards
WHERE active = true
ORDER BY points_cost ASC;

-- name: UpdateLoyaltyProfilePoints :one
UPDATE loyalty_profiles
SET points = $1, lifetime_points = $2, updated_at = NOW()
WHERE user_id = $3
RETURNING user_id, points, tier, tier_progress, lifetime_points, joined_at, COALESCE(next_tier_threshold, 0) AS next_tier_threshold, COALESCE(next_tier_name, '') AS next_tier_name;
