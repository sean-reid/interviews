terraform {
  required_version = ">= 1.5"
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.0"
    }
  }
}

provider "aws" {
  region = var.region
}

variable "region" {
  type        = string
  description = "AWS region for the evidence bucket"
}

variable "bucket" {
  type        = string
  description = "Name for the evidence bucket"
}

variable "noncurrent_version_days" {
  type        = number
  default     = 30
  description = "How long a superseded object version is kept before expiry"
}

resource "aws_s3_bucket" "evidence" {
  bucket = var.bucket
}

resource "aws_s3_bucket_versioning" "evidence" {
  bucket = aws_s3_bucket.evidence.id
  versioning_configuration {
    status = "Enabled"
  }
}

# Versioning with no expiry only grows. The provisioning log re-uploads every
# 30 seconds during boot and the evidence bundle every 2 minutes for the whole
# interview, so a session leaves dozens of superseded versions, and terraform
# state in this bucket holds the session tokens in plaintext. Keeping them
# forever is a decision about credentials as much as about bytes.
resource "aws_s3_bucket_lifecycle_configuration" "evidence" {
  bucket = aws_s3_bucket.evidence.id

  rule {
    id     = "expire-superseded-versions"
    status = "Enabled"

    filter {}

    noncurrent_version_expiration {
      noncurrent_days = var.noncurrent_version_days
    }

    # A failed evidence sync leaves parts that bill and that no listing shows.
    abort_incomplete_multipart_upload {
      days_after_initiation = 7
    }
  }

  # Versioning has to exist before a rule can talk about noncurrent versions.
  depends_on = [aws_s3_bucket_versioning.evidence]
}

resource "aws_s3_bucket_public_access_block" "evidence" {
  bucket                  = aws_s3_bucket.evidence.id
  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

output "evidence_bucket" {
  value       = aws_s3_bucket.evidence.id
  description = "Bucket every interview host syncs evidence into"
}
