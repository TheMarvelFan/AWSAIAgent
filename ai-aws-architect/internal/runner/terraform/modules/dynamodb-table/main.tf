locals {
  table_name = "aiarch-${var.table_name}-${var.name_suffix}"
}

resource "aws_dynamodb_table" "this" {
  name = local.table_name

  # PAY_PER_REQUEST rather than provisioned capacity, and not a parameter.
  # Provisioned capacity bills whether or not anything reads the table, which
  # is exactly the kind of silent cost this project exists to avoid. On-demand
  # costs nothing at rest and is covered by the free tier at demo volumes.
  billing_mode = "PAY_PER_REQUEST"

  hash_key  = var.partition_key
  range_key = var.sort_key != "" ? var.sort_key : null

  attribute {
    name = var.partition_key
    type = "S"
  }

  # DynamoDB requires an attribute definition for every key, and ONLY for keys.
  # Declaring an unused attribute is an error, hence the dynamic block.
  dynamic "attribute" {
    for_each = var.sort_key != "" ? [var.sort_key] : []

    content {
      name = attribute.value
      type = "S"
    }
  }

  dynamic "ttl" {
    for_each = var.ttl_attribute != "" ? [var.ttl_attribute] : []

    content {
      attribute_name = ttl.value
      enabled        = true
    }
  }

  # Point-in-time recovery is deliberately off: it roughly doubles storage cost
  # and this catalog block is not for data worth recovering. Revisit if the
  # catalog ever grows a "production database" block.
  point_in_time_recovery {
    enabled = false
  }

  server_side_encryption {
    enabled = true
  }
}
