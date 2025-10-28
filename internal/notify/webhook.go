package notify

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"

	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
	"github.com/noah-isme/backend-toko/internal/obs"
)

// Dispatcher coordinates webhook scheduling and delivery.
type Dispatcher struct {
	Store              Store
	Client             *http.Client
	BackoffBaseSec     int
	DefaultMaxAttempts int
	Enabled            bool
	Replay             ReplayProtector
	ReplayTTL          time.Duration
}

// Schedule enqueues deliveries for active endpoints subscribed to the topic.
func (d *Dispatcher) Schedule(ctx context.Context, event dbgen.DomainEvent) error {
	if d == nil || !d.Enabled || d.Store == nil {
		return nil
	}
	if strings.TrimSpace(event.Topic) == "" {
		return nil
	}
	endpoints, err := d.Store.ListActiveEndpointsForTopic(ctx, event.Topic)
	if err != nil {
		return err
	}
	var joined error
	for _, ep := range endpoints {
		maxAttempt := d.DefaultMaxAttempts
		if maxAttempt <= 0 {
			maxAttempt = 6
		}
		_, err := d.Store.EnqueueDelivery(ctx, dbgen.EnqueueDeliveryParams{
			EndpointID: ep.ID,
			EventID:    event.ID,
			MaxAttempt: int32(maxAttempt),
		})
		if err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23505" {
				continue
			}
			joined = errors.Join(joined, fmt.Errorf("enqueue delivery for %s: %w", uuidFrom(ep.ID), err))
		}
	}
	return joined
}

// WorkOnce dequeues eligible deliveries and attempts delivery.
func (d *Dispatcher) WorkOnce(ctx context.Context, batch int32) error {
	if d == nil || !d.Enabled || d.Store == nil {
		return nil
	}
	if batch <= 0 {
		batch = 1
	}
	ctx, span := otel.Tracer("notify.Dispatcher").Start(ctx, "Dispatcher.WorkOnce")
	defer span.End()
	span.SetAttributes(attribute.Int("webhook.batch", int(batch)))

	deliveries, err := d.Store.DequeueDueDeliveries(ctx, batch)
	if err != nil {
		span.RecordError(err)
		return err
	}
	for _, del := range deliveries {
		if obs.WebhookDispatchAttempts != nil {
			obs.WebhookDispatchAttempts.Inc()
		}
		attemptStart := time.Now()
		if err := d.Store.MarkDelivering(ctx, del.ID); err != nil {
			continue
		}
		endpoint, err := d.Store.GetWebhookEndpoint(ctx, del.EndpointID)
		if err != nil {
			_ = d.failDelivery(ctx, del, fmt.Errorf("load endpoint: %w", err))
			continue
		}
		event, err := d.Store.GetDomainEvent(ctx, del.EventID)
		if err != nil {
			_ = d.failDelivery(ctx, del, fmt.Errorf("load event: %w", err))
			continue
		}
		status, respBody, deliverErr := d.deliver(ctx, endpoint, event, del)
		if deliverErr == nil && status >= 200 && status < 300 {
			if obs.WebhookDeliveriesTotal != nil {
				obs.WebhookDeliveriesTotal.WithLabelValues("delivered").Inc()
			}
			if obs.WebhookAttemptLatency != nil {
				obs.WebhookAttemptLatency.WithLabelValues("delivered").Observe(obs.DurationMillis(time.Since(attemptStart)))
			}
			statusVal := pgtype.Int4{}
			if status > 0 {
				statusVal = pgtype.Int4{Int32: int32(status), Valid: true}
			}
			bodyVal := pgtype.Text{}
			if respBody != "" {
				bodyVal = pgtype.Text{String: respBody, Valid: true}
			}
			if err := d.Store.MarkDelivered(ctx, dbgen.MarkDeliveredParams{
				ResponseStatus: statusVal,
				ResponseBody:   bodyVal,
				ID:             del.ID,
			}); err != nil {
				return err
			}
			continue
		}
		reason := fmt.Sprintf("status=%d err=%v", status, deliverErr)
		reasonText := pgtype.Text{String: reason, Valid: true}
		if int(del.Attempt+1) >= int(del.MaxAttempt) {
			if obs.WebhookDeliveriesTotal != nil {
				obs.WebhookDeliveriesTotal.WithLabelValues("dlq").Inc()
			}
			if obs.WebhookAttemptLatency != nil {
				obs.WebhookAttemptLatency.WithLabelValues("dlq").Observe(obs.DurationMillis(time.Since(attemptStart)))
			}
			if obs.WebhookDispatchDLQ != nil {
				obs.WebhookDispatchDLQ.Inc()
			}
			_ = d.Store.MoveToDLQ(ctx, dbgen.MoveToDLQParams{LastError: reasonText, ID: del.ID})
			_, _ = d.Store.InsertWebhookDlq(ctx, dbgen.InsertWebhookDlqParams{DeliveryID: del.ID, Reason: reasonText})
			continue
		}
		if obs.WebhookDeliveriesTotal != nil {
			obs.WebhookDeliveriesTotal.WithLabelValues("failed").Inc()
		}
		if obs.WebhookAttemptLatency != nil {
			obs.WebhookAttemptLatency.WithLabelValues("failed").Observe(obs.DurationMillis(time.Since(attemptStart)))
		}
		delay := d.nextDelay(del.Attempt)
		_ = d.Store.MarkFailedWithBackoff(ctx, dbgen.MarkFailedWithBackoffParams{DelaySec: int32(delay), LastError: reasonText, ID: del.ID})
	}
	return nil
}

func (d *Dispatcher) nextDelay(attempt int32) int {
	base := d.BackoffBaseSec
	if base <= 0 {
		base = 5
	}
	factor := 1 << int(attempt)
	if factor < 1 {
		factor = 1
	}
	return base * factor
}

func (d *Dispatcher) failDelivery(ctx context.Context, del dbgen.WebhookDelivery, err error) error {
	reason := err.Error()
	reasonText := pgtype.Text{String: reason, Valid: true}
	if int(del.Attempt+1) >= int(del.MaxAttempt) {
		if obs.WebhookDeliveriesTotal != nil {
			obs.WebhookDeliveriesTotal.WithLabelValues("dlq").Inc()
		}
		if obs.WebhookDispatchDLQ != nil {
			obs.WebhookDispatchDLQ.Inc()
		}
		if dlqErr := d.Store.MoveToDLQ(ctx, dbgen.MoveToDLQParams{LastError: reasonText, ID: del.ID}); dlqErr != nil {
			return dlqErr
		}
		_, _ = d.Store.InsertWebhookDlq(ctx, dbgen.InsertWebhookDlqParams{DeliveryID: del.ID, Reason: reasonText})
		return nil
	}
	if obs.WebhookDeliveriesTotal != nil {
		obs.WebhookDeliveriesTotal.WithLabelValues("failed").Inc()
	}
	delay := d.nextDelay(del.Attempt)
	return d.Store.MarkFailedWithBackoff(ctx, dbgen.MarkFailedWithBackoffParams{DelaySec: int32(delay), LastError: reasonText, ID: del.ID})
}

func (d *Dispatcher) deliver(ctx context.Context, ep dbgen.WebhookEndpoint, ev dbgen.DomainEvent, del dbgen.WebhookDelivery) (int, string, error) {
	if d.Client == nil {
		d.Client = HttpClient(5000, false)
	}
	ctx, span := otel.Tracer("notify.Dispatcher").Start(ctx, "Dispatcher.deliver")
	defer span.End()
	span.SetAttributes(
		attribute.String("webhook.endpoint_id", uuidFrom(ep.ID)),
		attribute.String("webhook.delivery_id", uuidFrom(del.ID)),
		attribute.String("webhook.topic", ev.Topic),
	)
	if err := validateURL(ep.Url); err != nil {
		span.RecordError(err)
		return 0, "", err
	}
	var occurred time.Time
	if ev.OccurredAt.Valid {
		occurred = ev.OccurredAt.Time
	} else {
		occurred = time.Now()
	}
	payload := struct {
		EventID    string          `json:"eventId"`
		Topic      string          `json:"topic"`
		Data       json.RawMessage `json:"data"`
		OccurredAt time.Time       `json:"occurredAt"`
	}{
		EventID:    uuidFrom(ev.ID),
		Topic:      ev.Topic,
		Data:       json.RawMessage(ev.Payload),
		OccurredAt: occurred,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		span.RecordError(err)
		return 0, "", err
	}
	ts := time.Now().Unix()
	if d.Replay != nil && d.ReplayTTL > 0 {
		key := replayKey(ep.ID, ev.ID)
		ok, err := d.Replay.Acquire(ctx, key, d.ReplayTTL)
		if err != nil {
			span.RecordError(err)
			return 0, "", err
		}
		if !ok {
			span.AddEvent("delivery replay prevented")
			return http.StatusOK, "replay-suppressed", nil
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ep.Url, bytes.NewReader(body))
	if err != nil {
		span.RecordError(err)
		return 0, "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "toko-api-webhooks/1.0")
	eventID := uuidFrom(ev.ID)
	deliveryID := uuidFrom(del.ID)
	req.Header.Set("X-Event-ID", eventID)
	req.Header.Set("X-Timestamp", fmt.Sprintf("%d", ts))
	req.Header.Set("X-Idempotency-Key", deliveryID)
	req.Header.Set("X-Signature", ComputeSignature(ep.Secret, ts, eventID, body))
	resp, err := d.Client.Do(req)
	if err != nil {
		span.RecordError(err)
		return 0, "", err
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		span.RecordError(err)
		return resp.StatusCode, "", err
	}
	span.SetAttributes(attribute.Int("http.status_code", resp.StatusCode))
	return resp.StatusCode, string(responseBody), nil
}

func validateURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid endpoint url: %w", err)
	}
	if parsed.Scheme != "https" && parsed.Scheme != "http" {
		return errors.New("webhook url must be http or https")
	}
	if parsed.Scheme == "http" {
		host := parsed.Hostname()
		if host != "localhost" && host != "127.0.0.1" {
			return errors.New("http webhook only allowed for localhost")
		}
	}
	if parsed.Host == "" {
		return errors.New("webhook url must include host")
	}
	return nil
}

// Deliver exposes the low-level delivery routine to allow manual replays and testing.
func (d *Dispatcher) Deliver(ctx context.Context, ep dbgen.WebhookEndpoint, ev dbgen.DomainEvent, del dbgen.WebhookDelivery) (int, string, error) {
	return d.deliver(ctx, ep, ev, del)
}

// ComputeSignature calculates the webhook signature for the provided payload. The
// format is HMAC-SHA256 over "<ts>.<eventID>.<body>" using the endpoint secret.
func ComputeSignature(secret string, ts int64, eventID string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(strconv.FormatInt(ts, 10)))
	_, _ = mac.Write([]byte("."))
	_, _ = mac.Write([]byte(eventID))
	_, _ = mac.Write([]byte("."))
	_, _ = mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

// HttpClient returns an HTTP client configured for webhook delivery.
func HttpClient(timeoutMs int, insecure bool) *http.Client {
	if timeoutMs <= 0 {
		timeoutMs = 5000
	}
	transport := &http.Transport{}
	if insecure {
		transport.TLSClientConfig = insecureTLSConfig
	}
	return &http.Client{
		Timeout:   time.Duration(timeoutMs) * time.Millisecond,
		Transport: otelhttp.NewTransport(transport),
	}
}

var insecureTLSConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec

// ReplayProtector guards against sending duplicate deliveries within a TTL.
type ReplayProtector interface {
	Acquire(ctx context.Context, key string, ttl time.Duration) (bool, error)
	Release(ctx context.Context, key string) error
}

func replayKey(endpointID, eventID pgtype.UUID) string {
	return fmt.Sprintf("wh:%s:%s", uuidFrom(endpointID), uuidFrom(eventID))
}

func uuidFrom(value pgtype.UUID) string {
	if !value.Valid {
		return ""
	}
	id, err := uuid.FromBytes(value.Bytes[:])
	if err != nil {
		return ""
	}
	return id.String()
}
