variable "function_name" {
  type = string
}

variable "name_suffix" {
  type        = string
  description = "Stable per-chat suffix supplied by the generator."
}

variable "memory_mb" {
  type    = number
  default = 512

  validation {
    condition     = var.memory_mb >= 128 && var.memory_mb <= 2048
    error_message = "memory_mb must be between 128 and 2048."
  }
}

variable "timeout_seconds" {
  type    = number
  default = 15

  validation {
    condition     = var.timeout_seconds >= 1 && var.timeout_seconds <= 60
    error_message = "timeout_seconds must be between 1 and 60."
  }
}

variable "log_retention_days" {
  type        = number
  default     = 7
  description = "CloudWatch Logs never expire by default, which bills forever."
}
