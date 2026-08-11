variable "aws_region" {
  description = "AWS region to deploy into. Ignored (but still required by the provider) when dynamodb_endpoint is set."
  type        = string
  default     = "us-east-1"
}

variable "dynamodb_endpoint" {
  description = <<-EOT
    Set to a local DynamoDB endpoint (e.g. "http://localhost:8000") to apply
    this table definition against dynamodb-local instead of real AWS. Leave
    empty (the default) for a real deployment.
  EOT
  type        = string
  default     = ""
}

variable "table_name" {
  description = "Name of the hiring_records table. Matches shared.TableName in the Go code - change both together."
  type        = string
  default     = "hiring_records"
}

variable "tags" {
  description = "Tags applied to the table (ignored against dynamodb-local, which doesn't support tagging)."
  type        = map(string)
  default = {
    Project = "hiring-sentiment"
    Environment = "production"
  }
}
