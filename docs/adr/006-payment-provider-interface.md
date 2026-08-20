# ADR-006: Pluggable Payment Provider Interface

**Status:** Accepted  
**Date:** 2024-03  

## Context

Indonesia's payment landscape requires supporting multiple gateways (Midtrans, Xendit, and potentially others). Each gateway has different APIs, webhook formats, and status nomenclature.

## Decision

Define a **`payment.Provider` interface** with methods `CreateIntent` and `VerifyWebhook`. Each gateway implements this interface:

- `payment.Midtrans` — Snap token flow, notification webhook.
- `payment.Xendit` — Invoice creation, callback webhook.
- `payment.Midtrans{Stub: true}` — deterministic stub for integration tests.

The active provider is selected at startup via `PAYMENT_PROVIDER` env var. Webhook handlers dispatch based on the `{provider}` URL parameter.

Shared concerns:
- Redis-based replay protection prevents double-processing of webhook callbacks.
- Idempotent payment intent creation via the `Idem` middleware.

## Consequences

- **Positive:** Adding a new gateway means implementing one interface — no checkout/order changes.
- **Positive:** The stub provider enables full checkout integration tests without network calls.
- **Negative:** Status normalisation (`normaliseMidtransStatus`, `normaliseXenditStatus`) must be maintained per provider.
- **Negative:** Provider-specific fields (e.g., Midtrans `snap_token`) leak into the generic response; kept behind nullable fields.
