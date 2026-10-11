# Core AWS resources. Every resource type here is on the principle-0 allowlist
# (infra/policy/free_only.rego): Lambda, SQS, EventBridge Scheduler, SSM
# Standard parameters, CloudWatch Logs/alarms, SNS (email), Budgets, IAM.

terraform {
  required_version = ">= 1.10"
  required_providers {
    aws     = { source = "hashicorp/aws", version = "~> 6.0" }
    archive = { source = "hashicorp/archive", version = "~> 2.7" }
  }
}

data "aws_region" "current" {}

locals {
  prefix     = "vaultops-${var.env}"
  ssm_prefix = "/vaultops/${var.env}"
  region     = data.aws_region.current.region
  account_id = var.aws_account_id

  functions = {
    api = {
      timeout     = 10
      description = "HTTP API behind the Function URL"
    }
    worker = {
      timeout     = 30
      description = "SQS consumer: verifies webhook events against BitGo and books transfers"
    }
    recon = {
      timeout     = 30
      description = "Scheduled reconciliation"
    }
  }

  # Secrets are SecureString shells; values are written out-of-band with
  # `aws ssm put-parameter --overwrite` so they never enter Terraform state.
  # Each organization stores its own BitGo token (sealed with token-enc-keys);
  # there is deliberately no shared BitGo token in deployed environments.
  secure_parameters = {
    "database-url"   = "Supabase Supavisor pooler URL (transaction mode, port 6543) for role vaultops_app"
    "token-enc-keys" = "AES-256 keyring for sealing stored BitGo tokens: 1:<base64>,2:<base64>"
    # R2 API token scoped to the audit-anchor and reports buckets (object read/write only)
    "r2-access-key-id"     = "R2 access key id for the audit-anchor and reports buckets"
    "r2-secret-access-key" = "R2 secret access key for the audit-anchor and reports buckets"
  }

  common_env = {
    VAULTOPS_ENV       = var.env
    BITGO_BASE_URL     = var.bitgo_base_url
    DATABASE_URL_SSM   = "${local.ssm_prefix}/database-url"
    TOKEN_ENC_KEYS_SSM = "${local.ssm_prefix}/token-enc-keys"
    SIWE_DOMAIN        = var.siwe_domain
    # Audit anchors go to Cloudflare R2 (bucket lock), not S3 — principle 0.
    R2_ENDPOINT              = var.r2_endpoint
    R2_AUDIT_BUCKET          = var.r2_audit_bucket
    R2_REPORTS_BUCKET        = var.r2_reports_bucket
    R2_ACCESS_KEY_ID_SSM     = "${local.ssm_prefix}/r2-access-key-id"
    R2_SECRET_ACCESS_KEY_SSM = "${local.ssm_prefix}/r2-secret-access-key"
  }
}

# ---- SSM parameters ---------------------------------------------------------

resource "aws_ssm_parameter" "secure" {
  for_each    = local.secure_parameters
  name        = "${local.ssm_prefix}/${each.key}"
  description = each.value
  type        = "SecureString"
  tier        = "Standard" # Advanced parameters are billed
  # key_id intentionally omitted: the AWS managed aws/ssm key has no monthly fee.
  value = "CHANGE_ME"
  lifecycle {
    ignore_changes = [value]
  }
}

# ---- Lambda -----------------------------------------------------------------

data "archive_file" "placeholder" {
  type                    = "zip"
  output_path             = "${path.module}/.placeholder.zip"
  source_content          = "placeholder: real code is deployed by CI (deploy-core)"
  source_content_filename = "bootstrap"
}

resource "aws_cloudwatch_log_group" "fn" {
  for_each          = local.functions
  name              = "/aws/lambda/${local.prefix}-${each.key}"
  retention_in_days = var.log_retention_days
}

data "aws_iam_policy_document" "lambda_trust" {
  statement {
    actions = ["sts:AssumeRole"]
    principals {
      type        = "Service"
      identifiers = ["lambda.amazonaws.com"]
    }
  }
}

resource "aws_iam_role" "fn" {
  for_each           = local.functions
  name               = "${local.prefix}-${each.key}"
  assume_role_policy = data.aws_iam_policy_document.lambda_trust.json
}

data "aws_iam_policy_document" "fn" {
  for_each = local.functions
  statement {
    sid       = "Logs"
    actions   = ["logs:CreateLogStream", "logs:PutLogEvents"]
    resources = ["${aws_cloudwatch_log_group.fn[each.key].arn}:*"]
  }
  statement {
    sid       = "ReadSecrets"
    actions   = ["ssm:GetParameter"]
    resources = [for p in aws_ssm_parameter.secure : p.arn]
  }
  dynamic "statement" {
    for_each = each.key == "worker" ? [1] : []
    content {
      sid       = "ConsumeQueue"
      actions   = ["sqs:ReceiveMessage", "sqs:DeleteMessage", "sqs:GetQueueAttributes", "sqs:ChangeMessageVisibility"]
      resources = [aws_sqs_queue.webhook.arn]
    }
  }
}

resource "aws_iam_role_policy" "fn" {
  for_each = local.functions
  role     = aws_iam_role.fn[each.key].id
  name     = "least-privilege"
  policy   = data.aws_iam_policy_document.fn[each.key].json
}

resource "aws_lambda_function" "fn" {
  for_each      = local.functions
  function_name = "${local.prefix}-${each.key}"
  description   = each.value.description
  role          = aws_iam_role.fn[each.key].arn
  runtime       = "provided.al2023"
  handler       = "bootstrap"
  architectures = ["arm64"]
  memory_size   = var.lambda_memory_mb
  timeout       = each.value.timeout
  filename      = data.archive_file.placeholder.output_path

  environment {
    variables = local.common_env
  }

  logging_config {
    log_format = "JSON"
    log_group  = aws_cloudwatch_log_group.fn[each.key].name
  }

  # No VPC (would need a NAT gateway), no provisioned concurrency, no active
  # X-Ray tracing — all billed (principle 0).

  lifecycle {
    ignore_changes = [filename, source_code_hash] # code ships via CI deploy-core
  }
  depends_on = [aws_iam_role_policy.fn]
}

resource "aws_lambda_function_url" "api" {
  function_name      = aws_lambda_function.fn["api"].function_name
  authorization_type = "AWS_IAM" # only the edge IAM user may invoke
}

# ---- SQS --------------------------------------------------------------------

resource "aws_sqs_queue" "webhook_dlq" {
  name                      = "${local.prefix}-webhook-events-dlq"
  message_retention_seconds = 1209600 # 14 days
  sqs_managed_sse_enabled   = true
}

resource "aws_sqs_queue" "webhook" {
  name                       = "${local.prefix}-webhook-events"
  visibility_timeout_seconds = 6 * local.functions.worker.timeout
  message_retention_seconds  = 345600 # 4 days
  receive_wait_time_seconds  = 20
  sqs_managed_sse_enabled    = true
  redrive_policy = jsonencode({
    deadLetterTargetArn = aws_sqs_queue.webhook_dlq.arn
    maxReceiveCount     = 5
  })
}

resource "aws_lambda_event_source_mapping" "worker" {
  event_source_arn        = aws_sqs_queue.webhook.arn
  function_name           = aws_lambda_function.fn["worker"].arn
  batch_size              = 10
  function_response_types = ["ReportBatchItemFailures"]
  scaling_config {
    maximum_concurrency = 2 # caps runaway invocations; minimum value AWS allows
  }
}

# ---- EventBridge Scheduler --------------------------------------------------

data "aws_iam_policy_document" "scheduler_trust" {
  statement {
    actions = ["sts:AssumeRole"]
    principals {
      type        = "Service"
      identifiers = ["scheduler.amazonaws.com"]
    }
    condition {
      test     = "StringEquals"
      variable = "aws:SourceAccount"
      values   = [local.account_id]
    }
  }
}

resource "aws_iam_role" "scheduler" {
  name               = "${local.prefix}-scheduler"
  assume_role_policy = data.aws_iam_policy_document.scheduler_trust.json
}

resource "aws_iam_role_policy" "scheduler" {
  role = aws_iam_role.scheduler.id
  name = "invoke-recon"
  policy = jsonencode({
    Version   = "2012-10-17"
    Statement = [{ Effect = "Allow", Action = "lambda:InvokeFunction", Resource = aws_lambda_function.fn["recon"].arn }]
  })
}

resource "aws_scheduler_schedule" "recon" {
  for_each = {
    reconcile    = { expr = "rate(${var.recon_interval_minutes} minutes)", input = { task = "reconcile" } }
    daily-report = { expr = "cron(5 0 * * ? *)", input = { task = "daily_report" } }
    audit-anchor = { expr = "cron(15 0 * * ? *)", input = { task = "audit_anchor" } }
  }
  name                         = "${local.prefix}-${each.key}"
  schedule_expression          = each.value.expr
  schedule_expression_timezone = "Asia/Taipei"
  flexible_time_window {
    mode = "OFF"
  }
  target {
    arn      = aws_lambda_function.fn["recon"].arn
    role_arn = aws_iam_role.scheduler.arn
    input    = jsonencode(each.value.input)
    retry_policy {
      maximum_retry_attempts = 2
    }
  }
}

# ---- Alerting (CloudWatch alarms ≤ 10, SNS email) ---------------------------

resource "aws_sns_topic" "alerts" {
  name = "${local.prefix}-alerts"
}

resource "aws_sns_topic_subscription" "email" {
  count     = var.alert_email == "" ? 0 : 1
  topic_arn = aws_sns_topic.alerts.arn
  protocol  = "email"
  endpoint  = var.alert_email
}

resource "aws_cloudwatch_metric_alarm" "dlq_not_empty" {
  alarm_name          = "${local.prefix}-webhook-dlq-not-empty"
  alarm_description   = "Webhook events failed 5 times and were dead-lettered"
  namespace           = "AWS/SQS"
  metric_name         = "ApproximateNumberOfMessagesVisible"
  dimensions          = { QueueName = aws_sqs_queue.webhook_dlq.name }
  statistic           = "Maximum"
  period              = 300
  evaluation_periods  = 1
  threshold           = 0
  comparison_operator = "GreaterThanThreshold"
  treat_missing_data  = "notBreaching"
  alarm_actions       = [aws_sns_topic.alerts.arn]
}

resource "aws_cloudwatch_metric_alarm" "fn_errors" {
  for_each            = local.functions
  alarm_name          = "${local.prefix}-${each.key}-errors"
  namespace           = "AWS/Lambda"
  metric_name         = "Errors"
  dimensions          = { FunctionName = aws_lambda_function.fn[each.key].function_name }
  statistic           = "Sum"
  period              = 300
  evaluation_periods  = 1
  threshold           = 0
  comparison_operator = "GreaterThanThreshold"
  treat_missing_data  = "notBreaching"
  alarm_actions       = [aws_sns_topic.alerts.arn]
}

# ---- Budgets (max 2) -------------------------------------------------------

resource "aws_budgets_budget" "spend" {
  for_each = var.create_budgets ? {
    zero-spend = "0.01"
    cap        = tostring(var.monthly_budget_usd)
  } : {}
  name         = "vaultops-${each.key}"
  budget_type  = "COST"
  limit_amount = each.value
  limit_unit   = "USD"
  time_unit    = "MONTHLY"

  notification {
    comparison_operator        = "GREATER_THAN"
    threshold                  = 100
    threshold_type             = "PERCENTAGE"
    notification_type          = "ACTUAL"
    subscriber_email_addresses = var.alert_email == "" ? [] : [var.alert_email]
    subscriber_sns_topic_arns  = var.alert_email == "" ? [aws_sns_topic.alerts.arn] : []
  }
}
