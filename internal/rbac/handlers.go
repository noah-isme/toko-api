package rbac

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/noah-isme/backend-toko/internal/audit"
	"github.com/noah-isme/backend-toko/internal/common"
	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
	"github.com/noah-isme/backend-toko/internal/tenant"
)

// StaffStore is the tenant-scoped query surface used by the staff handlers.
// *dbgen.Queries satisfies it.
type StaffStore interface {
	ListTenantStaff(ctx context.Context, arg dbgen.ListTenantStaffParams) ([]dbgen.ListTenantStaffRow, error)
	CountTenantStaff(ctx context.Context, tenantID pgtype.UUID) (int64, error)
	GetTenantStaffMember(ctx context.Context, arg dbgen.GetTenantStaffMemberParams) (dbgen.GetTenantStaffMemberRow, error)
	GetTenantMemberUserIDByEmail(ctx context.Context, arg dbgen.GetTenantMemberUserIDByEmailParams) (pgtype.UUID, error)
	GetTenantMembershipForUpdate(ctx context.Context, arg dbgen.GetTenantMembershipForUpdateParams) (dbgen.TenantMembership, error)
	CountTenantOwners(ctx context.Context, tenantID pgtype.UUID) (int64, error)
	UpdateTenantMembershipRole(ctx context.Context, arg dbgen.UpdateTenantMembershipRoleParams) (dbgen.TenantMembership, error)
	ActivateTenantMembershipRole(ctx context.Context, arg dbgen.ActivateTenantMembershipRoleParams) (dbgen.TenantMembership, error)
	LockTenantStaff(ctx context.Context, tenantID pgtype.UUID) error
}

// StaffHandler serves the tenant staff management endpoints under /admin.
type StaffHandler struct {
	Pool *pgxpool.Pool
	Q    StaffStore
	// RunInTx runs fn inside one database transaction. When nil, a
	// transaction is opened on Pool. Unit tests inject a fake.
	RunInTx func(ctx context.Context, fn func(StaffStore) error) error
}

const (
	staffDefaultLimit = 20
	staffMaxLimit     = 100
	staffMaxPage      = 100000
	statusActive      = "ACTIVE"
)

// StaffMember is the API representation of a staff membership.
type StaffMember struct {
	UserID    string    `json:"userId"`
	Name      string    `json:"name"`
	Email     string    `json:"email"`
	Role      Role      `json:"role"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// RoleDefinition is one entry of GET /admin/roles.
type RoleDefinition struct {
	Role        Role         `json:"role"`
	Permissions []Permission `json:"permissions"`
}

// staffError is a handler outcome that maps to an HTTP error response.
type staffError struct {
	status  int
	code    string
	message string
	details any
}

func (e *staffError) Error() string { return e.message }

func newStaffError(status int, code, message string) *staffError {
	return &staffError{status: status, code: code, message: message}
}

var (
	errStaffNotFound = newStaffError(http.StatusNotFound, "STAFF_NOT_FOUND", "staff member not found")
	errUserNotFound  = newStaffError(http.StatusNotFound, "USER_NOT_FOUND", "no member of this tenant has that email")
	errStaffExists   = newStaffError(http.StatusConflict, "STAFF_EXISTS", "user is already staff; change the role instead")
	errOwnerRequired = newStaffError(http.StatusForbidden, "OWNER_REQUIRED", "only an owner can grant or change the owner role")
	errLastOwner     = newStaffError(http.StatusConflict, "LAST_OWNER", "the tenant must keep at least one active owner")
	errActorRevoked  = newStaffError(http.StatusForbidden, "TENANT_FORBIDDEN", "user is not an active member of this tenant")
	errActorNoManage = &staffError{status: http.StatusForbidden, code: "FORBIDDEN", message: "insufficient permissions", details: map[string]string{"required": string(PermStaffManage)}}
)

func writeStaffError(w http.ResponseWriter, err error) {
	var se *staffError
	if errors.As(err, &se) {
		common.JSONError(w, se.status, se.code, se.message, se.details)
		return
	}
	common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "failed to update staff", nil)
}

func validationError(w http.ResponseWriter, message string) {
	common.JSONError(w, http.StatusBadRequest, "VALIDATION_ERROR", message, nil)
}

// Roles lists the staff roles and their permissions (GET /admin/roles).
func (h *StaffHandler) Roles(w http.ResponseWriter, _ *http.Request) {
	roles := AllRoles()
	out := make([]RoleDefinition, 0, len(roles))
	for _, r := range roles {
		out = append(out, RoleDefinition{Role: r, Permissions: PermissionsFor(r)})
	}
	common.JSON(w, http.StatusOK, map[string]any{"data": out})
}

// List returns the staff of the current tenant (GET /admin/staff).
func (h *StaffHandler) List(w http.ResponseWriter, r *http.Request) {
	if h.Q == nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "staff store not configured", nil)
		return
	}
	tenantID, ok := requestTenant(r)
	if !ok {
		common.JSONError(w, http.StatusBadRequest, "MISSING_TENANT", "tenant is required", nil)
		return
	}
	page, limit := parsePage(r)
	ctx := r.Context()
	rows, err := h.Q.ListTenantStaff(ctx, dbgen.ListTenantStaffParams{
		TenantID:   tenantID,
		PageLimit:  int32(limit),
		PageOffset: int32((page - 1) * limit),
	})
	if err != nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "failed to list staff", nil)
		return
	}
	total, err := h.Q.CountTenantStaff(ctx, tenantID)
	if err != nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "failed to count staff", nil)
		return
	}
	out := make([]StaffMember, 0, len(rows))
	for _, row := range rows {
		out = append(out, StaffMember{
			UserID: uuidString(row.UserID), Name: row.Name, Email: row.Email,
			Role: RoleFromDB(row.Role), Status: row.Status,
			CreatedAt: row.CreatedAt.Time, UpdatedAt: row.UpdatedAt.Time,
		})
	}
	common.JSON(w, http.StatusOK, map[string]any{
		"data":       out,
		"pagination": common.Pagination{Page: page, PerPage: limit, TotalItems: int(total)},
	})
}

type assignRequest struct {
	Email string `json:"email"`
	Role  string `json:"role"`
}

type changeRoleRequest struct {
	Role string `json:"role"`
}

// Assign grants a staff role to an existing member of the tenant, looked up
// by email (POST /admin/staff).
func (h *StaffHandler) Assign(w http.ResponseWriter, r *http.Request) {
	var req assignRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		validationError(w, "invalid payload")
		return
	}
	email := strings.TrimSpace(req.Email)
	if email == "" || !strings.Contains(email, "@") {
		validationError(w, "a valid email is required")
		return
	}
	newRole, ok := parseStaffRole(req.Role)
	if !ok {
		validationError(w, "role must be one of the staff roles")
		return
	}
	scope, ok := h.scope(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	audit.Annotate(ctx, "email", email)
	audit.Annotate(ctx, "newRole", string(newRole))

	var member StaffMember
	err := h.inTx(ctx, func(q StaffStore) error {
		actorRole, err := lockAndLoadActor(ctx, q, scope)
		if err != nil {
			return err
		}
		if newRole == RoleOwner && actorRole != RoleOwner {
			return errOwnerRequired
		}
		// Only users who already belong to this tenant can be found, so an
		// email of another tenant's account reads exactly like an unknown one.
		userID, err := q.GetTenantMemberUserIDByEmail(ctx, dbgen.GetTenantMemberUserIDByEmailParams{TenantID: scope.tenantID, Email: email})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return errUserNotFound
			}
			return fmt.Errorf("resolve member email: %w", err)
		}
		audit.Annotate(ctx, "targetUserId", uuidString(userID))
		target, err := q.GetTenantMembershipForUpdate(ctx, dbgen.GetTenantMembershipForUpdateParams{TenantID: scope.tenantID, UserID: userID})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return errUserNotFound
			}
			return fmt.Errorf("lock target membership: %w", err)
		}
		previous := RoleFromDB(target.Role)
		audit.Annotate(ctx, "previousRole", string(previous))
		if previous.IsStaff() {
			return errStaffExists
		}
		if _, err := q.ActivateTenantMembershipRole(ctx, dbgen.ActivateTenantMembershipRoleParams{
			TenantID: scope.tenantID, UserID: userID, Role: newRole.DB(),
		}); err != nil {
			return fmt.Errorf("grant role: %w", err)
		}
		member, err = loadMember(ctx, q, scope.tenantID, userID)
		return err
	})
	if err != nil {
		writeStaffError(w, err)
		return
	}
	common.JSON(w, http.StatusCreated, map[string]any{"data": member})
}

// ChangeRole changes the role of an existing staff member
// (PATCH /admin/staff/{userId}).
func (h *StaffHandler) ChangeRole(w http.ResponseWriter, r *http.Request) {
	targetID, ok := parseTargetID(w, r)
	if !ok {
		return
	}
	var req changeRoleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		validationError(w, "invalid payload")
		return
	}
	newRole, ok := parseStaffRole(req.Role)
	if !ok {
		validationError(w, "role must be one of the staff roles; use DELETE to revoke")
		return
	}
	scope, ok := h.scope(w, r)
	if !ok {
		return
	}
	member, err := h.setRole(r.Context(), scope, targetID, newRole)
	if err != nil {
		writeStaffError(w, err)
		return
	}
	common.JSON(w, http.StatusOK, map[string]any{"data": member})
}

// Revoke removes the staff role (back to customer) without deleting the
// membership (DELETE /admin/staff/{userId}).
func (h *StaffHandler) Revoke(w http.ResponseWriter, r *http.Request) {
	targetID, ok := parseTargetID(w, r)
	if !ok {
		return
	}
	scope, ok := h.scope(w, r)
	if !ok {
		return
	}
	if _, err := h.setRole(r.Context(), scope, targetID, RoleCustomer); err != nil {
		writeStaffError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// setRole implements ChangeRole and Revoke inside one serialized transaction.
func (h *StaffHandler) setRole(ctx context.Context, scope requestScope, targetID pgtype.UUID, newRole Role) (StaffMember, error) {
	audit.Annotate(ctx, "targetUserId", uuidString(targetID))
	audit.Annotate(ctx, "newRole", string(newRole))
	var member StaffMember
	err := h.inTx(ctx, func(q StaffStore) error {
		actorRole, err := lockAndLoadActor(ctx, q, scope)
		if err != nil {
			return err
		}
		target, err := q.GetTenantMembershipForUpdate(ctx, dbgen.GetTenantMembershipForUpdateParams{TenantID: scope.tenantID, UserID: targetID})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return errStaffNotFound
			}
			return fmt.Errorf("lock target membership: %w", err)
		}
		previous := RoleFromDB(target.Role)
		if !previous.IsStaff() {
			return errStaffNotFound
		}
		audit.Annotate(ctx, "previousRole", string(previous))
		if (previous == RoleOwner || newRole == RoleOwner) && actorRole != RoleOwner {
			return errOwnerRequired
		}
		// Last-owner rule: only an ACTIVE owner is protected and counted. The
		// advisory lock taken first serializes this check per tenant.
		if previous == RoleOwner && target.Status == statusActive && newRole != RoleOwner {
			owners, err := q.CountTenantOwners(ctx, scope.tenantID)
			if err != nil {
				return fmt.Errorf("count owners: %w", err)
			}
			if owners <= 1 {
				return errLastOwner
			}
		}
		if _, err := q.UpdateTenantMembershipRole(ctx, dbgen.UpdateTenantMembershipRoleParams{
			TenantID: scope.tenantID, UserID: targetID, Role: newRole.DB(),
		}); err != nil {
			return fmt.Errorf("update role: %w", err)
		}
		member, err = loadMember(ctx, q, scope.tenantID, targetID)
		if err == nil {
			audit.Annotate(ctx, "email", member.Email)
		}
		return err
	})
	return member, err
}

// lockAndLoadActor takes the per-tenant staff lock and then re-reads the
// actor's membership inside the transaction. The role stored on the request
// context was read before the lock, so a concurrent demotion of the actor
// would otherwise go unnoticed; every authorization decision of a staff
// mutation uses the role returned here.
func lockAndLoadActor(ctx context.Context, q StaffStore, scope requestScope) (Role, error) {
	if err := q.LockTenantStaff(ctx, scope.tenantID); err != nil {
		return "", fmt.Errorf("lock tenant staff: %w", err)
	}
	actor, err := q.GetTenantMembershipForUpdate(ctx, dbgen.GetTenantMembershipForUpdateParams{TenantID: scope.tenantID, UserID: scope.actorID})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", errActorRevoked
		}
		return "", fmt.Errorf("load actor membership: %w", err)
	}
	if actor.Status != statusActive {
		return "", errActorRevoked
	}
	role := RoleFromDB(actor.Role)
	if !role.Has(PermStaffManage) {
		return "", errActorNoManage
	}
	return role, nil
}

func loadMember(ctx context.Context, q StaffStore, tenantID, userID pgtype.UUID) (StaffMember, error) {
	row, err := q.GetTenantStaffMember(ctx, dbgen.GetTenantStaffMemberParams{TenantID: tenantID, UserID: userID})
	if err != nil {
		return StaffMember{}, fmt.Errorf("load staff member: %w", err)
	}
	return StaffMember{
		UserID: uuidString(row.UserID), Name: row.Name, Email: row.Email,
		Role: RoleFromDB(row.Role), Status: row.Status,
		CreatedAt: row.CreatedAt.Time, UpdatedAt: row.UpdatedAt.Time,
	}, nil
}

type requestScope struct {
	tenantID pgtype.UUID
	actorID  pgtype.UUID
}

// scope resolves the tenant and the acting user, writing an error response
// when either is missing.
func (h *StaffHandler) scope(w http.ResponseWriter, r *http.Request) (requestScope, bool) {
	if h.Q == nil && h.RunInTx == nil && h.Pool == nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "staff store not configured", nil)
		return requestScope{}, false
	}
	tenantID, ok := requestTenant(r)
	if !ok {
		common.JSONError(w, http.StatusBadRequest, "MISSING_TENANT", "tenant is required", nil)
		return requestScope{}, false
	}
	rawUser, ok := common.UserID(r.Context())
	if !ok {
		common.JSONError(w, http.StatusUnauthorized, "UNAUTHORIZED", "authentication required", nil)
		return requestScope{}, false
	}
	actorID, err := parsePGUUID(rawUser)
	if err != nil {
		common.JSONError(w, http.StatusUnauthorized, "UNAUTHORIZED", "invalid authentication context", nil)
		return requestScope{}, false
	}
	return requestScope{tenantID: tenantID, actorID: actorID}, true
}

func (h *StaffHandler) inTx(ctx context.Context, fn func(StaffStore) error) error {
	if h.RunInTx != nil {
		return h.RunInTx(ctx, fn)
	}
	if h.Pool == nil {
		return errors.New("staff transaction pool not configured")
	}
	tx, err := h.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin staff tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := fn(dbgen.New(tx)); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit staff tx: %w", err)
	}
	return nil
}

func parseStaffRole(raw string) (Role, bool) {
	role, ok := ParseRole(strings.TrimSpace(raw))
	if !ok || !role.IsStaff() {
		return "", false
	}
	return role, true
}

func parseTargetID(w http.ResponseWriter, r *http.Request) (pgtype.UUID, bool) {
	id, err := parsePGUUID(chi.URLParam(r, "userId"))
	if err != nil {
		validationError(w, "invalid user id")
		return pgtype.UUID{}, false
	}
	return id, true
}

func requestTenant(r *http.Request) (pgtype.UUID, bool) {
	raw, ok := tenant.FromContext(r.Context())
	if !ok {
		return pgtype.UUID{}, false
	}
	id, err := parsePGUUID(raw)
	if err != nil {
		return pgtype.UUID{}, false
	}
	return id, true
}

func parsePage(r *http.Request) (page, limit int) {
	page, limit = 1, staffDefaultLimit
	if v, err := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("page"))); err == nil && v > 0 {
		page = v
	}
	if v, err := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("limit"))); err == nil && v > 0 {
		limit = v
	}
	if limit > staffMaxLimit {
		limit = staffMaxLimit
	}
	if page > staffMaxPage {
		page = staffMaxPage
	}
	return page, limit
}

func uuidString(id pgtype.UUID) string {
	if !id.Valid {
		return ""
	}
	return uuid.UUID(id.Bytes).String()
}
