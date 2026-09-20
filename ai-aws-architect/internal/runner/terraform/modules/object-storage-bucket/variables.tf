variable "bucket_name_prefix" {
  type        = string
  description = "Lowercase prefix. A per-chat suffix is appended for global uniqueness."
}

variable "name_suffix" {
  type        = string
  description = <<-EOT
    Stable per-chat suffix. Supplied by the generator rather than randomised in
    Terraform: a random_id would be stored in state and survive, but a fresh
    workspace (or lost state) would produce a different name and orphan the old
    bucket. Deriving it from the chat id makes the name reproducible from
    nothing but the deployment record.
  EOT
}

variable "versioning" {
  type    = bool
  default = true
}

variable "expire_after_days" {
  type    = number
  default = 30

  validation {
    condition     = var.expire_after_days >= 1 && var.expire_after_days <= 365
    error_message = "expire_after_days must be between 1 and 365."
  }
}
