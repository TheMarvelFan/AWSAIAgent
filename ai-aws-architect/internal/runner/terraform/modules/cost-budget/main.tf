# A monthly cost budget in the CUSTOMER's account.
#
# This exists because of a gap the cross-account design opened: the project's
# original budget guardrail assumed resources lived in our account, where our
# own AWS Budget could see them. Once provisioning moved to the customer's
# account, our budget became blind to their spend. A budget created here is the
# guardrail travelling with the deployment rather than being left behind.
#
# Note what it is and is not. It ALERTS; it does not block. AWS budget actions
# can attach IAM restrictions on breach, which would need broader IAM
# permissions than the provisioning role has and is a deliberate omission.
locals {
  budget_name = "aiarch-${var.budget_name}-${var.name_suffix}"
}

resource "aws_budgets_budget" "this" {
  name         = local.budget_name
  budget_type  = "COST"
  limit_amount = tostring(var.monthly_limit_usd)
  limit_unit   = "USD"
  time_unit    = "MONTHLY"

  # Account-wide, not filtered to this system's tags. Filtering by tag requires
  # the tag to be activated as a cost allocation tag in the billing console and
  # then takes up to 24 hours to take effect - too slow and too manual to rely
  # on. An account-wide budget is blunter but works immediately.

  # 80% of ACTUAL spend: early enough to react.
  notification {
    comparison_operator        = "GREATER_THAN"
    threshold                  = 80
    threshold_type             = "PERCENTAGE"
    notification_type          = "ACTUAL"
    subscriber_email_addresses = [var.notify_email]
  }

  # 100% of FORECASTED spend: AWS projects month-end from current run rate, so
  # this fires while there is still time to do something about it.
  notification {
    comparison_operator        = "GREATER_THAN"
    threshold                  = 100
    threshold_type             = "PERCENTAGE"
    notification_type          = "FORECASTED"
    subscriber_email_addresses = [var.notify_email]
  }

  # Actually over.
  notification {
    comparison_operator        = "GREATER_THAN"
    threshold                  = 100
    threshold_type             = "PERCENTAGE"
    notification_type          = "ACTUAL"
    subscriber_email_addresses = [var.notify_email]
  }
}
