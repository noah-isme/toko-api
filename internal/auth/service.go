package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/alexedwards/argon2id"
	"github.com/google/uuid"
	"github.com/jackc/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/lestrrat-go/jwx/v2/jwa"
	"github.com/lestrrat-go/jwx/v2/jwt"

	"github.com/noah-isme/backend-toko/internal/common"
	db "github.com/noah-isme/backend-toko/internal/db/gen"
)

const (
	defaultAccessTTL  = 15 * time.Minute
	defaultRefreshTTL = 24 * time.Hour
	defaultResetTTL   = 24 * time.Hour
)

// Service coordinates authentication, password management, and session persistence.
type Service struct {
	queries    db.Querier
	secret     []byte
	accessTTL  time.Duration
	refreshTTL time.Duration
	resetTTL   time.Duration
	now        func() time.Time
	signer     jwa.SignatureAlgorithm
}

// Config configures the auth service.
type Config struct {
	Queries         db.Querier
	Secret          string
	AccessTokenTTL  time.Duration
	RefreshTokenTTL time.Duration
	ResetTokenTTL   time.Duration
}

// User represents a safe subset of the user model returned to clients.
type User struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Email     string    `json:"email"`
	Roles     []string  `json:"roles"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// LoginResult bundles token material returned after a successful login.
type LoginResult struct {
	User          User      `json:"user"`
	AccessToken   string    `json:"access_token"`
	RefreshToken  string    `json:"refresh_token"`
	AccessExpiry  time.Time `json:"access_expires_at"`
	RefreshExpiry time.Time `json:"refresh_expires_at"`
}

// PasswordResetResult holds the generated reset token for out-of-band delivery.
type PasswordResetResult struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
}

// NewService constructs a Service instance with sane defaults.
func NewService(cfg Config) (*Service, error) {
	if cfg.Queries == nil {
		return nil, errors.New("auth: queries is required")
	}
	secret := strings.TrimSpace(cfg.Secret)
	if secret == "" {
		return nil, errors.New("auth: secret is required")
	}
	accessTTL := cfg.AccessTokenTTL
	if accessTTL <= 0 {
		accessTTL = defaultAccessTTL
	}
	refreshTTL := cfg.RefreshTokenTTL
	if refreshTTL <= 0 {
		refreshTTL = defaultRefreshTTL
	}
	resetTTL := cfg.ResetTokenTTL
	if resetTTL <= 0 {
		resetTTL = defaultResetTTL
	}
	return &Service{
		queries:    cfg.Queries,
		secret:     []byte(secret),
		accessTTL:  accessTTL,
		refreshTTL: refreshTTL,
		resetTTL:   resetTTL,
		now:        time.Now,
		signer:     jwa.HS256,
	}, nil
}

// WithNow allows tests to override the time provider.
func (s *Service) WithNow(now func() time.Time) {
	if now != nil {
		s.now = now
	}
}

// Register creates a new user with the supplied credentials.
func (s *Service) Register(ctx context.Context, name, email, password string) (User, error) {
	if strings.TrimSpace(name) == "" {
		return User{}, common.NewAppError("VALIDATION_ERROR", "name is required", httpStatusBadRequest, nil)
	}
	normalizedEmail := strings.TrimSpace(strings.ToLower(email))
	if normalizedEmail == "" {
		return User{}, common.NewAppError("VALIDATION_ERROR", "email is required", httpStatusBadRequest, nil)
	}
	if len(password) < 8 {
		return User{}, common.NewAppError("VALIDATION_ERROR", "password must be at least 8 characters", httpStatusBadRequest, nil)
	}

	hash, err := argon2id.CreateHash(password, argon2id.DefaultParams)
	if err != nil {
		return User{}, fmt.Errorf("hash password: %w", err)
	}

	created, err := s.queries.CreateUser(ctx, db.CreateUserParams{
		Name:         strings.TrimSpace(name),
		Email:        normalizedEmail,
		PasswordHash: hash,
	})
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return User{}, common.NewAppError("EMAIL_ALREADY_USED", "email is already registered", httpStatusConflict, err)
		}
		return User{}, fmt.Errorf("create user: %w", err)
	}

	return convertCreateUserRow(created), nil
}

// Login verifies credentials and issues new JWT/refresh token pair.
func (s *Service) Login(ctx context.Context, email, password, userAgent, ip string) (LoginResult, error) {
	normalizedEmail := strings.TrimSpace(strings.ToLower(email))
	if normalizedEmail == "" || password == "" {
		return LoginResult{}, common.NewAppError("INVALID_CREDENTIALS", "invalid email or password", httpStatusUnauthorized, nil)
	}

	dbUser, err := s.queries.GetUserByEmail(ctx, normalizedEmail)
	if err != nil {
		return LoginResult{}, common.NewAppError("INVALID_CREDENTIALS", "invalid email or password", httpStatusUnauthorized, nil)
	}

	ok, err := argon2id.ComparePasswordAndHash(password, dbUser.PasswordHash)
	if err != nil || !ok {
		return LoginResult{}, common.NewAppError("INVALID_CREDENTIALS", "invalid email or password", httpStatusUnauthorized, nil)
	}

	userID := uuidString(dbUser.ID)
	if userID == "" {
		return LoginResult{}, errors.New("auth: invalid user identifier")
	}

	accessToken, accessExpiry, err := s.signAccessToken(userID)
	if err != nil {
		return LoginResult{}, fmt.Errorf("sign access token: %w", err)
	}

	refreshToken, refreshExpiry, err := s.generateRefreshToken(ctx, dbUser.ID, userAgent, ip)
	if err != nil {
		return LoginResult{}, fmt.Errorf("generate refresh token: %w", err)
	}

	return LoginResult{
		User:          convertUserModel(dbUser),
		AccessToken:   accessToken,
		RefreshToken:  refreshToken,
		AccessExpiry:  accessExpiry,
		RefreshExpiry: refreshExpiry,
	}, nil
}

// Logout revokes the refresh token.
func (s *Service) Logout(ctx context.Context, refreshToken string) error {
	if strings.TrimSpace(refreshToken) == "" {
		return nil
	}
	return s.queries.DeleteSessionByToken(ctx, refreshToken)
}

// Me fetches the current authenticated user.
func (s *Service) Me(ctx context.Context, userID string) (User, error) {
	if strings.TrimSpace(userID) == "" {
		return User{}, common.NewAppError("UNAUTHORIZED", "unauthorized", httpStatusUnauthorized, nil)
	}
	id, err := pgUUIDFromString(userID)
	if err != nil {
		return User{}, common.NewAppError("UNAUTHORIZED", "unauthorized", httpStatusUnauthorized, nil)
	}
	dbUser, err := s.queries.GetUserByID(ctx, id)
	if err != nil {
		return User{}, common.NewAppError("UNAUTHORIZED", "unauthorized", httpStatusUnauthorized, nil)
	}
	return convertUserFromGet(dbUser), nil
}

// InitiatePasswordReset issues a reset token for the provided email. Errors are suppressed to avoid account enumeration.
func (s *Service) InitiatePasswordReset(ctx context.Context, email string) (PasswordResetResult, error) {
	normalizedEmail := strings.TrimSpace(strings.ToLower(email))
	if normalizedEmail == "" {
		return PasswordResetResult{}, nil
	}
	dbUser, err := s.queries.GetUserByEmail(ctx, normalizedEmail)
	if err != nil {
		// Silently return to avoid exposing whether the email exists.
		return PasswordResetResult{}, nil
	}

	token, err := generateToken(32)
	if err != nil {
		return PasswordResetResult{}, fmt.Errorf("generate reset token: %w", err)
	}
	expiresAt := s.now().Add(s.resetTTL)

	if _, err := s.queries.CreatePasswordReset(ctx, db.CreatePasswordResetParams{
		UserID:    dbUser.ID,
		Token:     token,
		ExpiresAt: pgTimestamp(expiresAt),
	}); err != nil {
		return PasswordResetResult{}, fmt.Errorf("create password reset: %w", err)
	}

	return PasswordResetResult{Token: token, ExpiresAt: expiresAt}, nil
}

// ResetPassword updates the password for the user associated with the provided token.
func (s *Service) ResetPassword(ctx context.Context, token, newPassword string) error {
	if len(newPassword) < 8 {
		return common.NewAppError("VALIDATION_ERROR", "password must be at least 8 characters", httpStatusBadRequest, nil)
	}

	reset, err := s.queries.GetPasswordResetByToken(ctx, token)
	if err != nil {
		return common.NewAppError("INVALID_TOKEN", "reset token is invalid", httpStatusBadRequest, err)
	}
	if reset.UsedAt.Valid {
		return common.NewAppError("INVALID_TOKEN", "reset token has been used", httpStatusBadRequest, nil)
	}
	if !reset.ExpiresAt.Valid || s.now().After(reset.ExpiresAt.Time) {
		return common.NewAppError("INVALID_TOKEN", "reset token has expired", httpStatusBadRequest, nil)
	}

	hash, err := argon2id.CreateHash(newPassword, argon2id.DefaultParams)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}

	if _, err := s.queries.UpdateUserPassword(ctx, db.UpdateUserPasswordParams{
		ID:           reset.UserID,
		PasswordHash: hash,
	}); err != nil {
		return fmt.Errorf("update password: %w", err)
	}

	if err := s.queries.MarkPasswordResetUsed(ctx, reset.ID); err != nil {
		return fmt.Errorf("mark reset used: %w", err)
	}

	if err := s.queries.DeleteSessionsByUser(ctx, reset.UserID); err != nil {
		return fmt.Errorf("delete sessions: %w", err)
	}

	if err := s.queries.DeletePasswordResetsByUser(ctx, reset.UserID); err != nil {
		return fmt.Errorf("delete resets: %w", err)
	}

	return nil
}

// ParseAccessToken validates an access token and returns the subject (user ID).
func (s *Service) ParseAccessToken(token string) (string, error) {
	if strings.TrimSpace(token) == "" {
		return "", common.NewAppError("UNAUTHORIZED", "missing token", httpStatusUnauthorized, nil)
	}
	parsed, err := jwt.ParseString(token, jwt.WithKey(s.signer, s.secret))
	if err != nil {
		return "", common.NewAppError("UNAUTHORIZED", "invalid token", httpStatusUnauthorized, err)
	}
	if err := jwt.Validate(parsed, jwt.WithClock(jwt.ClockFunc(s.now))); err != nil {
		return "", common.NewAppError("UNAUTHORIZED", "invalid token", httpStatusUnauthorized, err)
	}
	return parsed.Subject(), nil
}

func (s *Service) signAccessToken(userID string) (string, time.Time, error) {
	expiresAt := s.now().Add(s.accessTTL)
	token, err := jwt.NewBuilder().
		Subject(userID).
		IssuedAt(s.now()).
		Expiration(expiresAt).
		Build()
	if err != nil {
		return "", time.Time{}, err
	}
	signed, err := jwt.Sign(token, jwt.WithKey(s.signer, s.secret))
	if err != nil {
		return "", time.Time{}, err
	}
	return string(signed), expiresAt, nil
}

func (s *Service) generateRefreshToken(ctx context.Context, userID pgtype.UUID, userAgent, ip string) (string, time.Time, error) {
	if !userID.Valid {
		return "", time.Time{}, errors.New("auth: invalid user identifier")
	}
	token, err := generateToken(48)
	if err != nil {
		return "", time.Time{}, err
	}
	expiresAt := s.now().Add(s.refreshTTL)
	if _, err := s.queries.CreateSession(ctx, db.CreateSessionParams{
		UserID:       userID,
		RefreshToken: token,
		UserAgent:    pgText(userAgent),
		Ip:           pgText(ip),
		ExpiresAt:    pgTimestamp(expiresAt),
	}); err != nil {
		return "", time.Time{}, err
	}
	return token, expiresAt, nil
}

func generateToken(length int) (string, error) {
	buf := make([]byte, length)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func convertCreateUserRow(u db.CreateUserRow) User {
	return User{
		ID:        uuidString(u.ID),
		Name:      u.Name,
		Email:     u.Email,
		Roles:     u.Roles,
		CreatedAt: toTime(u.CreatedAt),
		UpdatedAt: toTime(u.UpdatedAt),
	}
}

func convertUserModel(u db.User) User {
	return User{
		ID:        uuidString(u.ID),
		Name:      u.Name,
		Email:     u.Email,
		Roles:     u.Roles,
		CreatedAt: toTime(u.CreatedAt),
		UpdatedAt: toTime(u.UpdatedAt),
	}
}

func convertUserFromGet(u db.GetUserByIDRow) User {
	return User{
		ID:        uuidString(u.ID),
		Name:      u.Name,
		Email:     u.Email,
		Roles:     u.Roles,
		CreatedAt: toTime(u.CreatedAt),
		UpdatedAt: toTime(u.UpdatedAt),
	}
}

func pgUUIDFromString(value string) (pgtype.UUID, error) {
	var id pgtype.UUID
	if err := id.Scan(value); err != nil {
		return pgtype.UUID{}, err
	}
	return id, nil
}

func uuidString(id pgtype.UUID) string {
	if !id.Valid {
		return ""
	}
	u, err := uuid.FromBytes(id.Bytes[:])
	if err != nil {
		return ""
	}
	return u.String()
}

func pgText(value string) pgtype.Text {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return pgtype.Text{}
	}
	return pgtype.Text{String: trimmed, Valid: true}
}

func pgTimestamp(t time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: t, Valid: true}
}

func toTime(ts pgtype.Timestamptz) time.Time {
	if !ts.Valid {
		return time.Time{}
	}
	return ts.Time
}

const httpStatusBadRequest = 400
const httpStatusUnauthorized = 401
const httpStatusConflict = 409
