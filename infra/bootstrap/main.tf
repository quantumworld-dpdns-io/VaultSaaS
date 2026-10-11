# One-time bootstrap, applied locally with personal admin credentials.
# Creates the GitHub Actions OIDC provider and the three CI roles (plan §3.1):
#   vaultops-tf-plan   read-only, assumable from pull requests
#   vaultops-tf-apply  scoped write, assumable only from the protected "prod"/"dev" environments
#   vaultops-deploy    lambda:UpdateFunctionCode only, assumable from the dev/prod environments
# Everything here is IAM, which has no charge (principle 0).

terraform {
  required_version = ">= 1.10"
  required_providers {
    aws = { source = "hashicorp/aws", version = "~> 6.0" }
  }
  backend "s3" {
    key                         = "bootstrap/terraform.tfstate"
    region                      = "auto"
    use_lockfile                = true
    skip_credentials_validation = true
    skip_region_validation      = true
    skip_requesting_account_id  = true
    skip_metadata_api_check     = true
    skip_s3_checksum            = true
    use_path_style              = true
    # bucket + endpoints come from backend.r2.hcl (see ../backend.r2.hcl.example)
  }
}

provider "aws" {
  region = var.region
  default_tags { tags = { project = "vaultops", managed_by = "terraform", stack = "bootstrap" } }
}

variable "region" {
  type    = string
  default = "ap-northeast-1"
}

variable "github_repository" {
  description = "owner/name of the GitHub repository allowed to assume the CI roles"
  type        = string
  validation {
    condition     = can(regex("^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$", var.github_repository))
    error_message = "Use the form owner/name."
  }
}

data "aws_caller_identity" "current" {}

locals {
  account_id = data.aws_caller_identity.current.account_id
  oidc_host  = "token.actions.githubusercontent.com"
  repo_sub   = "repo:${var.github_repository}"
}

resource "aws_iam_openid_connect_provider" "github" {
  url            = "https://${local.oidc_host}"
  client_id_list = ["sts.amazonaws.com"]
}

data "aws_iam_policy_document" "trust" {
  for_each = {
    plan   = ["${local.repo_sub}:pull_request", "${local.repo_sub}:ref:refs/heads/main"]
    apply  = ["${local.repo_sub}:environment:dev", "${local.repo_sub}:environment:prod"]
    deploy = ["${local.repo_sub}:environment:dev", "${local.repo_sub}:environment:prod"]
  }
  statement {
    actions = ["sts:AssumeRoleWithWebIdentity"]
    principals {
      type        = "Federated"
      identifiers = [aws_iam_openid_connect_provider.github.arn]
    }
    condition {
      test     = "StringEquals"
      variable = "${local.oidc_host}:aud"
      values   = ["sts.amazonaws.com"]
    }
    condition {
      test     = "StringEquals"
      variable = "${local.oidc_host}:sub"
      values   = each.value
    }
  }
}

resource "aws_iam_role" "ci" {
  for_each             = data.aws_iam_policy_document.trust
  name                 = "vaultops-${each.key == "plan" ? "tf-plan" : each.key == "apply" ? "tf-apply" : "deploy"}"
  assume_role_policy   = each.value.json
  max_session_duration = 3600
}

resource "aws_iam_role_policy_attachment" "plan_readonly" {
  role       = aws_iam_role.ci["plan"].name
  policy_arn = "arn:aws:iam::aws:policy/ReadOnlyAccess"
}

# Apply role: may only manage vaultops-* resources of the services allowed by
# principle 0. Creating anything else (S3, API Gateway, EC2, …) is denied by
# omission, in addition to the plan-time policy check.
data "aws_iam_policy_document" "apply" {
  statement {
    sid = "ScopedServices"
    actions = [
      "lambda:*", "sqs:*", "scheduler:*", "sns:*",
      "logs:*", "cloudwatch:*",
    ]
    resources = [
      "arn:aws:lambda:${var.region}:${local.account_id}:function:vaultops-*",
      "arn:aws:sqs:${var.region}:${local.account_id}:vaultops-*",
      "arn:aws:scheduler:${var.region}:${local.account_id}:schedule/default/vaultops-*",
      "arn:aws:sns:${var.region}:${local.account_id}:vaultops-*",
      "arn:aws:logs:${var.region}:${local.account_id}:log-group:/aws/lambda/vaultops-*",
      "arn:aws:logs:${var.region}:${local.account_id}:log-group:/aws/lambda/vaultops-*:*",
      "arn:aws:cloudwatch:${var.region}:${local.account_id}:alarm:vaultops-*",
    ]
  }
  statement {
    sid       = "ScopedSSM"
    actions   = ["ssm:PutParameter", "ssm:DeleteParameter", "ssm:AddTagsToResource", "ssm:RemoveTagsFromResource", "ssm:GetParameter*", "ssm:ListTagsForResource"]
    resources = ["arn:aws:ssm:${var.region}:${local.account_id}:parameter/vaultops/*"]
  }
  statement {
    sid = "ScopedIAM"
    actions = [
      "iam:CreateRole", "iam:DeleteRole", "iam:GetRole", "iam:UpdateRole", "iam:TagRole", "iam:UntagRole",
      "iam:PutRolePolicy", "iam:DeleteRolePolicy", "iam:GetRolePolicy", "iam:ListRolePolicies", "iam:ListAttachedRolePolicies",
      "iam:ListInstanceProfilesForRole", "iam:PassRole", "iam:UpdateAssumeRolePolicy",
      "iam:CreateUser", "iam:DeleteUser", "iam:GetUser", "iam:TagUser", "iam:UntagUser",
      "iam:PutUserPolicy", "iam:DeleteUserPolicy", "iam:GetUserPolicy", "iam:ListUserPolicies", "iam:ListAttachedUserPolicies",
      "iam:ListAccessKeys", "iam:ListGroupsForUser",
    ]
    resources = [
      "arn:aws:iam::${local.account_id}:role/vaultops-*",
      "arn:aws:iam::${local.account_id}:user/vaultops-*",
    ]
  }
  statement {
    sid       = "Budgets"
    actions   = ["budgets:ViewBudget", "budgets:ModifyBudget", "budgets:ListTagsForResource", "budgets:TagResource", "budgets:UntagResource"]
    resources = ["arn:aws:budgets::${local.account_id}:budget/vaultops-*"]
  }
  statement {
    sid = "ReadForRefresh"
    actions = [
      "lambda:List*", "lambda:GetAccountSettings", "sqs:ListQueues", "logs:Describe*", "cloudwatch:Describe*",
      "ssm:DescribeParameters", "sns:ListTopics", "scheduler:ListSchedules", "sts:GetCallerIdentity", "iam:ListRoles",
    ]
    resources = ["*"]
  }
  statement {
    sid    = "DenyCredentialMinting"
    effect = "Deny"
    # Edge access keys are created by hand so they never reach Terraform state.
    actions   = ["iam:CreateAccessKey", "iam:UpdateAccessKey"]
    resources = ["*"]
  }
}

resource "aws_iam_role_policy" "apply" {
  role   = aws_iam_role.ci["apply"].id
  name   = "vaultops-scoped-apply"
  policy = data.aws_iam_policy_document.apply.json
}

data "aws_iam_policy_document" "deploy" {
  statement {
    actions   = ["lambda:UpdateFunctionCode", "lambda:GetFunction", "lambda:PublishVersion"]
    resources = ["arn:aws:lambda:${var.region}:${local.account_id}:function:vaultops-*"]
  }
}

resource "aws_iam_role_policy" "deploy" {
  role   = aws_iam_role.ci["deploy"].id
  name   = "vaultops-deploy-code"
  policy = data.aws_iam_policy_document.deploy.json
}

output "role_arns" {
  value = { for k, r in aws_iam_role.ci : k => r.arn }
}
