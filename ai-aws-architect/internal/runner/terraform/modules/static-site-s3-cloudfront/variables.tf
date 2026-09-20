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

variable "price_class" {
  type        = string
  default     = "PriceClass_100"
  description = "Edge footprint. PriceClass_100 is the cheapest and covers North America and Europe."

  validation {
    condition     = contains(["PriceClass_100", "PriceClass_200", "PriceClass_All"], var.price_class)
    error_message = "price_class must be PriceClass_100, PriceClass_200 or PriceClass_All."
  }
}

variable "spa_fallback" {
  type        = bool
  default     = true
  description = <<-EOT
    Serve /index.html with a 200 for 403 and 404, so a client-side router owns
    unknown paths. Harmless for a plain multi-page site; required for a SPA,
    where a deep link would otherwise 404 on refresh.

    403 is included because a private S3 origin returns AccessDenied rather
    than NotFound for a missing key - CloudFront never sees a 404 from it.
  EOT
}
