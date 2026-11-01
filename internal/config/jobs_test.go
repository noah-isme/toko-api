package config

import (
	"testing"
	"time"
)

func setDevJobEnv(t *testing.T) {
	t.Helper()
	t.Setenv("APP_ENV", "development")
	t.Setenv("DATABASE_URL", "postgres://user:pass@localhost:5432/toko")
	t.Setenv("REDIS_URL", "redis://localhost:6379")
	t.Setenv("JWT_SECRET", "development-secret")
}

func TestLoadJobDefaults(t *testing.T) {
	setDevJobEnv(t)
	cfg, err := LoadForTests(map[string]string{"WORKER_JOBS_ENABLED": ""})
	if err != nil {
		t.Fatal(err)
	}
	j := cfg.JobsConfig
	if j.WorkerJobsEnabled {
		t.Fatal("WORKER_JOBS_ENABLED must default to false for the rollout release")
	}
	checks := map[string][2]time.Duration{
		"lock ttl":        {j.JobLockTTL, 300 * time.Second},
		"expiry interval": {j.JobCheckoutExpiryInterval, time.Minute},
		"grace":           {j.JobCheckoutExpiryGrace, 10 * time.Minute},
		"silent after":    {j.JobCheckoutExpirySilentAfter, 24 * time.Hour},
		"proof hold":      {j.JobCheckoutProofHold, 72 * time.Hour},
		"track interval":  {j.JobShipmentTrackingInterval, 5 * time.Minute},
		"backoff base":    {j.ShipmentTrackBackoffBase, 15 * time.Minute},
		"backoff max":     {j.ShipmentTrackBackoffMax, 6 * time.Hour},
		"track silent":    {j.ShipmentTrackSilentAfter, 48 * time.Hour},
	}
	for name, c := range checks {
		if c[0] != c[1] {
			t.Errorf("%s = %v, want %v", name, c[0], c[1])
		}
	}
	if j.JobBatchSize != 100 || j.ShipmentTrackMaxCallsPerTick != 50 || j.ShipmentTrackMaxAttempts != 200 || j.ShipmentTrackMaxAgeDays != 30 {
		t.Fatalf("unexpected int defaults: %+v", j)
	}
}

func TestLoadJobOverrides(t *testing.T) {
	setDevJobEnv(t)
	cfg, err := LoadForTests(map[string]string{
		"WORKER_JOBS_ENABLED":                    "true",
		"JOB_CHECKOUT_EXPIRY_INTERVAL_SEC":       "1",
		"JOB_CHECKOUT_EXPIRY_GRACE_SEC":          "0",
		"JOB_CHECKOUT_EXPIRY_SILENT_AFTER_HOURS": "6",
		"JOB_CHECKOUT_PROOF_HOLD_HOURS":          "24",
		"JOB_BATCH_SIZE":                         "0",
		"SHIPMENT_TRACK_BACKOFF_BASE_SEC":        "600",
		"SHIPMENT_TRACK_BACKOFF_MAX_SEC":         "60",
		"SHIPMENT_TRACK_MAX_CALLS_PER_TICK":      "7",
		"SHIPMENT_TRACK_MAX_AGE_DAYS":            "0",
	})
	if err != nil {
		t.Fatal(err)
	}
	j := cfg.JobsConfig
	if !j.WorkerJobsEnabled {
		t.Fatal("WORKER_JOBS_ENABLED=true not applied")
	}
	if j.JobCheckoutExpiryInterval != 5*time.Second {
		t.Fatalf("interval must clamp to 5s, got %v", j.JobCheckoutExpiryInterval)
	}
	if j.JobCheckoutExpiryGrace != 0 || j.JobCheckoutExpirySilentAfter != 6*time.Hour || j.JobCheckoutProofHold != 24*time.Hour {
		t.Fatalf("unexpected checkout overrides: %+v", j)
	}
	if j.JobBatchSize != 1 || j.ShipmentTrackMaxAgeDays != 1 {
		t.Fatalf("batch and max age must clamp to 1: %+v", j)
	}
	if j.ShipmentTrackBackoffMax != 10*time.Minute {
		t.Fatalf("backoff max must be at least base, got %v", j.ShipmentTrackBackoffMax)
	}
	if j.ShipmentTrackMaxCallsPerTick != 7 {
		t.Fatalf("calls per tick = %d", j.ShipmentTrackMaxCallsPerTick)
	}
}
