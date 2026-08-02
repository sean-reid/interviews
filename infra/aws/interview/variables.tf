variable "problem" {
  type        = string
  description = "Problem id to run"
}

variable "seed" {
  type        = string
  description = "Interview id; selects the variant and the evidence prefix"
}

variable "ttl_minutes" {
  type        = number
  default     = 120
  description = "Minutes until the host powers off and terminates"
}

variable "allow_cidrs" {
  type        = list(string)
  default     = ["0.0.0.0/0"]
  description = "CIDRs allowed to reach the session over HTTPS"
}

variable "evidence_bucket" {
  type        = string
  description = "Evidence bucket from the account module"
}

variable "repo_tarball_s3_uri" {
  type        = string
  description = "s3:// URI of the platform tarball (binary, content, host assets)"

  validation {
    condition     = startswith(var.repo_tarball_s3_uri, "s3://")
    error_message = "repo_tarball_s3_uri must be an s3:// URI."
  }
}

variable "content_version" {
  type        = string
  default     = ""
  description = "Version id of the content tarball the host will unpack; recorded in the evidence"
}

variable "instance_type" {
  type        = string
  default     = "t3.large"
  description = "EC2 instance type; kind wants at least 2 vCPU and 8 GiB"
}

variable "region" {
  type        = string
  description = "AWS region to run the host in"
}
