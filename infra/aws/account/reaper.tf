# A host stops billing when three things work: a systemd timer inside the
# guest, someone remembering interviews end, and someone running sessions
# --remote. The first two share a failure domain with the host and the third
# lives in a person's head, so none of them is a backstop. This is one, and
# it is the only thing here that runs when nobody is at a keyboard.
#
# It terminates on the tags the interview module writes, and only when all
# three are present. The IAM below can terminate nothing else: the condition
# on ManagedBy is what keeps a machine with delete permission from being a
# machine that can delete anything.

variable "reaper_grace_minutes" {
  type        = number
  default     = 15
  description = "Extra time past a host's TTL before the reaper acts, so the guest's own timer wins"
}

variable "reaper_interval_minutes" {
  type        = number
  default     = 15
  description = "How often the reaper looks"
}

variable "reaper_orphan_age_hours" {
  type        = number
  default     = 24
  description = "How old an iv- role or instance profile with no instance must be before the reaper deletes it"
}

data "aws_caller_identity" "current" {}

data "archive_file" "reaper" {
  type        = "zip"
  source_file = "${path.module}/reaper/handler.py"
  output_path = "${path.module}/.reaper.zip"
}

data "aws_iam_policy_document" "reaper_assume" {
  statement {
    actions = ["sts:AssumeRole"]
    principals {
      type        = "Service"
      identifiers = ["lambda.amazonaws.com"]
    }
  }
}

data "aws_iam_policy_document" "reaper" {
  # Describe cannot be resource-scoped by the API, so the read is account
  # wide. The write below is not.
  statement {
    sid       = "FindInterviewHosts"
    actions   = ["ec2:DescribeInstances"]
    resources = ["*"]
  }

  statement {
    sid       = "TerminateOnlyInterviewHosts"
    actions   = ["ec2:TerminateInstances"]
    resources = ["*"]
    condition {
      test     = "StringEquals"
      variable = "ec2:ResourceTag/ManagedBy"
      values   = ["interviews"]
    }
  }

  # The interview module makes a role and an instance profile per session,
  # and a destroy that dies partway strands both. They cost nothing, the
  # terraform identity is deliberately denied ListRoles, and instance
  # profiles cap at 1000 per account, so the reaper is the only thing that
  # can ever see or remove them. The list calls cannot be resource-scoped;
  # everything that inspects or deletes is.
  statement {
    sid       = "FindOrphanedSessionRolesAndProfiles"
    actions   = ["iam:ListRoles", "iam:ListInstanceProfiles"]
    resources = ["*"]
  }

  statement {
    sid = "InspectOnlyUnderTheIvPrefix"
    actions = [
      "iam:ListRoleTags",
      "iam:ListInstanceProfileTags",
      "iam:ListRolePolicies",
      "iam:ListInstanceProfilesForRole",
    ]
    resources = [
      "arn:aws:iam::${data.aws_caller_identity.current.account_id}:role/iv-*",
      "arn:aws:iam::${data.aws_caller_identity.current.account_id}:instance-profile/iv-*",
    ]
  }

  # The tag condition is what keeps the reaper's own iv-reaper role, which
  # this module creates untagged, out of its own reach.
  statement {
    sid = "DeleteOnlyTaggedInterviewLeftovers"
    actions = [
      "iam:RemoveRoleFromInstanceProfile",
      "iam:DeleteInstanceProfile",
      "iam:DeleteRolePolicy",
      "iam:DeleteRole",
    ]
    resources = [
      "arn:aws:iam::${data.aws_caller_identity.current.account_id}:role/iv-*",
      "arn:aws:iam::${data.aws_caller_identity.current.account_id}:instance-profile/iv-*",
    ]
    condition {
      test     = "StringEquals"
      variable = "aws:ResourceTag/ManagedBy"
      values   = ["interviews"]
    }
  }

  statement {
    sid       = "OwnLogs"
    actions   = ["logs:CreateLogStream", "logs:PutLogEvents"]
    resources = ["${aws_cloudwatch_log_group.reaper.arn}:*"]
  }
}

resource "aws_iam_role" "reaper" {
  name               = "iv-reaper"
  assume_role_policy = data.aws_iam_policy_document.reaper_assume.json
}

resource "aws_iam_role_policy" "reaper" {
  name   = "iv-reaper"
  role   = aws_iam_role.reaper.id
  policy = data.aws_iam_policy_document.reaper.json
}

# Created here rather than left to Lambda, which would make one with no
# expiry. Every run logs a line per instance it looked at.
resource "aws_cloudwatch_log_group" "reaper" {
  name              = "/aws/lambda/iv-reaper"
  retention_in_days = 30
}

resource "aws_lambda_function" "reaper" {
  function_name    = "iv-reaper"
  role             = aws_iam_role.reaper.arn
  handler          = "handler.handler"
  runtime          = "python3.12"
  filename         = data.archive_file.reaper.output_path
  source_code_hash = data.archive_file.reaper.output_base64sha256
  timeout          = 60

  environment {
    variables = {
      REAPER_GRACE_MINUTES    = tostring(var.reaper_grace_minutes)
      REAPER_ORPHAN_AGE_HOURS = tostring(var.reaper_orphan_age_hours)
    }
  }

  depends_on = [aws_cloudwatch_log_group.reaper]
}

resource "aws_cloudwatch_event_rule" "reaper" {
  name                = "iv-reaper"
  description         = "Terminate interview hosts past their TTL"
  schedule_expression = "rate(${var.reaper_interval_minutes} minutes)"
}

resource "aws_cloudwatch_event_target" "reaper" {
  rule      = aws_cloudwatch_event_rule.reaper.name
  target_id = "iv-reaper"
  arn       = aws_lambda_function.reaper.arn
}

resource "aws_lambda_permission" "reaper" {
  statement_id  = "AllowExecutionFromEventBridge"
  action        = "lambda:InvokeFunction"
  function_name = aws_lambda_function.reaper.function_name
  principal     = "events.amazonaws.com"
  source_arn    = aws_cloudwatch_event_rule.reaper.arn
}

output "reaper_log_group" {
  value       = aws_cloudwatch_log_group.reaper.name
  description = "Where the reaper says what it looked at and what it terminated"
}
