# IAM user for the Cloudflare Worker — the only long-lived AWS credential in
# the system (plan §2, threat model). Terraform creates the user and its
# policy but never an access key: keys are minted by hand and piped straight
# into `wrangler secret put` so they never reach Terraform state.

terraform {
  required_version = ">= 1.10"
  required_providers {
    aws = { source = "hashicorp/aws", version = "~> 6.0" }
  }
}

variable "env" { type = string }
variable "api_function_arn" { type = string }
variable "webhook_queue_arn" { type = string }

resource "aws_iam_user" "edge" {
  name = "vaultops-${var.env}-edge"
  tags = { purpose = "cloudflare-worker", rotate_every_days = "90" }
}

data "aws_iam_policy_document" "edge" {
  statement {
    sid       = "InvokeCoreViaFunctionUrl"
    actions   = ["lambda:InvokeFunctionUrl"]
    resources = [var.api_function_arn]
    condition {
      test     = "StringEquals"
      variable = "lambda:FunctionUrlAuthType"
      values   = ["AWS_IAM"]
    }
  }
  statement {
    # Function URLs created since late 2025 also require lambda:InvokeFunction
    # on the same function; still scoped to that single ARN.
    sid       = "InvokeCore"
    actions   = ["lambda:InvokeFunction"]
    resources = [var.api_function_arn]
  }
  statement {
    sid       = "EnqueueWebhookEvents"
    actions   = ["sqs:SendMessage"]
    resources = [var.webhook_queue_arn]
  }
}

resource "aws_iam_user_policy" "edge" {
  user   = aws_iam_user.edge.name
  name   = "edge-least-privilege"
  policy = data.aws_iam_policy_document.edge.json
}

output "user_name" {
  value = aws_iam_user.edge.name
}
