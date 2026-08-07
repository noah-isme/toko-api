package qa

import (
	"context"

	"github.com/jackc/pgx/v5/pgtype"
	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
)

type Service struct {
	Q *dbgen.Queries
}

func (s *Service) CreateQuestion(ctx context.Context, productID, userID, tenantID pgtype.UUID, question string) (dbgen.ProductQuestion, error) {
	return s.Q.CreateQuestion(ctx, dbgen.CreateQuestionParams{
		ProductID: productID,
		UserID:    userID,
		Question:  question,
		TenantID:  tenantID,
	})
}

func (s *Service) GetQuestion(ctx context.Context, id pgtype.UUID) (dbgen.ProductQuestion, error) {
	return s.Q.GetQuestion(ctx, id)
}

func (s *Service) ListQuestions(ctx context.Context, productID, tenantID pgtype.UUID, page, limit int32) ([]dbgen.ProductQuestion, error) {
	if page < 1 {
		page = 1
	}
	if limit < 1 {
		limit = 10
	}
	offset := (page - 1) * limit
	return s.Q.GetProductQuestions(ctx, dbgen.GetProductQuestionsParams{
		ProductID: productID,
		TenantID:  tenantID,
		Limit:     limit,
		Offset:    offset,
	})
}

func (s *Service) AnswerQuestion(ctx context.Context, questionID pgtype.UUID, answer string, answeredBy pgtype.UUID) (dbgen.ProductQuestion, error) {
	return s.Q.AnswerQuestion(ctx, dbgen.AnswerQuestionParams{
		ID:         questionID,
		Answer:     pgtype.Text{String: answer, Valid: answer != ""},
		AnsweredBy: answeredBy,
	})
}

func (s *Service) VoteQuestion(ctx context.Context, questionID, userID pgtype.UUID) error {
	return s.Q.VoteQuestion(ctx, dbgen.VoteQuestionParams{
		QuestionID: questionID,
		UserID:     userID,
	})
}

func (s *Service) UnvoteQuestion(ctx context.Context, questionID, userID pgtype.UUID) error {
	return s.Q.UnvoteQuestion(ctx, dbgen.UnvoteQuestionParams{
		QuestionID: questionID,
		UserID:     userID,
	})
}

func (s *Service) GetQuestionVoteCount(ctx context.Context, questionID pgtype.UUID) (int32, error) {
	return s.Q.GetQuestionVoteCount(ctx, questionID)
}

func (s *Service) UpdateQuestionVoteCount(ctx context.Context, questionID pgtype.UUID) (int32, error) {
	return s.Q.UpdateQuestionVoteCount(ctx, questionID)
}

func (s *Service) CheckQuestionVote(ctx context.Context, questionID, userID pgtype.UUID) error {
	_, err := s.Q.CheckQuestionVote(ctx, dbgen.CheckQuestionVoteParams{
		QuestionID: questionID,
		UserID:     userID,
	})
	return err
}
