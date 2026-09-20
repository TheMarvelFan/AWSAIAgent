variable "budget_name" {
  type = string
}

variable "name_suffix" {
  type = string
}

variable "monthly_limit_usd" {
  type = number

  validation {
    condition     = var.monthly_limit_usd > 0 && var.monthly_limit_usd <= 100000
    error_message = "monthly_limit_usd must be between 1 and 100000."
  }
}

variable "notify_email" {
  type        = string
  description = <<-EOT
    Where threshold alerts go. Required: a budget nobody is told about is a
    number in a console, not a guardrail.
  EOT

  validation {
    condition     = can(regex("^[^@\\s]+@[^@\\s]+\\.[^@\\s]+$", var.notify_email))
    error_message = "notify_email must be a valid email address."
  }
}
