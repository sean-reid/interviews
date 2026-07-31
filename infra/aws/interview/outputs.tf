output "candidate_url" {
  value       = "https://${aws_eip.this.public_ip}.sslip.io/c/${random_password.candidate_token.result}"
  sensitive   = true
  description = "Writable session URL; send at start time, not before"
}

output "observer_url" {
  value       = "https://${aws_eip.this.public_ip}.sslip.io/o/${random_password.observer_token.result}"
  sensitive   = true
  description = "Read-only session URL for the interviewer"
}

output "evidence_path" {
  value       = "s3://${var.evidence_bucket}/${var.seed}/"
  description = "Where the host syncs evidence.tar.gz"
}

output "public_ip" {
  value       = aws_eip.this.public_ip
  description = "The host's elastic IP"
}
