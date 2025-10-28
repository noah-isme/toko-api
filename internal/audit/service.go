package audit

import (
	"context"
	"encoding/json"
	"errors"
	"math/rand"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/noah-isme/backend-toko/internal/common"
	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
	"github.com/noah-isme/backend-toko/internal/obs"
)

// ActorKind represents the source of an audited action.
type ActorKind string

const (
	// ActorKindUser represents an authenticated end-user.
	ActorKindUser ActorKind = "user"
	// ActorKindSystem represents internal automated actions.
	ActorKindSystem ActorKind = "system"
	// ActorKindAnonymous represents unauthenticated actors.
	ActorKindAnonymous ActorKind = "anonymous"
)

// Actor describes the entity performing the action.
type Actor struct {
	Kind   ActorKind
	UserID *string
}

// Store defines the database operations required for auditing.
type Store interface {
	InsertAuditLog(ctx context.Context, arg dbgen.InsertAuditLogParams) (dbgen.InsertAuditLogRow, error)
	ListAuditLogs(ctx context.Context, arg dbgen.ListAuditLogsParams) ([]dbgen.AuditLog, error)
}

// Service persists audit logs for critical application flows.
type Service struct {
	Store        Store
	Enabled      bool
	SamplingRate float64
}

// Record persists an audit log entry when auditing is enabled.
func (s Service) Record(ctx context.Context, actor Actor, action, resourceType, resourceID string, req *http.Request, status int, metadata []byte) error {
	if !s.Enabled {
		return nil
	}
	if s.SamplingRate > 0 && s.SamplingRate < 1 {
		if rand.Float64() > s.SamplingRate {
			return nil
		}
	}
	if req == nil {
		return errors.New("audit: request is required")
	}
	if s.Store == nil {
		return errors.New("audit: store not configured")
	}

	method := req.Method
	route := obs.RoutePatternFromContext(req.Context())
	if route == "" {
		route = strings.TrimSpace(req.URL.Path)
	}
	normalizedAction := buildAction(action, method, route)
	normalizedResource := buildResource(resourceType, route)

	actorKind := normalizeActorKind(actor.Kind)
	userID := sanitizeString(actor.UserID)
	ip := sanitizeString(pointerOf(common.ClientIP(req)))
	ua := sanitizeString(pointerOf(req.Header.Get("User-Agent")))
	requestID := sanitizeString(pointerOf(req.Header.Get("X-Request-ID")))
	resID := sanitizeString(pointerOf(resourceID))

	jsonb := toJSONB(metadata, req.URL.RawQuery)

	finalStatus := status
	if finalStatus == 0 {
		finalStatus = http.StatusOK
	}

	_, err := s.Store.InsertAuditLog(ctx, dbgen.InsertAuditLogParams{
		ActorKind:    string(actorKind),
		ActorUserID:  toNullUUID(userID),
		Action:       normalizedAction,
		ResourceType: normalizedResource,
		ResourceID:   toNullText(resID),
		Method:       method,
		Path:         req.URL.Path,
		Route:        toNullText(pointerOf(route)),
		Status:       int32(finalStatus),
		Ip:           toNullText(ip),
		UserAgent:    toNullText(ua),
		RequestID:    toNullText(requestID),
		Metadata:     jsonb,
	})
	return err
}

func buildAction(action, method, route string) string {
	trimmed := strings.TrimSpace(action)
	if trimmed != "" {
		return trimmed
	}
	base := strings.ToUpper(strings.TrimSpace(method))
	target := route
	if target == "" {
		target = "/"
	}
	return base + " " + target
}

func buildResource(resourceType, route string) string {
	trimmed := strings.TrimSpace(resourceType)
	if trimmed != "" {
		return trimmed
	}
	route = strings.Trim(route, " ")
	if route == "" {
		return "unknown"
	}
	segments := strings.Split(strings.Trim(route, "/"), "/")
	if len(segments) >= 3 && segments[0] == "api" && segments[1] == "v1" {
		return strings.Join(segments[2:], ".")
	}
	return strings.ReplaceAll(strings.Trim(route, "/"), "/", ".")
}

func normalizeActorKind(kind ActorKind) ActorKind {
	switch kind {
	case ActorKindUser, ActorKindSystem:
		return kind
	default:
		return ActorKindAnonymous
	}
}

func sanitizeString(value *string) *string {
	if value == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

func pointerOf(value string) *string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	trimmed := strings.TrimSpace(value)
	return &trimmed
}

func toNullUUID(value *string) pgtype.UUID {
	if value == nil {
		return pgtype.UUID{}
	}
	parsed, err := uuid.Parse(*value)
	if err != nil {
		return pgtype.UUID{}
	}
	return pgtype.UUID{Bytes: parsed, Valid: true}
}

func toNullText(value *string) pgtype.Text {
	if value == nil {
		return pgtype.Text{}
	}
	return pgtype.Text{String: *value, Valid: true}
}

func toJSONB(metadata []byte, query string) []byte {
	if len(metadata) > 0 {
		return metadata
	}
	if strings.TrimSpace(query) == "" {
		return nil
	}
	payload := map[string]string{"query": query}
	data, err := json.Marshal(payload)
	if err != nil {
		return nil
	}
	return data
}
