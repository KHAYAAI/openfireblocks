locals {
  public_subnet_cidrs  = [cidrsubnet(var.vpc_cidr, 2, 0), cidrsubnet(var.vpc_cidr, 2, 1)]
  private_subnet_cidrs = [cidrsubnet(var.vpc_cidr, 2, 2), cidrsubnet(var.vpc_cidr, 2, 3)]
}

# VPC
resource "aws_vpc" "main" {
  cidr_block = var.vpc_cidr

  enable_dns_hostnames = true
  enable_dns_support   = true

  tags = merge(
    var.tags,
    {
      Name = "openfireblocks-${var.region}-vpc"
    }
  )
}

# Internet Gateway
resource "aws_internet_gateway" "main" {
  vpc_id = aws_vpc.main.id

  tags = merge(
    var.tags,
    {
      Name = "openfireblocks-${var.region}-igw"
    }
  )
}

# Public Subnets
resource "aws_subnet" "public" {
  count             = length(local.public_subnet_cidrs)
  vpc_id            = aws_vpc.main.id
  cidr_block        = local.public_subnet_cidrs[count.index]
  availability_zone = var.availability_zones[count.index % length(var.availability_zones)]

  map_public_ip_on_launch = true

  tags = merge(
    var.tags,
    {
      Name = "openfireblocks-${var.region}-public-subnet-${count.index + 1}"
      Type = "Public"
    }
  )
}

# Private Subnets
resource "aws_subnet" "private" {
  count             = length(local.private_subnet_cidrs)
  vpc_id            = aws_vpc.main.id
  cidr_block        = local.private_subnet_cidrs[count.index]
  availability_zone = var.availability_zones[count.index % length(var.availability_zones)]

  tags = merge(
    var.tags,
    {
      Name = "openfireblocks-${var.region}-private-subnet-${count.index + 1}"
      Type = "Private"
    }
  )
}

# Elastic IPs for NAT Gateways
resource "aws_eip" "nat" {
  count  = var.enable_nat_gateway ? length(local.public_subnet_cidrs) : 0
  domain = "vpc"

  tags = merge(
    var.tags,
    {
      Name = "openfireblocks-${var.region}-nat-eip-${count.index + 1}"
    }
  )

  depends_on = [aws_internet_gateway.main]
}

# NAT Gateways
resource "aws_nat_gateway" "main" {
  count         = var.enable_nat_gateway ? length(local.public_subnet_cidrs) : 0
  allocation_id = aws_eip.nat[count.index].id
  subnet_id     = aws_subnet.public[count.index].id

  tags = merge(
    var.tags,
    {
      Name = "openfireblocks-${var.region}-nat-${count.index + 1}"
    }
  )

  depends_on = [aws_internet_gateway.main]
}

# Route Table for Public Subnets
resource "aws_route_table" "public" {
  vpc_id = aws_vpc.main.id

  route {
    cidr_block      = "0.0.0.0/0"
    gateway_id      = aws_internet_gateway.main.id
  }

  tags = merge(
    var.tags,
    {
      Name = "openfireblocks-${var.region}-public-rt"
    }
  )
}

# Route Table Associations for Public Subnets
resource "aws_route_table_association" "public" {
  count          = length(aws_subnet.public)
  subnet_id      = aws_subnet.public[count.index].id
  route_table_id = aws_route_table.public.id
}

# Route Tables for Private Subnets (one per NAT Gateway for HA)
resource "aws_route_table" "private" {
  count  = length(local.private_subnet_cidrs)
  vpc_id = aws_vpc.main.id

  route {
    cidr_block     = "0.0.0.0/0"
    nat_gateway_id = var.enable_nat_gateway ? aws_nat_gateway.main[count.index % length(aws_nat_gateway.main)].id : null
  }

  tags = merge(
    var.tags,
    {
      Name = "openfireblocks-${var.region}-private-rt-${count.index + 1}"
    }
  )
}

# Route Table Associations for Private Subnets
resource "aws_route_table_association" "private" {
  count          = length(aws_subnet.private)
  subnet_id      = aws_subnet.private[count.index].id
  route_table_id = aws_route_table.private[count.index].id
}

# VPN Gateway (optional)
resource "aws_vpn_gateway" "main" {
  count  = var.enable_vpn_gateway ? 1 : 0
  vpc_id = aws_vpc.main.id

  tags = merge(
    var.tags,
    {
      Name = "openfireblocks-${var.region}-vpn-gw"
    }
  )
}

# VPC Flow Logs
resource "aws_flow_log" "main" {
  count                   = var.enable_flow_logs ? 1 : 0
  iam_role_arn           = aws_iam_role.flow_logs[0].arn
  log_destination        = aws_cloudwatch_log_group.flow_logs[0].arn
  traffic_type           = "ALL"
  vpc_id                 = aws_vpc.main.id
  log_destination_type   = "cloud-watch-logs"

  tags = merge(
    var.tags,
    {
      Name = "openfireblocks-${var.region}-flow-logs"
    }
  )
}

resource "aws_cloudwatch_log_group" "flow_logs" {
  count             = var.enable_flow_logs ? 1 : 0
  name              = "/aws/vpc/openfireblocks-${var.region}-flow-logs"
  retention_in_days = 7

  tags = merge(
    var.tags,
    {
      Name = "openfireblocks-${var.region}-flow-logs"
    }
  )
}

resource "aws_iam_role" "flow_logs" {
  count = var.enable_flow_logs ? 1 : 0
  name  = "openfireblocks-${var.region}-flow-logs-role"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect = "Allow"
        Principal = {
          Service = "vpc-flow-logs.amazonaws.com"
        }
        Action = "sts:AssumeRole"
      }
    ]
  })
}

resource "aws_iam_role_policy" "flow_logs" {
  count = var.enable_flow_logs ? 1 : 0
  name  = "openfireblocks-${var.region}-flow-logs-policy"
  role  = aws_iam_role.flow_logs[0].id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect = "Allow"
        Action = [
          "logs:CreateLogGroup",
          "logs:CreateLogStream",
          "logs:PutLogEvents",
          "logs:DescribeLogGroups",
          "logs:DescribeLogStreams"
        ]
        Resource = "${aws_cloudwatch_log_group.flow_logs[0].arn}:*"
      }
    ]
  })
}

# DB Subnet Group
resource "aws_db_subnet_group" "main" {
  name       = "openfireblocks-${var.region}-db-subnet-group"
  subnet_ids = aws_subnet.private[*].id

  tags = merge(
    var.tags,
    {
      Name = "openfireblocks-${var.region}-db-subnet-group"
    }
  )
}

# ElastiCache Subnet Group
resource "aws_elasticache_subnet_group" "main" {
  name       = "openfireblocks-${var.region}-cache-subnet-group"
  subnet_ids = aws_subnet.private[*].id

  tags = merge(
    var.tags,
    {
      Name = "openfireblocks-${var.region}-cache-subnet-group"
    }
  )
}
