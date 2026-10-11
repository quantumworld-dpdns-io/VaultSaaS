variable "cloudflare_account_id" { type = string }
variable "cloudflare_zone_id" { type = string }
variable "domain" { type = string }

variable "alert_email" {
  type    = string
  default = ""
}

variable "create_budgets" {
  description = "Budgets are account-wide; only one environment creates them."
  type        = bool
  default     = false
}

variable "attach_worker_domain" {
  type    = bool
  default = true
}

variable "bitgo_source_cidrs" {
  type    = list(string)
  default = []
}

variable "admin_emails" {
  type    = list(string)
  default = []
}
