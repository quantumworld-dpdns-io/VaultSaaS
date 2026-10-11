# Principle 0: AWS must never use a paid service (plan §1, §3.1).
# Evaluated by conftest against `terraform show -json <plan>` for every
# environment, in CI (infra-plan) and in `make policy`.
package main

import rego.v1

# Resource types whose usage stays inside AWS Always Free at VaultOps' scale.
aws_allowed := {
	"aws_iam_role",
	"aws_iam_role_policy",
	"aws_iam_role_policy_attachment",
	"aws_iam_user",
	"aws_iam_user_policy",
	"aws_iam_openid_connect_provider",
	"aws_lambda_function",
	"aws_lambda_function_url",
	"aws_lambda_permission",
	"aws_lambda_event_source_mapping",
	"aws_sqs_queue",
	"aws_sqs_queue_redrive_policy",
	"aws_scheduler_schedule",
	"aws_ssm_parameter",
	"aws_cloudwatch_log_group",
	"aws_cloudwatch_metric_alarm",
	"aws_sns_topic",
	"aws_sns_topic_subscription",
	"aws_budgets_budget",
}

# Called out explicitly so the failure message explains why.
aws_forbidden_reason := {
	"aws_api_gateway_rest_api": "API Gateway is only free for 12 months; use the Cloudflare Worker + Function URL",
	"aws_apigatewayv2_api": "API Gateway is only free for 12 months; use the Cloudflare Worker + Function URL",
	"aws_s3_bucket": "S3 is only free for 12 months; use Cloudflare R2",
	"aws_cloudtrail": "a trail writes to S3; use the free 90-day CloudTrail Event history",
	"aws_nat_gateway": "NAT Gateway is billed hourly",
	"aws_instance": "EC2 is not Always Free",
	"aws_db_instance": "RDS is not Always Free; the database is Supabase",
	"aws_secretsmanager_secret": "Secrets Manager is billed per secret; use SSM SecureString",
	"aws_kms_key": "customer managed KMS keys cost $1/month; use the aws/ssm managed key",
	"aws_vpc_endpoint": "VPC endpoints are billed hourly",
	"aws_lambda_provisioned_concurrency_config": "provisioned concurrency is billed",
	"aws_iam_access_key": "access keys must never be stored in Terraform state; mint them by hand",
}

# Worker code and secrets are owned by wrangler so secrets stay out of state.
cloudflare_forbidden_reason := {
	"cloudflare_workers_script": "Worker code and secret bindings are deployed by wrangler, not Terraform",
	"cloudflare_workers_secret": "Worker secrets are set with `wrangler secret put`, never via Terraform",
}

managed contains rc if {
	some rc in input.resource_changes
	rc.mode == "managed"
	not "delete" in rc.change.actions
}

after(rc) := object.get(rc.change, "after", {})

deny contains msg if {
	some rc in managed
	startswith(rc.type, "aws_")
	reason := aws_forbidden_reason[rc.type]
	msg := sprintf("%s: %s is forbidden — %s", [rc.address, rc.type, reason])
}

deny contains msg if {
	some rc in managed
	startswith(rc.type, "aws_")
	not aws_allowed[rc.type]
	not aws_forbidden_reason[rc.type]
	msg := sprintf("%s: %s is not on the Always Free allowlist (infra/policy/free_only.rego)", [rc.address, rc.type])
}

deny contains msg if {
	some rc in managed
	reason := cloudflare_forbidden_reason[rc.type]
	msg := sprintf("%s: %s", [rc.address, reason])
}

# ---- attribute rules ----------------------------------------------------

deny contains msg if {
	some rc in managed
	rc.type == "aws_ssm_parameter"
	after(rc).tier != "Standard"
	msg := sprintf("%s: SSM parameters must use tier Standard (Advanced is billed)", [rc.address])
}

deny contains msg if {
	some rc in managed
	rc.type == "aws_ssm_parameter"
	key := object.get(after(rc), "key_id", null)
	key != null
	key != "alias/aws/ssm"
	msg := sprintf("%s: SecureString must use the AWS managed aws/ssm key, not %v", [rc.address, key])
}

deny contains msg if {
	some rc in managed
	rc.type == "aws_cloudwatch_log_group"
	days := object.get(after(rc), "retention_in_days", 0)
	not retention_ok(days)
	msg := sprintf("%s: log retention must be set and at most 7 days (got %v)", [rc.address, days])
}

retention_ok(days) if {
	days > 0
	days <= 7
}

deny contains msg if {
	some rc in managed
	rc.type == "aws_lambda_function"
	after(rc).architectures != ["arm64"]
	msg := sprintf("%s: Lambda must run on arm64", [rc.address])
}

deny contains msg if {
	some rc in managed
	rc.type == "aws_lambda_function"
	after(rc).memory_size > 256
	msg := sprintf("%s: Lambda memory must be <= 256 MB", [rc.address])
}

deny contains msg if {
	some rc in managed
	rc.type == "aws_lambda_function"
	after(rc).timeout > 30
	msg := sprintf("%s: Lambda timeout must be <= 30 s", [rc.address])
}

deny contains msg if {
	some rc in managed
	rc.type == "aws_lambda_function"
	count(object.get(after(rc), "vpc_config", [])) > 0
	msg := sprintf("%s: Lambda must not join a VPC (would need a NAT gateway)", [rc.address])
}

deny contains msg if {
	some rc in managed
	rc.type == "aws_lambda_function"
	some t in object.get(after(rc), "tracing_config", [])
	t.mode == "Active"
	msg := sprintf("%s: active X-Ray tracing is not allowed", [rc.address])
}

deny contains msg if {
	some rc in managed
	rc.type == "aws_lambda_event_source_mapping"
	not concurrency_capped(after(rc))
	msg := sprintf("%s: SQS event source must set scaling_config.maximum_concurrency = 2", [rc.address])
}

concurrency_capped(a) if {
	some sc in a.scaling_config
	sc.maximum_concurrency == 2
}

# The free tier allows 10 alarms per account, shared by dev and prod.
deny contains msg if {
	n := count([rc | some rc in managed; rc.type == "aws_cloudwatch_metric_alarm"])
	n > 5
	msg := sprintf("%d CloudWatch alarms in one environment; keep <= 5 so dev + prod stay within the free 10", [n])
}

deny contains msg if {
	n := count([rc | some rc in managed; rc.type == "aws_budgets_budget"])
	n > 2
	msg := sprintf("%d budgets; at most 2 are free", [n])
}
