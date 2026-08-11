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
  region = var.aws_region

  access_key                  = var.dynamodb_endpoint != "" ? "local" : null
  secret_key                  = var.dynamodb_endpoint != "" ? "local" : null
  skip_credentials_validation = var.dynamodb_endpoint != ""
  skip_requesting_account_id  = var.dynamodb_endpoint != ""
  skip_metadata_api_check     = var.dynamodb_endpoint != ""

  dynamic "endpoints" {
    for_each = var.dynamodb_endpoint != "" ? [1] : []
    content {
      dynamodb = var.dynamodb_endpoint
    }
  }
}
