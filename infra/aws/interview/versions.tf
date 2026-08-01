terraform {
  required_version = ">= 1.5"

  # State lives in the evidence bucket, one key per interview, configured at
  # init time so nothing here names a bucket. Local state would mean only the
  # machine that provisioned a host could destroy it, and a lost checkout
  # would leave resources nobody can clean up. The account module keeps local
  # state because it is what creates this bucket.
  backend "s3" {}

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.0"
    }
    random = {
      source  = "hashicorp/random"
      version = "~> 3.6"
    }
  }
}

provider "aws" {
  region = var.region
}
