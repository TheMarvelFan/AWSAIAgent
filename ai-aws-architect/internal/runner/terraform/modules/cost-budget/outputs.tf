output "budget_name" {
  value = aws_budgets_budget.this.name
}

output "monthly_limit_usd" {
  value = aws_budgets_budget.this.limit_amount
}
