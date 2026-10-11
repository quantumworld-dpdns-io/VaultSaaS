# Runbook — webhook dead-letter queue

Triggered by: the CloudWatch alarm `vaultops-<env>-webhook-dlq-not-empty`, or the in-app alerts `webhook_dead_lettered` / `dlq_backlog`.

1. Find the cause. Dashboard → Security alerts, or:
   `SELECT status, last_error, count(*) FROM app.webhook_events WHERE received_at > now() - interval '24 hours' GROUP BY 1, 2;`
   - `bitgo status 401` → the org's BitGo token expired or was revoked. Ask the org admin to reconnect.
   - `bitgo status 5xx` / timeouts → BitGo outage. Wait for recovery.
   - anything else → check the worker logs (CloudWatch, 7-day retention).
2. After fixing the cause, redrive the messages to the main queue:
   ```bash
   aws sqs start-message-move-task \
     --source-arn arn:aws:sqs:ap-northeast-1:<acct>:vaultops-prod-webhook-events-dlq
   ```
   Re-processing is safe: deliveries are de-duplicated by body hash and transfers by `(provider, wallet, transfer)`.
3. Even without a redrive, the next scheduled reconciliation (every 15 min) backfills missed transfers (`missed_webhook` findings).
4. Resolve the alerts in the dashboard once the queue is empty.
