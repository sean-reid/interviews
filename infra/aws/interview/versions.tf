terraform {
  required_version = ">= 1.5"

  # State lives in the evidence bucket, one key per interview, configured at
  # init time so nothing here names a bucket. Local state would mean only the
  # machine that provisioned a host could destroy it, and a lost checkout
  # would leave resources nobody can clean up. The account module keeps its
  # own state bucket, because it is what creates this one.
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

  # Every resource carries the interview it belongs to, because the session
  # registry is per machine: a host provisioned from one laptop is invisible
  # from another, and an apply that dies before writing the record leaves
  # something nobody can attribute. These tags are what makes the account
  # itself the index. They stay off the host, which disables metadata tags.
  default_tags {
    tags = {
      ManagedBy  = "interviews"
      Interview  = var.seed
      Problem    = var.problem
      TTLMinutes = tostring(var.ttl_minutes)
    }
  }
}
