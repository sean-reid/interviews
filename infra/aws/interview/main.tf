data "aws_ami" "ubuntu" {
  most_recent = true
  owners      = ["099720109477"] # Canonical

  filter {
    name   = "name"
    values = ["ubuntu/images/hvm-ssd-gp3/ubuntu-noble-24.04-amd64-server-*"]
  }

  filter {
    name   = "virtualization-type"
    values = ["hvm"]
  }
}

data "aws_vpc" "default" {
  default = true
}

data "aws_subnets" "default" {
  filter {
    name   = "vpc-id"
    values = [data.aws_vpc.default.id]
  }
}

resource "random_password" "candidate_token" {
  length  = 32
  special = false
}

resource "random_password" "observer_token" {
  length  = 32
  special = false
}

locals {
  # The EIP exists before the instance, so the sslip.io hostname is
  # known at user-data render time; the association happens after boot.
  hostname     = "${aws_eip.this.public_ip}.sslip.io"
  tarball_path = trimprefix(var.repo_tarball_s3_uri, "s3://")
}

resource "aws_security_group" "session" {
  name_prefix = "iv-${var.seed}-"
  description = "Interview session host"
  vpc_id      = data.aws_vpc.default.id

  ingress {
    description = "Session HTTPS"
    from_port   = 443
    to_port     = 443
    protocol    = "tcp"
    cidr_blocks = var.allow_cidrs
  }

  ingress {
    description = "ACME HTTP-01 and the HTTPS redirect"
    from_port   = 80
    to_port     = 80
    protocol    = "tcp"
    cidr_blocks = ["0.0.0.0/0"]
  }

  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }
}

data "aws_iam_policy_document" "assume" {
  statement {
    actions = ["sts:AssumeRole"]
    principals {
      type        = "Service"
      identifiers = ["ec2.amazonaws.com"]
    }
  }
}

data "aws_iam_policy_document" "session" {
  statement {
    actions   = ["s3:PutObject"]
    resources = ["arn:aws:s3:::${var.evidence_bucket}/${var.seed}/*"]
  }

  statement {
    actions   = ["s3:GetObject"]
    resources = ["arn:aws:s3:::${local.tarball_path}"]
  }
}

resource "aws_iam_role" "session" {
  name_prefix        = "iv-${var.seed}-"
  assume_role_policy = data.aws_iam_policy_document.assume.json
}

resource "aws_iam_role_policy" "session" {
  name   = "evidence-and-tarball"
  role   = aws_iam_role.session.id
  policy = data.aws_iam_policy_document.session.json
}

resource "aws_iam_instance_profile" "session" {
  name_prefix = "iv-${var.seed}-"
  role        = aws_iam_role.session.name
}

resource "aws_eip" "this" {
  domain = "vpc"
}

resource "aws_instance" "session" {
  ami                                  = data.aws_ami.ubuntu.id
  instance_type                        = var.instance_type
  subnet_id                            = data.aws_subnets.default.ids[0]
  vpc_security_group_ids               = [aws_security_group.session.id]
  iam_instance_profile                 = aws_iam_instance_profile.session.name
  instance_initiated_shutdown_behavior = "terminate"

  # IMDSv2 only, and a hop limit of 1 so a container cannot reach the
  # instance role. Without this any local uid can read user-data, which
  # carries the session tokens, and borrow the role to fetch the content
  # tarball from S3.
  metadata_options {
    http_endpoint               = "enabled"
    http_tokens                 = "required"
    http_put_response_hop_limit = 1
    instance_metadata_tags      = "disabled"
  }

  user_data = join("", [
    templatefile("${path.module}/user-data.sh.tpl", {
      problem             = var.problem
      seed                = var.seed
      candidate_token     = random_password.candidate_token.result
      observer_token      = random_password.observer_token.result
      ttl_minutes         = var.ttl_minutes
      hostname            = local.hostname
      evidence_bucket     = var.evidence_bucket
      repo_tarball_s3_uri = var.repo_tarball_s3_uri
    }),
    file("${path.module}/../../../session/host/provision.sh"),
  ])

  root_block_device {
    volume_size = 40
    volume_type = "gp3"
  }

  tags = {
    Name = "iv-${var.problem}-${var.seed}"
  }
}

resource "aws_eip_association" "this" {
  instance_id   = aws_instance.session.id
  allocation_id = aws_eip.this.id
}
