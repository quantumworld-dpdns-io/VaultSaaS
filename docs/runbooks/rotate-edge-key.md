# Runbook — rotate the edge IAM access key (every 90 days)

The Cloudflare Worker signs calls to the core Lambda and SQS with the IAM user
`vaultops-<env>-edge`. Terraform creates the user and policy but never the key,
so the key never enters Terraform state.

1. Create a second key. A user may hold two keys, so there is no downtime:
   ```bash
   KEY_JSON=$(aws iam create-access-key --user-name vaultops-prod-edge)
   ```
   *(If the user already has two keys, delete the older inactive one first.)*
2. Install the new key straight into the Worker, without echoing it:
   ```bash
   jq -r '.AccessKey.AccessKeyId'     <<<"$KEY_JSON" | npx wrangler secret put AWS_ACCESS_KEY_ID --env prod
   jq -r '.AccessKey.SecretAccessKey' <<<"$KEY_JSON" | npx wrangler secret put AWS_SECRET_ACCESS_KEY --env prod
   unset KEY_JSON
   ```
3. Verify: `curl -s https://api.<domain>/api/healthz` must return `"service":"core"`. Then send a test webhook and confirm it is queued (202).
4. Deactivate the old key, wait 24 h while watching the alarms, then delete it:
   ```bash
   aws iam list-access-keys --user-name vaultops-prod-edge
   aws iam update-access-key --user-name vaultops-prod-edge --access-key-id <OLD> --status Inactive
   aws iam delete-access-key --user-name vaultops-prod-edge --access-key-id <OLD>
   ```
5. Record the rotation date in the ops log.

**Suspected compromise:** do steps 1–3 immediately and **delete** (not just deactivate) the old key. Then check CloudTrail Event history (free, 90 days) for `Invoke*`/`SendMessage` from unexpected IPs.
