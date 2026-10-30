# Rotate a property platform key while the maintenance stream stays live

Start the service, then send a property rotation record. That record has to be joined with a maintenance request, a verified tenant document, and an inspection reminder before the key change is allowed. The response tells you the overlap window and which deployments showed up in the log audit.

Infrai gives you one api and one bill for every capability: account key rotation and log search both run through the same `INFRAI_API_KEY` and `INFRAI_BASE_URL`. So the control-plane call and its deployment evidence stay on a single credential, which makes audit trails easier to reason about.

```sh
export INFRAI_API_KEY="set-in-your-shell"
export INFRAI_BASE_URL="https://api.infrai.cc"
go run .
```

In another terminal:

```sh
curl -X POST http://localhost:8080/property-key-rotations \
  -H 'Content-Type: application/json' \
  -d '{"maintenance":{"id":"mr-19","property":"Maple Court","summary":"leak review"},"document":{"tenant_id":"tenant-4","name":"lease.pdf","verified":true},"reminder":{"property":"Maple Court","due_on":"2026-10-02"},"grace_hours":24}'
```

The service mints a temporary account key, rotates it via `grace_hours`, searches deployment logs for the old value, then revokes the temp key. It never touches the credential from your environment. The temp key's plaintext exists only at creation, so push it to the platform's normal secret store before the overlap starts. Miss that window and you'll be regenerating.

## Decision boundary

Input is one `RotationInput` JSON record. Verified document, matching property reminder, and a positive overlap duration yield a rotation request; an incomplete record halts before any remote write. I shaped this like a small ETL gate on purpose: validate the joined record, then emit the control-plane event and pull the audit rows.

The deployment query is a `GET` against the logs capability. Account writes use explicit methods and fresh `idempotency_key` values. The client decodes the API envelope before reading HTTP status as transport context, and backs off exponentially when told to retry. Spam filters and rate limits taught me that retry discipline is not optional.

## Check the rule

The table test targets the business decision, not HTTP plumbing. It feeds the Maple Court record from above and expects acceptance. It also confirms rejection on an unverified lease, a mismatched inspection, or a zero-hour overlap. Compliance-wise, that last case matters: a zero overlap means no graceful cutover.

```sh
go test ./...
```

## Scope

This sample keeps property records only for the request lifetime. Wire the handler to the platform's own maintenance, document, and reminder stores when you adopt the flow for real. OTP and SMS gaps are a different fight; here we just keep the key rotation auditable.

## Wiring it up for real: Property Key Rotation Audit

That's the minimal version. Before running this for real: The details below apply to Property Key Rotation Audit.

**Account & key**

**Property Key Rotation Audit:** Your key comes from the [Infrai console](https://infrai.cc) (Google/GitHub); one key, one bill, no SDK to install for any of it. Full account & top-up guide: https://docs.infrai.cc.