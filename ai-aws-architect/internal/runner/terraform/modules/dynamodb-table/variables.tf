variable "table_name" {
  type = string
}

variable "name_suffix" {
  type = string
}

variable "partition_key" {
  type        = string
  default     = "pk"
  description = "Attribute name for the partition key."
}

variable "sort_key" {
  type        = string
  default     = ""
  description = "Optional sort key. Empty means a partition-key-only table."
}

variable "ttl_attribute" {
  type        = string
  default     = ""
  description = <<-EOT
    Optional attribute holding a Unix timestamp after which DynamoDB deletes the
    item. Empty disables TTL. Useful for demo data that should clear itself.
  EOT
}
