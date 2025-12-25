-- name: CreateReview :one
INSERT INTO reviews (product_id, user_id, rating, comment, tenant_id)
VALUES ($1, $2, $3, $4, $5)
RETURNING id, product_id, user_id, rating, comment, created_at, updated_at, tenant_id;

-- name: GetProductReviews :many
SELECT id, product_id, user_id, rating, comment, created_at, updated_at, tenant_id
FROM reviews
WHERE product_id = $1 AND tenant_id = $2
ORDER BY created_at DESC
LIMIT $3 OFFSET $4;

-- name: GetReviewStats :one
SELECT 
    COUNT(*) as total_reviews,
    COALESCE(AVG(rating), 0)::float8 as average_rating,
    COUNT(*) FILTER (WHERE rating = 5) as count_5_star,
    COUNT(*) FILTER (WHERE rating = 4) as count_4_star,
    COUNT(*) FILTER (WHERE rating = 3) as count_3_star,
    COUNT(*) FILTER (WHERE rating = 2) as count_2_star,
    COUNT(*) FILTER (WHERE rating = 1) as count_1_star
FROM reviews
WHERE product_id = $1 AND tenant_id = $2;

-- name: DeleteReview :exec
DELETE FROM reviews
WHERE id = $1 AND user_id = $2 AND tenant_id = $3;

-- name: CheckUserReview :one
SELECT id 
FROM reviews 
WHERE user_id = $1 AND product_id = $2 AND tenant_id = $3
LIMIT 1;
