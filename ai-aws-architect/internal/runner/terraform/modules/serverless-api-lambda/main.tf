locals {
  # aiarch- prefix fixed in the module, not passed in: the IAM policy grants
  # this role access to aiarch-* names and nothing else, so the naming
  # convention and the permission boundary are the same fact.
  fn_name = "aiarch-${var.function_name}-${var.name_suffix}"
}

# A placeholder handler, generated at plan time.
#
# The catalog describes infrastructure, not application code - there is nowhere
# for a user's real handler to come from yet. This returns a recognisable
# response so the deployed URL visibly works, and is the obvious seam for a
# future "upload your code" step.
data "archive_file" "handler" {
  type        = "zip"
  output_path = "${path.module}/.build/${local.fn_name}.zip"

  source {
    filename = "index.py"
    content  = <<-PY
      import json

      def handler(event, context):
          return {
              "statusCode": 200,
              "headers": {"content-type": "application/json"},
              "body": json.dumps({
                  "message": "Provisioned by AI AWS Architect.",
                  "function": "${local.fn_name}",
                  "note": "Placeholder handler. Replace with your own code.",
              }),
          }
    PY
  }
}

# Execution role. Named with the aiarch- prefix because the provisioning role's
# IAM permissions are confined to role/aiarch-*, so anything outside that
# prefix simply cannot be created.
resource "aws_iam_role" "exec" {
  name = "${local.fn_name}-exec"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = { Service = "lambda.amazonaws.com" }
      Action    = "sts:AssumeRole"
    }]
  })
}

# Inline rather than the AWS-managed AWSLambdaBasicExecutionRole: the
# provisioning role can attach policies only within its own prefix, and an
# inline policy is deleted with the role, so teardown leaves nothing behind.
resource "aws_iam_role_policy" "logs" {
  name = "logs"
  role = aws_iam_role.exec.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect   = "Allow"
      Action   = ["logs:CreateLogStream", "logs:PutLogEvents"]
      Resource = "${aws_cloudwatch_log_group.this.arn}:*"
    }]
  })
}

# Created explicitly so retention is bounded. Lambda would create this group
# implicitly with no expiry, and logs bill indefinitely.
resource "aws_cloudwatch_log_group" "this" {
  name              = "/aws/lambda/${local.fn_name}"
  retention_in_days = var.log_retention_days
}

resource "aws_lambda_function" "this" {
  function_name = local.fn_name
  role          = aws_iam_role.exec.arn
  handler       = "index.handler"
  runtime       = "python3.13"

  filename         = data.archive_file.handler.output_path
  source_code_hash = data.archive_file.handler.output_base64sha256

  memory_size = var.memory_mb
  timeout     = var.timeout_seconds

  depends_on = [aws_cloudwatch_log_group.this]
}

resource "aws_apigatewayv2_api" "this" {
  name          = local.fn_name
  protocol_type = "HTTP"
}

resource "aws_apigatewayv2_integration" "this" {
  api_id                 = aws_apigatewayv2_api.this.id
  integration_type       = "AWS_PROXY"
  integration_uri        = aws_lambda_function.this.invoke_arn
  payload_format_version = "2.0"
}

resource "aws_apigatewayv2_route" "default" {
  api_id    = aws_apigatewayv2_api.this.id
  route_key = "$default"
  target    = "integrations/${aws_apigatewayv2_integration.this.id}"
}

resource "aws_apigatewayv2_stage" "this" {
  api_id      = aws_apigatewayv2_api.this.id
  name        = "$default"
  auto_deploy = true
}

# Scoped to this specific API. Without source_arn any API Gateway in any
# account could invoke the function.
resource "aws_lambda_permission" "apigw" {
  statement_id  = "AllowAPIGatewayInvoke"
  action        = "lambda:InvokeFunction"
  function_name = aws_lambda_function.this.function_name
  principal     = "apigateway.amazonaws.com"
  source_arn    = "${aws_apigatewayv2_api.this.execution_arn}/*/*"
}
