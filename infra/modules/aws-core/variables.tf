variable "env" {
  type = string
  validation {
    condition     = contains(["dev", "prod"], var.env)
    error_message = "env must be dev or prod."
  }
}

variable "bitgo_base_url" {
  type    = string
  default = "https://app.bitgo-test.com"
}

variable "lambda_memory_mb" {
  type    = number
  default = 128
  validation {
    condition     = var.lambda_memory_mb >= 128 && var.lambda_memory_mb <= 256
    error_message = "Keep Lambda memory between 128 and 256 MB to stay inside the free GB-seconds."
  }
}

variable "log_retention_days" {
  type    = number
  default = 7
  validation {
    condition     = contains([1, 3, 5, 7], var.log_retention_days)
    error_message = "Log retention must be 7 days or less."
  }
}

variable "recon_interval_minutes" {
  type    = number
  default = 15
}

variable "alert_email" {
  type    = string
  default = ""
}

variable "create_budgets" {
  description = "Budgets are account-wide; create them from one environment only."
  type        = bool
  default     = false
}

variable "monthly_budget_usd" {
  type    = number
  default = 5
}
