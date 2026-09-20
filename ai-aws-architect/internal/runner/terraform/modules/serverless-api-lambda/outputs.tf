output "api_url" {
  description = "Public HTTPS endpoint. Open it to confirm the deployment works."
  value       = aws_apigatewayv2_stage.this.invoke_url
}

output "function_name" {
  value = aws_lambda_function.this.function_name
}

output "log_group" {
  value = aws_cloudwatch_log_group.this.name
}
