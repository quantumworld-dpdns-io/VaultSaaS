terraform {
  required_version = ">= 1.10"
  required_providers {
    aws        = { source = "hashicorp/aws", version = "~> 6.0" }
    cloudflare = { source = "cloudflare/cloudflare", version = "~> 5.0" }
    archive    = { source = "hashicorp/archive", version = "~> 2.7" }
  }
}

locals {
  env = "prod"
}

provider "aws" {
  region              = "ap-northeast-1" # Tokyo: closest region to Taiwan customers (D6)
  allowed_account_ids = [var.aws_account_id]
  default_tags {
    tags = { project = "vaultops", env = local.env, managed_by = "terraform" }
  }
}

# CLOUDFLARE_API_TOKEN comes from the environment (scoped token, plan §3.1).
provider "cloudflare" {}

module "aws_core" {
  source         = "../../modules/aws-core"
  env            = local.env
  aws_account_id = var.aws_account_id
  # Stays on BitGo testnet until Go/No-Go gate 2 (commercial terms) passes.
  bitgo_base_url = var.bitgo_base_url
  alert_email    = var.alert_email
  create_budgets = var.create_budgets
}

module "aws_edge_access" {
  source            = "../../modules/aws-edge-access"
  env               = local.env
  api_function_arn  = module.aws_core.api_function_arn
  webhook_queue_arn = module.aws_core.webhook_queue_arn
}

module "cloudflare_edge" {
  source               = "../../modules/cloudflare-edge"
  env                  = local.env
  account_id           = var.cloudflare_account_id
  zone_id              = var.cloudflare_zone_id
  app_hostname         = "app.${var.domain}"
  api_hostname         = "api.${var.domain}"
  worker_script_name   = "vaultops-edge-${local.env}"
  attach_worker_domain = var.attach_worker_domain
  manage_zone_rulesets = true
  extra_api_hostnames  = ["api-dev.${var.domain}"]
  bitgo_source_cidrs   = var.bitgo_source_cidrs
  admin_emails         = var.admin_emails
}
