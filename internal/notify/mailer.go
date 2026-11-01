package notify

import (
	"strings"

	"github.com/rs/zerolog"

	"github.com/noah-isme/backend-toko/internal/common"
	"github.com/noah-isme/backend-toko/internal/config"
)

// NewMailer returns the transactional email sender shared by cmd/api and
// cmd/worker. Without SMTP_HOST it falls back to a no-op and says so loudly:
// password reset and email verification both mint tokens that are useless if
// nothing delivers them, and that failure is otherwise completely silent.
func NewMailer(cfg *config.Config, logger zerolog.Logger) common.EmailSender {
	if strings.TrimSpace(cfg.SMTPHost) == "" {
		logger.Warn().
			Msg("SMTP_HOST not set: transactional email is disabled, password reset and email verification links will not be delivered")
		return common.NopEmailSender{}
	}

	sender, err := common.NewSMTPSender(common.SMTPSender{
		Host:             cfg.SMTPHost,
		Port:             cfg.SMTPPort,
		Username:         cfg.SMTPUsername,
		Password:         cfg.SMTPPassword,
		From:             cfg.SMTPFrom,
		ImplicitTLS:      cfg.SMTPImplicitTLS,
		AllowInsecureTLS: cfg.SMTPAllowInsecureTLS,
		Timeout:          cfg.SMTPTimeout,
	})
	if err != nil {
		// Misconfigured SMTP is an operator error worth failing on: starting
		// with silently broken email is worse than not starting.
		logger.Fatal().Err(err).Msg("configure smtp sender")
	}

	if cfg.SMTPAllowInsecureTLS {
		logger.Warn().Msg("SMTP_ALLOW_INSECURE_TLS is enabled: TLS certificates are not verified")
	}
	if strings.TrimSpace(cfg.PublicBaseURL) == "" {
		// Reset and verification mails embed a storefront link built from this
		// value. Without it the link is relative, which no mail client can follow.
		logger.Warn().
			Msg("PUBLIC_BASE_URL not set: password reset and verification emails will contain relative, unusable links")
	}
	logger.Info().
		Str("host", cfg.SMTPHost).
		Int("port", cfg.SMTPPort).
		Bool("implicitTLS", cfg.SMTPImplicitTLS).
		Str("from", cfg.SMTPFrom).
		Msg("transactional email enabled")

	return sender
}
