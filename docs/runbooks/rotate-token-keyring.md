# Runbook — rotate the BitGo token keyring (TOKEN_ENC_KEYS)

Stored BitGo tokens are sealed with the newest key in `/vaultops/<env>/token-enc-keys` (`1:<b64>,2:<b64>`). Old versions stay readable, so rotation is additive.

1. Generate a key: `openssl rand -base64 32`
2. Append it as the next version (keep the old ones):
   `aws ssm put-parameter --name /vaultops/prod/token-enc-keys --type SecureString --overwrite --value "1:<old>,2:<new>"`
3. Lambdas pick it up on their next cold start; to force that, re-run `deploy-core`. New and updated connections now use v2.
4. Re-seal old connections by asking org admins to reconnect, or with a one-off script. Once no row has `key_version = 1`, drop v1 from the parameter.
