output "site_url" {
  description = "Public HTTPS URL. Open it to confirm the deployment works."
  value       = "https://${aws_cloudfront_distribution.this.domain_name}"
}

output "distribution_id" {
  value = aws_cloudfront_distribution.this.id
}

output "bucket_name" {
  description = "Upload your site's files here; CloudFront serves them."
  value       = aws_s3_bucket.this.id
}
