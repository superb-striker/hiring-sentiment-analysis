resource "aws_dynamodb_table" "hiring_records" {
  name         = var.table_name
  billing_mode = "PAY_PER_REQUEST" 
  hash_key     = "company"
  range_key    = "timestamp"

  attribute {
    name = "company"
    type = "S"
  }

  attribute {
    name = "timestamp"
    type = "S"
  }

  attribute {
    name = "status"
    type = "S"
  }

  attribute {
    name = "complaint_category"
    type = "S"
  }

  attribute {
    name = "record_id"
    type = "S"
  }

  global_secondary_index {
    name            = "status-index"
    hash_key        = "status"
    range_key       = "timestamp"
    projection_type = "ALL"
  }

  global_secondary_index {
    name            = "complaint_category-index"
    hash_key        = "complaint_category"
    range_key       = "timestamp"
    projection_type = "ALL"
  }

  global_secondary_index {
    name            = "record_id-index"
    hash_key        = "record_id"
    projection_type = "ALL"
  }

  tags = var.dynamodb_endpoint == "" ? var.tags : null
}
