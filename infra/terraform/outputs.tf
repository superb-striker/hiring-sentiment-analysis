output "table_name" {
  value = aws_dynamodb_table.hiring_records.name
}

output "table_arn" {
  value = aws_dynamodb_table.hiring_records.arn
}
