export interface Env {
  ENV: "local" | "dev" | "prod";
  CORE_URL: string;
  SQS_QUEUE_URL: string;
  AWS_REGION: string;
  ALLOWED_ORIGINS: string;
  // secrets
  AWS_ACCESS_KEY_ID?: string;
  AWS_SECRET_ACCESS_KEY?: string;
}
