# One-order email recovery after the SES Raw IAM fix

Status: local Go tests and disposable PostgreSQL 16 migration test passed on
2026-11-22. CI, immutable images, production migration, Worker deployment, and
one-order requeue are still pending. This document does not authorize any of
those operations or another email send.

## Why a simple retry is unsafe

The paid order has one job in `exhausted` with five `transport_failed`
attempts and no recorded SES message ID. The original five AWS errors were
redacted by the Worker. A separate Raw/MIME probe received AccessDenied before
`ses:SendRawEmail` was granted, and a synthetic probe was accepted afterward;
that is the supported cause of the transport failure, without proving each
individual attempt's AWS error code. Migration 14 caps both the job count
and attempt number at five and gives an order exactly one job. Resetting the
counter would collide with the fifth attempt; deleting that attempt would lose
the audit trail. Replaying the payment webhook cannot enqueue a second job.

## Narrow recovery mechanism

Migration 15 adds `max_attempts` to each job with default `5`, permits only `5`
or `6`, and permits an attempt number of six. Existing and future jobs retain
the five-attempt limit. The Worker reads the per-job limit when claiming work,
scheduling failure, and expiring a final lease. Only the specifically approved
exhausted order may be changed to `max_attempts=6` and `retrying`. Its sixth
attempt gets a new UUID and keeps all five failed rows. If attempt six fails,
it becomes `exhausted` again; do not requeue it automatically.

## Gates before a production change

1. Review and test migration 15 on the isolated test database. Build and
   verify immutable migration and Worker images, with CI green.
2. Obtain separate approval for migration 15. Check the existing PostgreSQL
   backup and encrypted host snapshot still exist; apply migration once;
   verify version 15 clean and default `max_attempts=5`.
3. Obtain separate approval to replace only `osmi-email-worker`. Retain the
   current Worker image for rollback. Verify health, IAM, IMDS guard, SQS and
   PostgreSQL connectivity, Server/API/site health, and unchanged container IDs
   for every other service.
4. Before requeue, read only: confirm the exact order is paid, its ticket is
   sold with QR data, one job is `exhausted|5|5`, all five attempt rows are
   `transport_failed` without SES IDs, the recipient is not suppressed, and no
   later message has reached the buyer. If any condition differs, stop.
5. Obtain separate approval for **one** conditional requeue of that order. It
   may trigger one Worker attempt and one customer email. No payment or new
   ticket is created. Do not repeat the update if its result is lost; inspect
   job state first. The Worker SES client must use one SDK attempt for this
   recovery so a transport timeout cannot cause an automatic duplicate call.

The authorized SQL should use a private `order_uuid` parameter and a single
conditional `UPDATE ... RETURNING` transaction. Its `WHERE` clause must match
`completed/paid`, `exhausted`, `attempts=5`, `max_attempts=5`, exactly five
failed attempts with no SES IDs, and a sold ticket with QR. A result of zero
rows means no send was authorized; do not weaken the predicate. The update sets
only `max_attempts=6`, `status='retrying'`, and `next_attempt_at=now()` on the
matching job.

## Verify and stop conditions

After one requeue, require one sixth attempt, SES message ID, `accepted` then
`delivered` feedback, and the buyer's confirmation that the ticket email shows
its QR. Keep the original order, payment, ticket, first five attempts, backup,
and snapshot. If SES times out or the job exhausts at six, do not send again;
inspect the provider and inbox before deciding a separate recovery.

Rollback before requeue: restore only the previous Worker image; retain the
forward-compatible migration. After requeue, stopping only the Worker is the
safe containment action if it becomes unhealthy. Do not roll back the schema
or clear attempt rows once a sixth attempt exists.
