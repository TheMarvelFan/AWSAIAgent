locals {
  # The aiarch- prefix is fixed here rather than passed in, because the IAM
  # policy grants this role access to arn:aws:s3:::aiarch-* and nothing else.
  # Enforcing the prefix in the module means no parameter value can produce a
  # bucket name outside what the role is permitted to touch - the naming
  # convention and the permission boundary are the same fact.
  #
  # 7 + up to 40 + 1 + 8 = 56, inside S3's 63-character limit.
  bucket_name = "aiarch-${var.bucket_name_prefix}-${var.name_suffix}"
}

resource "aws_s3_bucket" "this" {
  bucket = local.bucket_name

  # Auto-teardown must actually succeed. Without this, destroying a bucket that
  # has any object in it fails, the deployment lands in destroy_failed, and the
  # resource keeps billing. Correct for a demo; revisit before anything holds
  # data worth keeping.
  force_destroy = true
}

# Public access is blocked unconditionally and is not a parameter. Serving files
# publicly is what the static-site block is for; this one is private storage and
# there is no combination of inputs that makes it otherwise.
resource "aws_s3_bucket_public_access_block" "this" {
  bucket = aws_s3_bucket.this.id

  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

resource "aws_s3_bucket_versioning" "this" {
  bucket = aws_s3_bucket.this.id

  versioning_configuration {
    status = var.versioning ? "Enabled" : "Suspended"
  }
}

resource "aws_s3_bucket_server_side_encryption_configuration" "this" {
  bucket = aws_s3_bucket.this.id

  rule {
    apply_server_side_encryption_by_default {
      sse_algorithm = "AES256"
    }
  }
}

resource "aws_s3_bucket_lifecycle_configuration" "this" {
  bucket = aws_s3_bucket.this.id

  # Versioning keeps deleted objects as noncurrent versions, which bill. Both
  # rules are needed or a versioned bucket never actually shrinks.
  rule {
    id     = "expire-current"
    status = "Enabled"

    filter {}

    expiration {
      days = var.expire_after_days
    }
  }

  rule {
    id     = "expire-noncurrent"
    status = "Enabled"

    filter {}

    noncurrent_version_expiration {
      noncurrent_days = var.expire_after_days
    }
  }

  depends_on = [aws_s3_bucket_versioning.this]
}
