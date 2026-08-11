#!/usr/bin/env bash
# Applies infra/terraform against a local dynamodb-local instance.
# Assumes dynamodb-local is already running on localhost:8000:
#   docker run -d -p 8000:8000 amazon/dynamodb-local:latest
set -euo pipefail

ENDPOINT="${DYNAMODB_ENDPOINT:-http://localhost:8000}"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TF_DIR="$SCRIPT_DIR/../infra/terraform"

cd "$TF_DIR"
terraform init -input=false
terraform apply -input=false -auto-approve -var="dynamodb_endpoint=$ENDPOINT"

echo "hiring_records table applied against $ENDPOINT"
