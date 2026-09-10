package newsletter

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/noah-isme/backend-toko/internal/common"
	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
	"github.com/rs/zerolog"
)

type Subscriber struct {
	ID        string    `json:"id"`
	TenantID  string    `json:"tenantId"`
	Email     string    `json:"email"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type Service struct {
	Q       dbgen.Querier
	Mail    common.EmailSender
	BaseURL string
	Log     zerolog.Logger
}

func NewService(q dbgen.Querier, mailer common.EmailSender, baseURL string, log zerolog.Logger) *Service {
	return &Service{
		Q:       q,
		Mail:    mailer,
		BaseURL: baseURL,
		Log:     log,
	}
}

func (s *Service) Subscribe(ctx context.Context, tenantIDStr, rawEmail string) (*Subscriber, error) {
	cleanEmail := strings.ToLower(strings.TrimSpace(rawEmail))
	if cleanEmail == "" {
		return nil, errors.New("email is required")
	}
	addr, err := mail.ParseAddress(cleanEmail)
	if err != nil {
		return nil, fmt.Errorf("invalid email address: %w", err)
	}

	tenantUUID, err := toUUID(tenantIDStr)
	if err != nil {
		return nil, fmt.Errorf("invalid tenant id: %w", err)
	}

	sub, err := s.Q.SubscribeNewsletter(ctx, dbgen.SubscribeNewsletterParams{
		TenantID: tenantUUID,
		Email:    addr.Address,
	})
	if err != nil {
		return nil, fmt.Errorf("subscribe newsletter: %w", err)
	}

	res := &Subscriber{
		ID:        uuid.UUID(sub.ID.Bytes).String(),
		TenantID:  uuid.UUID(sub.TenantID.Bytes).String(),
		Email:     sub.Email,
		Status:    sub.Status,
		CreatedAt: sub.CreatedAt.Time,
		UpdatedAt: sub.UpdatedAt.Time,
	}

	// Asynchronously send welcome email via Resend SMTP
	if s.Mail != nil {
		go func(recipientEmail string) {
			subject := "Selamat Datang di Toko Kios! 🛍️"
			body := s.buildWelcomeEmail()
			if err := s.Mail.Send(recipientEmail, subject, body); err != nil {
				s.Log.Warn().Err(err).Str("email", recipientEmail).Msg("failed to send newsletter welcome email")
			} else {
				s.Log.Info().Str("email", recipientEmail).Msg("newsletter welcome email sent successfully")
			}
		}(addr.Address)
	}

	return res, nil
}

func (s *Service) Unsubscribe(ctx context.Context, tenantIDStr, rawEmail string) error {
	cleanEmail := strings.ToLower(strings.TrimSpace(rawEmail))
	tenantUUID, err := toUUID(tenantIDStr)
	if err != nil {
		return fmt.Errorf("invalid tenant id: %w", err)
	}

	return s.Q.UnsubscribeNewsletter(ctx, dbgen.UnsubscribeNewsletterParams{
		TenantID: tenantUUID,
		Email:    cleanEmail,
	})
}

func (s *Service) List(ctx context.Context, tenantIDStr string, limit, offset int32) ([]Subscriber, int64, error) {
	tenantUUID, err := toUUID(tenantIDStr)
	if err != nil {
		return nil, 0, fmt.Errorf("invalid tenant id: %w", err)
	}

	if limit <= 0 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}

	rows, err := s.Q.ListNewsletterSubscribers(ctx, dbgen.ListNewsletterSubscribersParams{
		TenantID: tenantUUID,
		Limit:    limit,
		Offset:   offset,
	})
	if err != nil {
		return nil, 0, fmt.Errorf("list subscribers: %w", err)
	}

	total, err := s.Q.CountNewsletterSubscribers(ctx, tenantUUID)
	if err != nil {
		return nil, 0, fmt.Errorf("count subscribers: %w", err)
	}

	items := make([]Subscriber, 0, len(rows))
	for _, r := range rows {
		items = append(items, Subscriber{
			ID:        uuid.UUID(r.ID.Bytes).String(),
			TenantID:  uuid.UUID(r.TenantID.Bytes).String(),
			Email:     r.Email,
			Status:    r.Status,
			CreatedAt: r.CreatedAt.Time,
			UpdatedAt: r.UpdatedAt.Time,
		})
	}

	return items, total, nil
}

func (s *Service) buildWelcomeEmail() string {
	storeURL := strings.TrimRight(s.BaseURL, "/")
	if storeURL == "" {
		storeURL = "https://www.tokokios.my.id"
	}

	return fmt.Sprintf(`<!DOCTYPE html>
<html>
<head>
<meta charset="utf-8">
<style>
  body { font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, Helvetica, Arial, sans-serif; background-color: #f8fafc; margin: 0; padding: 24px; color: #0f172a; }
  .card { max-width: 520px; margin: 0 auto; background: #ffffff; border-radius: 12px; border: 1px solid #e2e8f0; overflow: hidden; box-shadow: 0 4px 6px -1px rgba(0,0,0,0.05); }
  .header { background: #0f172a; padding: 32px 24px; text-align: center; color: #ffffff; }
  .header h1 { margin: 0; font-size: 24px; font-weight: 700; letter-spacing: -0.5px; }
  .content { padding: 32px 24px; font-size: 15px; line-height: 1.6; }
  .badge { display: inline-block; background: #ecfdf5; color: #047857; font-weight: 600; padding: 4px 12px; border-radius: 9999px; margin-bottom: 16px; font-size: 13px; }
  .voucher-box { background: #f8fafc; border: 2px dashed #cbd5e1; border-radius: 8px; padding: 16px; text-align: center; margin: 24px 0; }
  .voucher-code { font-family: monospace; font-size: 20px; font-weight: bold; color: #2563eb; letter-spacing: 2px; }
  .button { display: block; width: fit-content; margin: 24px auto 0; background: #2563eb; color: #ffffff !important; text-decoration: none; padding: 12px 28px; border-radius: 8px; font-weight: 600; }
  .footer { border-top: 1px solid #f1f5f9; padding: 20px 24px; text-align: center; font-size: 12px; color: #64748b; background: #f8fafc; }
</style>
</head>
<body>
<div class="card">
  <div class="header">
    <h1>Toko Kios</h1>
  </div>
  <div class="content">
    <div class="badge">Terima Kasih Telah Berlangganan</div>
    <h2 style="margin-top:0;">Halo Sahabat Toko Kios! 👋</h2>
    <p>Terima kasih telah mendaftarkan email Anda untuk berlangganan buletin <strong>Toko Kios</strong>. Anda akan menerima update promo eksklusif, rilis produk baru, dan info flash sale spesial langsung di inbox Anda.</p>
    <div class="voucher-box">
      <div style="font-size: 13px; color: #64748b; margin-bottom: 6px;">Gunakan voucher sambutan khusus Anda:</div>
      <div class="voucher-code">WELCOME50K</div>
      <div style="font-size: 12px; color: #64748b; margin-top: 4px;">Potongan Rp 50.000 untuk pesanan pertama Anda</div>
    </div>
    <p style="text-align: center;">Mulai belanja sekarang dan temukan produk impian Anda:</p>
    <a href="%s" class="button">Mulai Belanja</a>
  </div>
  <div class="footer">
    <p>&copy; 2026 Toko Kios. Hak cipta dilindungi undang-undang.</p>
    <p>Email ini dikirim otomatis karena Anda berlangganan di tokokios.my.id.</p>
  </div>
</div>
</body>
</html>`, storeURL)
}

func toUUID(value string) (pgtype.UUID, error) {
	parsed, err := uuid.Parse(value)
	if err != nil {
		return pgtype.UUID{}, err
	}
	return pgtype.UUID{Bytes: parsed, Valid: true}, nil
}
