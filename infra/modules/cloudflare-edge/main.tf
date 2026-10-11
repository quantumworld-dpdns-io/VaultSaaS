# Cloudflare edge resources owned by Terraform (plan §3.1). Worker code and
# secrets are deployed by wrangler; this module only attaches the Worker to
# its hostname, so `wrangler deploy` must have run once before apply when
# attach_worker_domain = true.

terraform {
  required_version = ">= 1.10"
  required_providers {
    cloudflare = { source = "cloudflare/cloudflare", version = "~> 5.0" }
  }
}

locals {
  prefix = "vaultops-${var.env}"
  # Rulesets are zone-wide (one entrypoint per phase), so they cover every
  # environment's API host and are owned by a single environment.
  api_hosts = distinct(concat([var.api_hostname], var.extra_api_hostnames))
  api_is    = "http.host in {${join(" ", [for h in local.api_hosts : "\"${h}\""])}}"
}

# ---- R2 ---------------------------------------------------------------------

resource "cloudflare_r2_bucket" "this" {
  for_each   = toset(["reports", "audit-anchor", "backups"])
  account_id = var.account_id
  name       = "${local.prefix}-${each.key}"
  location   = var.r2_location
}

resource "cloudflare_r2_bucket_lifecycle" "reports" {
  account_id  = var.account_id
  bucket_name = cloudflare_r2_bucket.this["reports"].name
  rules = [{
    id         = "expire-reports"
    enabled    = true
    conditions = { prefix = "" }
    delete_objects_transition = {
      condition = { type = "Age", max_age = var.report_retention_days * 86400 }
    }
  }]
}

resource "cloudflare_r2_bucket_lifecycle" "backups" {
  account_id  = var.account_id
  bucket_name = cloudflare_r2_bucket.this["backups"].name
  rules = [{
    id         = "expire-backups"
    enabled    = true
    conditions = { prefix = "" }
    delete_objects_transition = {
      condition = { type = "Age", max_age = var.backup_retention_days * 86400 }
    }
  }]
}

# Off-database tamper-evidence anchor for the audit hash chain (plan §4):
# objects cannot be deleted or overwritten until the lock period expires.
resource "cloudflare_r2_bucket_lock" "audit_anchor" {
  account_id  = var.account_id
  bucket_name = cloudflare_r2_bucket.this["audit-anchor"].name
  rules = [{
    id        = "retain-audit-anchors"
    enabled   = true
    prefix    = ""
    condition = { type = "Age", max_age_seconds = var.audit_lock_days * 86400 }
  }]
}

# ---- Worker hostname ---------------------------------------------------------

resource "cloudflare_workers_custom_domain" "api" {
  count      = var.attach_worker_domain ? 1 : 0
  account_id = var.account_id
  zone_id    = var.zone_id
  hostname   = var.api_hostname
  service    = var.worker_script_name
}

# ---- Pages (dashboard) -------------------------------------------------------

resource "cloudflare_pages_project" "web" {
  account_id        = var.account_id
  name              = "${local.prefix}-web"
  production_branch = "main"
  # Direct upload from CI (`wrangler pages deploy apps/web/dist`); no Git
  # integration, so the build never sees repository secrets.
}

resource "cloudflare_pages_domain" "web" {
  account_id   = var.account_id
  project_name = cloudflare_pages_project.web.name
  name         = var.app_hostname
}

resource "cloudflare_dns_record" "web" {
  zone_id = var.zone_id
  name    = var.app_hostname
  type    = "CNAME"
  content = "${cloudflare_pages_project.web.name}.pages.dev"
  proxied = true
  ttl     = 1
  comment = "VaultOps dashboard (${var.env})"
}

# ---- WAF custom rules & rate limiting ----------------------------------------
# One entrypoint ruleset per phase per zone. Free plan: 5 custom rules,
# 1 rate-limiting rule.

resource "cloudflare_ruleset" "firewall" {
  count   = var.manage_zone_rulesets ? 1 : 0
  zone_id = var.zone_id
  name    = "${local.prefix} custom firewall"
  kind    = "zone"
  phase   = "http_request_firewall_custom"
  rules = concat(
    [{
      description = "Webhook ingress accepts POST only"
      expression  = "${local.api_is} and starts_with(http.request.uri.path, \"/webhooks/\") and http.request.method ne \"POST\""
      action      = "block"
      enabled     = true
    }],
    length(var.bitgo_source_cidrs) == 0 ? [] : [{
      description = "Webhook ingress only from BitGo source ranges"
      expression  = "${local.api_is} and starts_with(http.request.uri.path, \"/webhooks/\") and not ip.src in {${join(" ", var.bitgo_source_cidrs)}}"
      action      = "block"
      enabled     = true
    }],
  )
}

resource "cloudflare_ruleset" "ratelimit" {
  count   = var.manage_zone_rulesets ? 1 : 0
  zone_id = var.zone_id
  name    = "${local.prefix} rate limits"
  kind    = "zone"
  phase   = "http_ratelimit"
  rules = [{
    description = "Per-IP limit on the API host"
    expression  = local.api_is
    action      = "block"
    enabled     = true
    ratelimit = {
      characteristics     = ["ip.src", "cf.colo.id"]
      period              = 10
      requests_per_period = var.rate_limit_per_10s
      mitigation_timeout  = 10
    }
  }]
}

# ---- Cloudflare Access for /admin --------------------------------------------

resource "cloudflare_zero_trust_access_policy" "admins" {
  count      = length(var.admin_emails) == 0 ? 0 : 1
  account_id = var.account_id
  name       = "${local.prefix} admins"
  decision   = "allow"
  include    = [for e in var.admin_emails : { email = { email = e } }]
}

resource "cloudflare_zero_trust_access_application" "admin" {
  count            = length(var.admin_emails) == 0 ? 0 : 1
  account_id       = var.account_id
  name             = "${local.prefix} admin"
  type             = "self_hosted"
  domain           = "${var.api_hostname}/admin"
  session_duration = "8h"
  policies         = [{ id = cloudflare_zero_trust_access_policy.admins[0].id, precedence = 1 }]
}
