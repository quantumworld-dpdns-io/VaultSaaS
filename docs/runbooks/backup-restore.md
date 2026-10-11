# Runbook — database backup and monthly restore drill

`.github/workflows/db-backup.yml` runs daily. It takes a `pg_dump` (custom format) of the Supabase database, encrypts it with `age` to the ops public key, and uploads it to the R2 `vaultops-<env>-backups` bucket. A lifecycle rule removes it after 35 days. Only ciphertext ever leaves the runner.

## Monthly restore drill (≈20 min)

1. Download the latest backup:
   `aws s3 cp --endpoint-url https://<acct>.r2.cloudflarestorage.com --profile vaultops-r2 s3://vaultops-prod-backups/<file> .`
2. Decrypt locally with the ops private key: `age -d -i ops.key <file> > vaultops.dump`
3. Restore into a scratch database (never production):
   ```bash
   make local-up
   createdb -h 127.0.0.1 -p 54322 -U postgres restore_drill
   pg_restore -h 127.0.0.1 -p 54322 -U postgres -d restore_drill --no-owner vaultops.dump
   ```
4. Verify: the row counts of `app.transfers` and `app.audit_logs` are plausible, and the audit chain verifies for every org:
   `SELECT id, app.verify_audit_chain(id) FROM app.organizations;` (all NULL).
5. Securely delete the decrypted files. Record the date, backup age and result in the ops log.
