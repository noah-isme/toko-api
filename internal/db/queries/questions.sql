-- name: CreateQuestion :one
INSERT INTO product_questions (product_id, user_id, question, tenant_id)
VALUES ($1, $2, $3, $4)
RETURNING id, product_id, user_id, question, answer, answered_by, answered_at, helpful_count, created_at, updated_at, tenant_id;

-- name: GetQuestion :one
SELECT id, product_id, user_id, question, answer, answered_by, answered_at, helpful_count, created_at, updated_at, tenant_id
FROM product_questions
WHERE id = $1;

-- name: GetProductQuestions :many
SELECT id, product_id, user_id, question, answer, answered_by, answered_at, helpful_count, created_at, updated_at, tenant_id
FROM product_questions
WHERE product_id = $1 AND tenant_id = $2
ORDER BY created_at DESC
LIMIT $3 OFFSET $4;

-- name: AnswerQuestion :one
UPDATE product_questions
SET answer = $2, answered_by = $3, answered_at = NOW(), updated_at = NOW()
WHERE id = $1
RETURNING id, product_id, user_id, question, answer, answered_by, answered_at, helpful_count, created_at, updated_at, tenant_id;

-- name: VoteQuestion :exec
INSERT INTO question_votes (question_id, user_id)
VALUES ($1, $2)
ON CONFLICT (question_id, user_id) DO NOTHING;

-- name: UnvoteQuestion :exec
DELETE FROM question_votes
WHERE question_id = $1 AND user_id = $2;

-- name: GetQuestionVoteCount :one
SELECT helpful_count FROM product_questions WHERE id = $1;

-- name: CheckQuestionVote :one
SELECT id FROM question_votes WHERE question_id = $1 AND user_id = $2;

-- name: UpdateQuestionVoteCount :one
UPDATE product_questions
SET helpful_count = (SELECT COUNT(*) FROM question_votes WHERE question_id = $1),
    updated_at = NOW()
WHERE id = $1
RETURNING helpful_count;
