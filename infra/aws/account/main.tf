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

resource "aws_s3_bucket" "evidence" {
  bucket = var.bucket
}

resource "aws_s3_bucket_versioning" "evidence" {
  bucket = aws_s3_bucket.evidence.id
  versioning_configuration {
    status = "Enabled"
  }
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
