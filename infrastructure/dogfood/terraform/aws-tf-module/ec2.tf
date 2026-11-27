resource "aws_instance" "automdmbot" {
  ami                    = "ami-0d1b5a8c13042c939" # us-east-2 - Ubuntu 24.04 LTS
  instance_type          = "t3.medium"
  subnet_id              = module.main.vpc.private_subnets[0] # take the first subnet in the list
  vpc_security_group_ids = [aws_security_group.automdmbot.id]

  root_block_device {
    volume_size = 50
  }

  key_name = "dogfood-automdmbot"

  tags = {
    Name = "${local.customer}-automdmbot"
  }

  volume_tags = merge({ Name = "${local.customer}-automdmbot" })
}

resource "aws_security_group" "automdmbot" {
  name        = "${local.customer}-automdmbot-sg"
  description = "Primary security group for ${local.customer}-automdmbot (SSH)"
  vpc_id      = module.main.vpc.vpc_id

  tags = {
    Name = "${local.customer}-automdmbot-sg"
  }
}

resource "aws_vpc_security_group_ingress_rule" "automdmbot_ssh" {
  security_group_id = aws_security_group.automdmbot.id
  cidr_ipv4         = "10.255.0.0/16"
  from_port         = 22
  to_port           = 22
  ip_protocol       = "tcp"
}

resource "aws_vpc_security_group_egress_rule" "automdmbot_any" {
  security_group_id = aws_security_group.automdmbot.id
  cidr_ipv4         = "0.0.0.0/0"
  ip_protocol       = "-1"
}
