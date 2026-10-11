export interface Env {
  ENV: "local" | "dev" | "prod";
  CORE_URL: string;
  SQS_QUEUE_URL: string;
  AWS_REGION: string;
  ALLOWED_ORIGINS: string;
  SUPABASE_ISSUER: string; // https://<project>.supabase.co/auth/v1
  SUPABASE_JWKS_URL: string; // <issuer>/.well-known/jwks.json
  // secrets
  AWS_ACCESS_KEY_ID?: string;
  AWS_SECRET_ACCESS_KEY?: string;
}
