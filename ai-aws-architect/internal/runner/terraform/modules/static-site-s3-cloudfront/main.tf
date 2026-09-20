locals {
  # The aiarch- prefix is fixed here rather than passed in, because the IAM
  # policy grants this role access to arn:aws:s3:::aiarch-* and nothing else.
  # Enforcing the prefix in the module means no parameter value can produce a
  # bucket name outside what the role is permitted to touch - the naming
  # convention and the permission boundary are the same fact.
  bucket_name = "aiarch-${var.bucket_name_prefix}-${var.name_suffix}"
  origin_id   = "s3-${local.bucket_name}"

  # AWS managed cache policy "Managed-CachingOptimized". Hardcoded rather than
  # looked up through a data source: resolving it by name needs
  # cloudfront:ListCachePolicies, which the provisioning role does not have and
  # should not need. The id is a documented, stable AWS constant.
  cache_policy_caching_optimized = "658327ea-f89d-4fab-a63d-7e88639e58f6"
}

resource "aws_s3_bucket" "this" {
  bucket = local.bucket_name

  # Auto-teardown must actually succeed. Without this, destroying a bucket
  # holding the site's files fails, the deployment lands in destroy_failed, and
  # the resource keeps billing.
  force_destroy = true
}

# The bucket is private. CloudFront reaches it through Origin Access Control,
# not by the bucket being public - so there is no combination of inputs that
# exposes the origin directly, and the only route to the content is through the
# distribution.
resource "aws_s3_bucket_public_access_block" "this" {
  bucket = aws_s3_bucket.this.id

  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

resource "aws_s3_bucket_server_side_encryption_configuration" "this" {
  bucket = aws_s3_bucket.this.id

  rule {
    apply_server_side_encryption_by_default {
      sse_algorithm = "AES256"
    }
  }
}

# A placeholder page, so the distribution URL shows something recognisable the
# moment it is live. Same reasoning as the serverless module's placeholder
# handler: the catalog describes infrastructure, not content, and there is
# nowhere for a user's real site to come from yet. This is the obvious seam for
# a future upload step.
resource "aws_s3_object" "index" {
  bucket       = aws_s3_bucket.this.id
  key          = "index.html"
  content_type = "text/html; charset=utf-8"

  content = <<-HTML
    <!doctype html>
    <html lang="en">
      <head>
        <meta charset="utf-8" />
        <meta name="viewport" content="width=device-width, initial-scale=1" />
        <title>Provisioned by AI AWS Architect</title>
      </head>
      <body>
        <main>
          <h1>This site is live.</h1>
          <p>
            Provisioned by AI AWS Architect into a private S3 bucket, served
            through CloudFront over HTTPS.
          </p>
          <p>Bucket: <code>${local.bucket_name}</code></p>
          <p>Placeholder content. Replace with your own files.</p>
        </main>
      </body>
    </html>
  HTML

  # Without this the placeholder is cached at the edge for a day, and replacing
  # it would appear to do nothing.
  cache_control = "no-cache"
}

resource "aws_cloudfront_origin_access_control" "this" {
  name                              = local.bucket_name
  description                       = "Access control for ${local.bucket_name}"
  origin_access_control_origin_type = "s3"
  signing_behavior                  = "always"
  signing_protocol                  = "sigv4"
}

resource "aws_cloudfront_distribution" "this" {
  enabled             = true
  default_root_object = "index.html"
  price_class         = var.price_class
  comment             = local.bucket_name

  # Deliberately left at the default (true). It costs 10-15 minutes on apply,
  # but the deployment state machine treats `applied` as meaning the resources
  # exist and work. Returning early would make `applied` mean "created, but not
  # yet serving", which is a lie in the direction that matters. DEPLOY_MAX_RUN_TIME
  # defaults to 20m, which covers it.
  wait_for_deployment = true

  origin {
    # bucket_regional_domain_name, not bucket_domain_name: the regional form
    # avoids a redirect on first request for buckets outside us-east-1.
    domain_name              = aws_s3_bucket.this.bucket_regional_domain_name
    origin_id                = local.origin_id
    origin_access_control_id = aws_cloudfront_origin_access_control.this.id
  }

  default_cache_behavior {
    target_origin_id = local.origin_id

    # A static site is read-only. Allowing anything beyond GET and HEAD would
    # let a request method reach the origin that the origin cannot serve.
    allowed_methods = ["GET", "HEAD"]
    cached_methods  = ["GET", "HEAD"]

    viewer_protocol_policy = "redirect-to-https"
    compress               = true
    cache_policy_id        = local.cache_policy_caching_optimized
  }

  # See the spa_fallback variable for why 403 is here as well as 404: a private
  # S3 origin answers AccessDenied for a missing key, so CloudFront never sees
  # a 404 from it.
  dynamic "custom_error_response" {
    for_each = var.spa_fallback ? [403, 404] : []

    content {
      error_code            = custom_error_response.value
      response_code         = 200
      response_page_path    = "/index.html"
      error_caching_min_ttl = 10
    }
  }

  restrictions {
    geo_restriction {
      restriction_type = "none"
    }
  }

  viewer_certificate {
    # The distribution's own *.cloudfront.net certificate. A custom domain
    # would need an ACM certificate in us-east-1 and a DNS record, neither of
    # which is in the vetted catalog.
    cloudfront_default_certificate = true
  }
}

# Grants exactly this distribution read access to exactly this bucket.
#
# Written after the distribution because it needs the distribution ARN in the
# condition. Without that condition the policy would let ANY CloudFront
# distribution in ANY account read the bucket, which is the classic
# misconfiguration this pattern exists to avoid.
resource "aws_s3_bucket_policy" "allow_cloudfront" {
  bucket = aws_s3_bucket.this.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Sid       = "AllowCloudFrontServicePrincipalReadOnly"
      Effect    = "Allow"
      Principal = { Service = "cloudfront.amazonaws.com" }
      Action    = "s3:GetObject"
      Resource  = "${aws_s3_bucket.this.arn}/*"
      Condition = {
        StringEquals = {
          "AWS:SourceArn" = aws_cloudfront_distribution.this.arn
        }
      }
    }]
  })

  # The public access block must exist first, or PutBucketPolicy can race with
  # it and be rejected as a public policy.
  depends_on = [aws_s3_bucket_public_access_block.this]
}
