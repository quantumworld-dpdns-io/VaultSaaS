variable "env" {
  type = string
}

variable "account_id" {
  type = string
}

variable "zone_id" {
  type = string
}

variable "app_hostname" {
  description = "Dashboard hostname, e.g. app.example.com"
  type        = string
}

variable "api_hostname" {
  description = "Worker hostname, e.g. api.example.com"
  type        = string
}

variable "worker_script_name" {
  type = string
}

variable "attach_worker_domain" {
  description = "Set false until the Worker has been deployed once with wrangler."
  type        = bool
  default     = true
}

variable "r2_location" {
  description = "R2 location hint; apac keeps data close to Taiwan/APAC customers (D6)."
  type        = string
  default     = "apac"
}

variable "report_retention_days" {
  type    = number
  default = 90
}

variable "backup_retention_days" {
  type    = number
  default = 35
}

variable "audit_lock_days" {
  type    = number
  default = 400
}

variable "bitgo_source_cidrs" {
  description = "BitGo webhook source ranges, if BitGo publishes them (Week 0 check). Empty disables the rule."
  type        = list(string)
  default     = []
}

variable "rate_limit_per_10s" {
  type    = number
  default = 50
}

variable "admin_emails" {
  type    = list(string)
  default = []
}

variable "manage_zone_rulesets" {
  description = "Zone-wide WAF/rate-limit rulesets; exactly one environment per zone sets this."
  type        = bool
  default     = false
}

variable "extra_api_hostnames" {
  description = "Other environments' API hosts in the same zone, covered by the zone rulesets."
  type        = list(string)
  default     = []
}
