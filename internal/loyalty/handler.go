package loyalty

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/noah-isme/backend-toko/internal/common"
)

type Handler struct {
	Svc *Service
}

func (h *Handler) GetProfile(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
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

	profile, err := h.Svc.GetProfile(ctx, userID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			common.JSONError(w, http.StatusNotFound, "NOT_FOUND", "loyalty profile not found", nil)
			return
		}
		common.JSONError(w, http.StatusInternalServerError, "GET_FAILED", "failed to get loyalty profile", err.Error())
		return
	}

	common.JSON(w, http.StatusOK, profile)
}

func (h *Handler) GetTransactions(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
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

	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))

	transactions, err := h.Svc.GetTransactions(ctx, userID, int32(page), int32(limit))
	if err != nil {
		common.JSONError(w, http.StatusInternalServerError, "LIST_FAILED", "failed to list transactions", err.Error())
		return
	}

	total, _ := h.Svc.GetTransactionCount(ctx, userID)

	common.JSON(w, http.StatusOK, map[string]any{
		"data": transactions,
		"meta": map[string]any{
			"page":         max(page, 1),
			"limit":        max(limit, 10),
			"total":        total,
			"total_pages":  max(1, (total + int64(max(limit, 10)) - 1) / int64(max(limit, 10))),
		},
	})
}

func (h *Handler) RedeemReward(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
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

	var req struct {
		RewardID string `json:"reward_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		common.JSONError(w, http.StatusBadRequest, "INVALID_BODY", "invalid request body", err.Error())
		return
	}

	if req.RewardID == "" {
		common.JSONError(w, http.StatusBadRequest, "MISSING_REWARD", "reward_id is required", nil)
		return
	}

	// Get profile to check points
	profile, err := h.Svc.GetProfile(ctx, userID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			common.JSONError(w, http.StatusNotFound, "NOT_FOUND", "loyalty profile not found", nil)
			return
		}
		common.JSONError(w, http.StatusInternalServerError, "GET_FAILED", "failed to get profile", err.Error())
		return
	}

	// Lookup reward cost from active rewards
	rewards, err := h.Svc.GetActiveRewards(ctx)
	if err != nil {
		common.JSONError(w, http.StatusInternalServerError, "REWARDS_FAILED", "failed to get rewards", err.Error())
		return
	}

	var cost int32
	found := false
	for _, reward := range rewards {
		if reward.ID.String() == req.RewardID || reward.Name == req.RewardID {
			cost = reward.PointsCost
			found = true
			break
		}
	}
	if !found {
		common.JSONError(w, http.StatusBadRequest, "INVALID_REWARD", "reward not found or inactive", nil)
		return
	}

	if profile.Points < cost {
		common.JSONError(w, http.StatusBadRequest, "INSUFFICIENT_POINTS", "not enough points", nil)
		return
	}

	// Deduct points
	newPoints := profile.Points - cost
	updatedProfile, err := h.Svc.UpdateProfilePoints(ctx, userID, newPoints, profile.LifetimePoints)
	if err != nil {
		common.JSONError(w, http.StatusInternalServerError, "UPDATE_FAILED", "failed to update points", err.Error())
		return
	}

	// Record transaction
	_, err = h.Svc.CreateTransaction(ctx, userID, "redeemed", "Redeemed reward: "+req.RewardID, -cost, newPoints, pgtype.UUID{}, "promo")
	if err != nil {
		common.JSONError(w, http.StatusInternalServerError, "TRANSACTION_FAILED", "failed to record transaction", err.Error())
		return
	}

	common.JSON(w, http.StatusOK, map[string]any{
		"success":        true,
		"message":        "Reward redeemed successfully",
		"remaining_points": updatedProfile.Points,
	})
}

func toUUID(value string) (pgtype.UUID, error) {
	parsed, err := uuid.Parse(value)
	if err != nil {
		return pgtype.UUID{}, err
	}
	return pgtype.UUID{Bytes: parsed, Valid: true}, nil
}
