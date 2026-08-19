# AWS WAF (Web Application Firewall) Configuration
# Protects API Gateway and Load Balancers from common attacks

resource "aws_wafv2_ip_set" "blocked_ips" {
  name               = "openfireblocks-blocked-ips-${var.environment}"
  description        = "IP set for blocked addresses"
  scope              = "REGIONAL"
  ip_address_version = "IPV4"
  addresses          = []

  tags = merge(
    {
      Environment = var.environment
      Project     = "OpenFireblocks"
      ManagedBy   = "Terraform"
    },
    {
      Name = "openfireblocks-blocked-ips"
    }
  )
}

resource "aws_wafv2_web_acl" "primary" {
  name        = "openfireblocks-primary-${var.environment}"
  description = "WAF rules for OpenFireblocks primary region"
  scope       = "REGIONAL"

  default_action {
    allow {}
  }

  # Rate limiting rule - prevent DDoS attacks
  rule {
    name     = "RateLimitRule"
    priority = 0

    action {
      block {}
    }

    statement {
      rate_based_statement {
        limit              = 2000
        aggregate_key_type = "IP"
      }
    }

    visibility_config {
      cloudwatch_metrics_enabled = true
      metric_name                = "RateLimitRule"
      sampled_requests_enabled   = true
    }
  }

  # AWS Managed Rule - Common Rule Set
  rule {
    name     = "AWSManagedRulesCommonRuleSet"
    priority = 1

    override_action {
      none {}
    }

    statement {
      managed_rule_group_statement {
        name        = "AWSManagedRulesCommonRuleSet"
        vendor_name = "AWS"

        # Exclude certain rules if needed
        rule_action_override {
          name = "SizeRestrictions_BODY"
          action_to_use {
            count {}
          }
        }
      }
    }

    visibility_config {
      cloudwatch_metrics_enabled = true
      metric_name                = "AWSManagedRulesCommonRuleSetMetrics"
      sampled_requests_enabled   = true
    }
  }

  # AWS Managed Rule - Known Bad Inputs
  rule {
    name     = "AWSManagedRulesKnownBadInputsRuleSet"
    priority = 2

    override_action {
      none {}
    }

    statement {
      managed_rule_group_statement {
        name        = "AWSManagedRulesKnownBadInputsRuleSet"
        vendor_name = "AWS"
      }
    }

    visibility_config {
      cloudwatch_metrics_enabled = true
      metric_name                = "AWSManagedRulesKnownBadInputsRuleSetMetrics"
      sampled_requests_enabled   = true
    }
  }

  # AWS Managed Rule - SQL Injection Protection
  rule {
    name     = "AWSManagedRulesSQLiRuleSet"
    priority = 3

    override_action {
      none {}
    }

    statement {
      managed_rule_group_statement {
        name        = "AWSManagedRulesSQLiRuleSet"
        vendor_name = "AWS"
      }
    }

    visibility_config {
      cloudwatch_metrics_enabled = true
      metric_name                = "AWSManagedRulesSQLiRuleSetMetrics"
      sampled_requests_enabled   = true
    }
  }

  # AWS Managed Rule - IP Reputation List
  rule {
    name     = "AWSManagedRulesAmazonIpReputationList"
    priority = 4

    override_action {
      none {}
    }

    statement {
      managed_rule_group_statement {
        name        = "AWSManagedRulesAmazonIpReputationList"
        vendor_name = "AWS"
      }
    }

    visibility_config {
      cloudwatch_metrics_enabled = true
      metric_name                = "AWSManagedRulesAmazonIpReputationListMetrics"
      sampled_requests_enabled   = true
    }
  }

  # Custom rule - Block access to sensitive paths
  rule {
    name     = "BlockSensitivePaths"
    priority = 5

    action {
      block {
        custom_response {
          response_code = 403
        }
      }
    }

    statement {
      byte_match_statement {
        field_to_match {
          uri_path {}
        }
        text_transformation {
          priority = 0
          type     = "LOWERCASE"
        }
        positional_constraint = "STARTS_WITH"
        search_string         = "/admin"
      }
    }

    visibility_config {
      cloudwatch_metrics_enabled = true
      metric_name                = "BlockSensitivePaths"
      sampled_requests_enabled   = true
    }
  }

  visibility_config {
    cloudwatch_metrics_enabled = true
    metric_name                = "openfireblocks-primary-waf"
    sampled_requests_enabled   = true
  }

  tags = merge(
    {
      Environment = var.environment
      Project     = "OpenFireblocks"
      ManagedBy   = "Terraform"
    },
    {
      Name = "openfireblocks-primary-waf"
    }
  )
}

resource "aws_wafv2_web_acl" "secondary" {
  provider    = aws.secondary
  name        = "openfireblocks-secondary-${var.environment}"
  description = "WAF rules for OpenFireblocks secondary region"
  scope       = "REGIONAL"

  default_action {
    allow {}
  }

  # Rate limiting rule
  rule {
    name     = "RateLimitRule"
    priority = 0

    action {
      block {}
    }

    statement {
      rate_based_statement {
        limit              = 2000
        aggregate_key_type = "IP"
      }
    }

    visibility_config {
      cloudwatch_metrics_enabled = true
      metric_name                = "RateLimitRule"
      sampled_requests_enabled   = true
    }
  }

  # AWS Managed Rule - Common Rule Set
  rule {
    name     = "AWSManagedRulesCommonRuleSet"
    priority = 1

    override_action {
      none {}
    }

    statement {
      managed_rule_group_statement {
        name        = "AWSManagedRulesCommonRuleSet"
        vendor_name = "AWS"

        rule_action_override {
          name = "SizeRestrictions_BODY"
          action_to_use {
            count {}
          }
        }
      }
    }

    visibility_config {
      cloudwatch_metrics_enabled = true
      metric_name                = "AWSManagedRulesCommonRuleSetMetrics"
      sampled_requests_enabled   = true
    }
  }

  # AWS Managed Rule - Known Bad Inputs
  rule {
    name     = "AWSManagedRulesKnownBadInputsRuleSet"
    priority = 2

    override_action {
      none {}
    }

    statement {
      managed_rule_group_statement {
        name        = "AWSManagedRulesKnownBadInputsRuleSet"
        vendor_name = "AWS"
      }
    }

    visibility_config {
      cloudwatch_metrics_enabled = true
      metric_name                = "AWSManagedRulesKnownBadInputsRuleSetMetrics"
      sampled_requests_enabled   = true
    }
  }

  visibility_config {
    cloudwatch_metrics_enabled = true
    metric_name                = "openfireblocks-secondary-waf"
    sampled_requests_enabled   = true
  }

  tags = merge(
    {
      Environment = var.environment
      Project     = "OpenFireblocks"
      ManagedBy   = "Terraform"
    },
    {
      Name = "openfireblocks-secondary-waf"
    }
  )
}

# CloudWatch Log Group for WAF
resource "aws_cloudwatch_log_group" "waf_primary" {
  name              = "/aws/waf/openfireblocks-primary"
  retention_in_days = 30
  kms_key_id        = aws_kms_key.primary.arn

  tags = merge(
    {
      Environment = var.environment
      Project     = "OpenFireblocks"
      ManagedBy   = "Terraform"
    },
    {
      Name = "openfireblocks-primary-waf-logs"
    }
  )
}

resource "aws_cloudwatch_log_group" "waf_secondary" {
  provider          = aws.secondary
  name              = "/aws/waf/openfireblocks-secondary"
  retention_in_days = 30
  kms_key_id        = aws_kms_key.secondary.arn

  tags = merge(
    {
      Environment = var.environment
      Project     = "OpenFireblocks"
      ManagedBy   = "Terraform"
    },
    {
      Name = "openfireblocks-secondary-waf-logs"
    }
  )
}

# Log Configuration for WAF
resource "aws_wafv2_web_acl_logging_configuration" "primary" {
  resource_arn            = aws_wafv2_web_acl.primary.arn
  log_destination_configs = [aws_cloudwatch_log_group.waf_primary.arn]

  logging_filter {
    default_behavior = "KEEP"

    filter {
      behavior = "KEEP"

      condition {
        action_condition {
          action = "BLOCK"
        }
      }

      requirement = "MEETS_ANY"
    }
  }
}

resource "aws_wafv2_web_acl_logging_configuration" "secondary" {
  provider                = aws.secondary
  resource_arn            = aws_wafv2_web_acl.secondary.arn
  log_destination_configs = [aws_cloudwatch_log_group.waf_secondary.arn]

  logging_filter {
    default_behavior = "KEEP"

    filter {
      behavior = "KEEP"

      condition {
        action_condition {
          action = "BLOCK"
        }
      }

      requirement = "MEETS_ANY"
    }
  }
}
