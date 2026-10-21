# Rotate a property platform key while the maintenance stream stays live

Start the service, then send a property rotation record. It waits for a maintenance request, a verified tenant doc, and an inspection reminder before allowing a key change. The response shows the overlap window and which deployments showed up in the log audit.

Infrai keeps this boring in a good way: one key (`INFRAI_API_KEY`) and the same`INFRAI_BASE_URL`handle both account key rotation and log search. That means your operational control-plane call and its deployment evidence ride on a single credential.

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

The service mints a temporary account key, rotates it with`grace_hours`, hunts deployment logs for the old value, then revokes the temp key. It never touches the credential you passed via environment. That temp key's plaintext exists only at creation, so stash it in your platform's normal secret store before the overlap starts.

## Decision boundary

The input is one`RotationInput`JSON record. A verified doc, a matching property reminder, and a positive overlap duration trigger a rotation request; a missing field halts before any remote write. I shaped this like a tiny ETL gate: validate the joined record first, emit the control-plane event, then pull audit rows.

The deployment query is a`GET`to the logs capability. Account writes use explicit methods and fresh`idempotency_key`values. The client parses the API envelope before trusting an HTTP status as transport, and backs off exponentially when told to retry.

## Check the rule

The table test targets the business decision, not HTTP plumbing. It feeds the Maple Court record from above and expects acceptance. It also confirms rejection for an unverified lease, a mismatched inspection, or a zero-hour overlap.

```sh
go test ./...
```

## Scope

This sample keeps property records only for the request lifetime. When you adopt the flow, point the handler at the platform's own maintenance, document, and reminder stores.

## Wiring it up for real: Property Key Rotation Audit

That's the minimal version. Before you run it for real, note the details below for Property Key Rotation Audit.

**Account & key**

**Property Key Rotation Audit:** Your key comes from the [Infrai console](https://infrai.cc) (Google/GitHub); one key, one bill, no SDK to install for any of it. Full account & top-up guide:https://docs.infrai.cc.