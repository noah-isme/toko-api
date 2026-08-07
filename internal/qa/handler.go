package qa

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/noah-isme/backend-toko/internal/common"
	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
	"github.com/noah-isme/backend-toko/internal/tenant"
)

type Handler struct {
	Svc *Service
}

func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	productIDStr := chi.URLParam(r, "id")

	var req struct {
		Question string `json:"question"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		common.JSONError(w, http.StatusBadRequest, "INVALID_BODY", "invalid request body", err.Error())
		return
	}

	productID, err := h.resolveProductRef(r, productIDStr)
	if err != nil {
		common.JSONError(w, http.StatusBadRequest, "INVALID_PRODUCT_ID", "invalid product id", err.Error())
		return
	}

	userIDStr, ok := common.UserID(ctx)
	if !ok {
		common.JSONError(w, http.StatusUnauthorized, "UNAUTHORIZED", "login required", nil)
		return
	}
	userID, err := toUUID(userIDStr)
	if err != nil {
		common.JSONError(w, http.StatusInternalServerError, "INVALID_USER_ID", "invalid user id", err.Error())
		return
	}

	tenantIDStr, ok := tenant.FromContext(ctx)
	if !ok {
		common.JSONError(w, http.StatusBadRequest, "MISSING_TENANT", "missing tenant context", nil)
		return
	}
	tenantID, err := toUUID(tenantIDStr)
	if err != nil {
		common.JSONError(w, http.StatusInternalServerError, "INVALID_TENANT_ID", "invalid tenant id", err.Error())
		return
	}

	question, err := h.Svc.CreateQuestion(ctx, productID, userID, tenantID, req.Question)
	if err != nil {
		common.JSONError(w, http.StatusInternalServerError, "CREATE_FAILED", "failed to create question", err.Error())
		return
	}

	common.JSON(w, http.StatusCreated, question)
}

func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	productIDStr := chi.URLParam(r, "id")
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))

	productID, err := h.resolveProductRef(r, productIDStr)
	if err != nil {
		common.JSONError(w, http.StatusBadRequest, "INVALID_PRODUCT_ID", "invalid product id", err.Error())
		return
	}

	tenantIDStr, ok := tenant.FromContext(ctx)
	if !ok {
		common.JSONError(w, http.StatusBadRequest, "MISSING_TENANT", "missing tenant context", nil)
		return
	}
	tenantID, err := toUUID(tenantIDStr)
	if err != nil {
		common.JSONError(w, http.StatusInternalServerError, "INVALID_TENANT_ID", "invalid tenant id", err.Error())
		return
	}

	questions, err := h.Svc.ListQuestions(ctx, productID, tenantID, int32(page), int32(limit))
	if err != nil {
		common.JSONError(w, http.StatusInternalServerError, "LIST_FAILED", "failed to list questions", err.Error())
		return
	}

	common.JSON(w, http.StatusOK, questions)
}

func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	questionIDStr := chi.URLParam(r, "questionId")
	questionID, err := toUUID(questionIDStr)
	if err != nil {
		common.JSONError(w, http.StatusBadRequest, "INVALID_QUESTION_ID", "invalid question id", err.Error())
		return
	}

	question, err := h.Svc.GetQuestion(r.Context(), questionID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			common.JSONError(w, http.StatusNotFound, "NOT_FOUND", "question not found", nil)
			return
		}
		common.JSONError(w, http.StatusInternalServerError, "GET_FAILED", "failed to get question", err.Error())
		return
	}

	common.JSON(w, http.StatusOK, question)
}

func (h *Handler) Answer(w http.ResponseWriter, r *http.Request) {
	questionIDStr := chi.URLParam(r, "questionId")
	questionID, err := toUUID(questionIDStr)
	if err != nil {
		common.JSONError(w, http.StatusBadRequest, "INVALID_QUESTION_ID", "invalid question id", err.Error())
		return
	}

	var req struct {
		Answer string `json:"answer"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		common.JSONError(w, http.StatusBadRequest, "INVALID_BODY", "invalid request body", err.Error())
		return
	}

	userIDStr, ok := common.UserID(r.Context())
	if !ok {
		common.JSONError(w, http.StatusUnauthorized, "UNAUTHORIZED", "login required", nil)
		return
	}
	userID, err := toUUID(userIDStr)
	if err != nil {
		common.JSONError(w, http.StatusInternalServerError, "INVALID_USER_ID", "invalid user id", err.Error())
		return
	}

	question, err := h.Svc.AnswerQuestion(r.Context(), questionID, req.Answer, userID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			common.JSONError(w, http.StatusNotFound, "NOT_FOUND", "question not found", nil)
			return
		}
		common.JSONError(w, http.StatusInternalServerError, "ANSWER_FAILED", "failed to answer question", err.Error())
		return
	}

	common.JSON(w, http.StatusOK, question)
}

func (h *Handler) Vote(w http.ResponseWriter, r *http.Request) {
	questionIDStr := chi.URLParam(r, "questionId")
	questionID, err := toUUID(questionIDStr)
	if err != nil {
		common.JSONError(w, http.StatusBadRequest, "INVALID_QUESTION_ID", "invalid question id", err.Error())
		return
	}

	var req struct {
		Dir string `json:"dir"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		common.JSONError(w, http.StatusBadRequest, "INVALID_BODY", "invalid request body", err.Error())
		return
	}

	userIDStr, ok := common.UserID(r.Context())
	if !ok {
		common.JSONError(w, http.StatusUnauthorized, "UNAUTHORIZED", "login required", nil)
		return
	}
	userID, err := toUUID(userIDStr)
	if err != nil {
		common.JSONError(w, http.StatusInternalServerError, "INVALID_USER_ID", "invalid user id", err.Error())
		return
	}

	if req.Dir == "clear" {
		if err := h.Svc.UnvoteQuestion(r.Context(), questionID, userID); err != nil {
			common.JSONError(w, http.StatusInternalServerError, "UNVOTE_FAILED", "failed to remove vote", err.Error())
			return
		}
	} else {
		if err := h.Svc.VoteQuestion(r.Context(), questionID, userID); err != nil {
			common.JSONError(w, http.StatusInternalServerError, "VOTE_FAILED", "failed to add vote", err.Error())
			return
		}
	}

	count, err := h.Svc.UpdateQuestionVoteCount(r.Context(), questionID)
	if err != nil {
		common.JSONError(w, http.StatusInternalServerError, "VOTE_COUNT_FAILED", "failed to update vote count", err.Error())
		return
	}

	hasVoted := h.Svc.CheckQuestionVote(r.Context(), questionID, userID) == nil
	common.JSON(w, http.StatusOK, map[string]any{
		"helpfulCount": count,
		"myVote":       map[string]any{"hasVoted": hasVoted},
	})
}

func toUUID(value string) (pgtype.UUID, error) {
	parsed, err := uuid.Parse(value)
	if err != nil {
		return pgtype.UUID{}, err
	}
	return pgtype.UUID{Bytes: parsed, Valid: true}, nil
}

func (h *Handler) resolveProductRef(r *http.Request, value string) (pgtype.UUID, error) {
	if id, err := toUUID(value); err == nil {
		return id, nil
	}
	tenantID, ok := tenant.FromContext(r.Context())
	if !ok {
		return pgtype.UUID{}, errors.New("tenant is required")
	}
	tenantUUID, err := toUUID(tenantID)
	if err != nil {
		return pgtype.UUID{}, err
	}
	product, err := h.Svc.Q.GetProductBySlug(r.Context(), dbgen.GetProductBySlugParams{Slug: value, TenantID: tenantUUID})
	if err != nil {
		return pgtype.UUID{}, err
	}
	return product.ID, nil
}
