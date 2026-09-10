package newsletter_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
	"github.com/noah-isme/backend-toko/internal/newsletter"
	"github.com/noah-isme/backend-toko/internal/tenant"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"
)

type mockMailer struct {
	mu       sync.Mutex
	messages []struct {
		To      string
		Subject string
		Body    string
	}
}

func (m *mockMailer) Send(to, subject, body string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.messages = append(m.messages, struct {
		To      string
		Subject string
		Body    string
	}{To: to, Subject: subject, Body: body})
	return nil
}

type fakeQuerier struct {
	dbgen.Querier
	subscribers map[string]dbgen.NewsletterSubscriber
}

func newFakeQuerier() *fakeQuerier {
	return &fakeQuerier{
		subscribers: make(map[string]dbgen.NewsletterSubscriber),
	}
}

func (f *fakeQuerier) SubscribeNewsletter(ctx context.Context, arg dbgen.SubscribeNewsletterParams) (dbgen.NewsletterSubscriber, error) {
	sub := dbgen.NewsletterSubscriber{
		ID:        pgtype.UUID{Bytes: uuid.New(), Valid: true},
		TenantID:  arg.TenantID,
		Email:     arg.Email,
		Status:    "SUBSCRIBED",
		CreatedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
		UpdatedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
	}
	f.subscribers[arg.Email] = sub
	return sub, nil
}

func (f *fakeQuerier) CountNewsletterSubscribers(ctx context.Context, tenantID pgtype.UUID) (int64, error) {
	return int64(len(f.subscribers)), nil
}

func (f *fakeQuerier) ListNewsletterSubscribers(ctx context.Context, arg dbgen.ListNewsletterSubscribersParams) ([]dbgen.NewsletterSubscriber, error) {
	var list []dbgen.NewsletterSubscriber
	for _, s := range f.subscribers {
		list = append(list, s)
	}
	return list, nil
}

func TestSubscribeValidEmail(t *testing.T) {
	q := newFakeQuerier()
	mailer := &mockMailer{}
	svc := newsletter.NewService(q, mailer, "https://www.tokokios.my.id", zerolog.Nop())
	h := newsletter.NewHandler(svc)

	tenantID := uuid.New().String()
	body, _ := json.Marshal(map[string]string{"email": "test@example.com"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/newsletter/subscribe", bytes.NewReader(body))
	req = req.WithContext(tenant.WithTenant(req.Context(), tenantID))
	rec := httptest.NewRecorder()

	h.Subscribe(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var resp struct {
		Data struct {
			Message string `json:"message"`
			Email   string `json:"email"`
		} `json:"data"`
	}
	err := json.Unmarshal(rec.Body.Bytes(), &resp)
	require.NoError(t, err)
	require.Equal(t, "test@example.com", resp.Data.Email)
	require.Contains(t, resp.Data.Message, "Thank you")
}

func TestSubscribeInvalidEmail(t *testing.T) {
	q := newFakeQuerier()
	mailer := &mockMailer{}
	svc := newsletter.NewService(q, mailer, "https://www.tokokios.my.id", zerolog.Nop())
	h := newsletter.NewHandler(svc)

	tenantID := uuid.New().String()
	body, _ := json.Marshal(map[string]string{"email": "not-an-email"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/newsletter/subscribe", bytes.NewReader(body))
	req = req.WithContext(tenant.WithTenant(req.Context(), tenantID))
	rec := httptest.NewRecorder()

	h.Subscribe(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestSubscribeMissingTenant(t *testing.T) {
	q := newFakeQuerier()
	mailer := &mockMailer{}
	svc := newsletter.NewService(q, mailer, "https://www.tokokios.my.id", zerolog.Nop())
	h := newsletter.NewHandler(svc)

	body, _ := json.Marshal(map[string]string{"email": "user@example.com"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/newsletter/subscribe", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	h.Subscribe(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}
