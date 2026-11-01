package config

import (
	"time"

	"github.com/knadh/koanf/v2"
)

// JobsConfig configures the worker's periodic jobs (checkout expiry and
// shipment tracking). See docs/ops/RUNBOOK.md "Periodic jobs".
type JobsConfig struct {
	// WorkerJobsEnabled starts the periodic jobs in cmd/worker. It defaults
	// to false for the release that introduces them: enable it only after
	// every API pod runs the webhook that guards PAID against EXPIRED orders.
	WorkerJobsEnabled bool
	// JobLockTTL bounds how long a crashed job run holds lock:job:<name>.
	JobLockTTL time.Duration
	// JobBatchSize is the per-tenant page size for job queries.
	JobBatchSize int

	JobCheckoutExpiryInterval time.Duration
	// JobCheckoutExpiryGrace keeps an order payable after its payment
	// deadline to absorb late provider notifications.
	JobCheckoutExpiryGrace time.Duration
	// JobCheckoutExpirySilentAfter expires older backlog without events.
	JobCheckoutExpirySilentAfter time.Duration
	// JobCheckoutProofHold keeps an order with a payment proof pending this
	// long after its first proof.
	JobCheckoutProofHold time.Duration

	JobShipmentTrackingInterval  time.Duration
	ShipmentTrackBackoffBase     time.Duration
	ShipmentTrackBackoffMax      time.Duration
	ShipmentTrackMaxCallsPerTick int
	ShipmentTrackMaxAttempts     int
	ShipmentTrackMaxAgeDays      int
	// ShipmentTrackSilentAfter applies older carrier events without
	// customer notifications.
	ShipmentTrackSilentAfter time.Duration
}

const minJobInterval = 5 * time.Second

func loadJobsConfig(k *koanf.Koanf) JobsConfig {
	sec := func(key string, def int) time.Duration {
		return time.Duration(parsePositiveIntAllowZero(k.String(key), def)) * time.Second
	}
	hours := func(key string, def int) time.Duration {
		return time.Duration(parsePositiveIntAllowZero(k.String(key), def)) * time.Hour
	}
	c := JobsConfig{
		WorkerJobsEnabled:            parseBoolWithDefault(k.String("WORKER_JOBS_ENABLED"), false),
		JobLockTTL:                   sec("JOB_LOCK_TTL_SEC", 300),
		JobBatchSize:                 parsePositiveIntAllowZero(k.String("JOB_BATCH_SIZE"), 100),
		JobCheckoutExpiryInterval:    sec("JOB_CHECKOUT_EXPIRY_INTERVAL_SEC", 60),
		JobCheckoutExpiryGrace:       sec("JOB_CHECKOUT_EXPIRY_GRACE_SEC", 600),
		JobCheckoutExpirySilentAfter: hours("JOB_CHECKOUT_EXPIRY_SILENT_AFTER_HOURS", 24),
		JobCheckoutProofHold:         hours("JOB_CHECKOUT_PROOF_HOLD_HOURS", 72),
		JobShipmentTrackingInterval:  sec("JOB_SHIPMENT_TRACKING_INTERVAL_SEC", 300),
		ShipmentTrackBackoffBase:     sec("SHIPMENT_TRACK_BACKOFF_BASE_SEC", 900),
		ShipmentTrackBackoffMax:      sec("SHIPMENT_TRACK_BACKOFF_MAX_SEC", 21600),
		ShipmentTrackMaxCallsPerTick: parsePositiveIntAllowZero(k.String("SHIPMENT_TRACK_MAX_CALLS_PER_TICK"), 50),
		ShipmentTrackMaxAttempts:     parsePositiveIntAllowZero(k.String("SHIPMENT_TRACK_MAX_ATTEMPTS"), 200),
		ShipmentTrackMaxAgeDays:      parsePositiveIntAllowZero(k.String("SHIPMENT_TRACK_MAX_AGE_DAYS"), 30),
		ShipmentTrackSilentAfter:     hours("SHIPMENT_TRACK_SILENT_AFTER_HOURS", 48),
	}
	if c.JobLockTTL < minJobInterval {
		c.JobLockTTL = minJobInterval
	}
	if c.JobBatchSize < 1 {
		c.JobBatchSize = 1
	}
	if c.JobCheckoutExpiryInterval < minJobInterval {
		c.JobCheckoutExpiryInterval = minJobInterval
	}
	if c.JobShipmentTrackingInterval < minJobInterval {
		c.JobShipmentTrackingInterval = minJobInterval
	}
	if c.ShipmentTrackBackoffBase < minJobInterval {
		c.ShipmentTrackBackoffBase = minJobInterval
	}
	if c.ShipmentTrackBackoffMax < c.ShipmentTrackBackoffBase {
		c.ShipmentTrackBackoffMax = c.ShipmentTrackBackoffBase
	}
	if c.ShipmentTrackMaxCallsPerTick < 1 {
		c.ShipmentTrackMaxCallsPerTick = 1
	}
	if c.ShipmentTrackMaxAttempts < 1 {
		c.ShipmentTrackMaxAttempts = 1
	}
	if c.ShipmentTrackMaxAgeDays < 1 {
		c.ShipmentTrackMaxAgeDays = 1
	}
	return c
}
