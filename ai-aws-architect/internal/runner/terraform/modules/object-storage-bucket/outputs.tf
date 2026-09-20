output "bucket_name" {
  value = aws_s3_bucket.this.id
}

output "bucket_arn" {
  value = aws_s3_bucket.this.arn
}

output "bucket_region" {
  # bucket_region, not region: in AWS provider v6 `region` became the
  # provider-level argument present on every resource, while the bucket's
  # actual location moved to bucket_region. They match in a single-region
  # setup, which is exactly why the wrong one is easy to ship.
  value = aws_s3_bucket.this.bucket_region
}
