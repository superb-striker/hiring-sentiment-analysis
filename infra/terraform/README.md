# Infrastructure -`hiring_records` table

Terraform for the DynamoDB table all four Go modules (`hiringdb`, and via
it, ingestion/coordinator/worker/analysis) read and write.

## Local development (dynamodb-local)

The same Terraform files apply against a local DynamoDB -no separate
hand-maintained schema script to keep in sync. `versions.tf` points the AWS
provider at `var.dynamodb_endpoint` when it's set and skips the
credential/account checks that would otherwise fail against a fake local
account.

```bash
docker run -d -p 8000:8000 amazon/dynamodb-local:latest

cd infra/terraform
terraform init
terraform apply -var="dynamodb_endpoint=http://localhost:8000" -auto-approve
```

Or via the repo-root helper script, which does the same thing:

```bash
./scripts/local-dynamodb-apply.sh
```

Re-running `apply` is safe (Terraform no-ops if the table already matches);
`dynamodb-local -inMemory` means the table (and its data) disappears when
the container stops, so you'll re-`apply` each time you restart it.

To tear down: `terraform destroy -var="dynamodb_endpoint=http://localhost:8000"`.

## Real AWS deployment

```bash
cd infra/terraform
cp terraform.tfvars.example terraform.tfvars   # edit as needed
terraform init
terraform plan     # dynamodb_endpoint defaults to "" -> real AWS
terraform apply
```

Uses whatever AWS credentials are already configured (profile, env vars, or
an assumed role) -nothing local-specific runs when `dynamodb_endpoint` is
unset. Set up remote state (an S3 backend, etc.) before applying against a
real account if more than one person will run this -not included here
since that's environment-specific.

## Schema notes

- On-demand (`PAY_PER_REQUEST`) billing -no capacity planning needed, cost
  scales with actual usage, appropriate for dev and for genuinely
  unpredictable Reddit-driven write volume. Revisit if steady-state traffic
  ever makes provisioned + autoscaling cheaper.
- All three GSIs project `ALL` attributes, so every access pattern in
  `hiringdb` is a single Query with everything it needs, no follow-up
  `GetItem`. Cheap to do on-demand; would need reconsidering under
  provisioned capacity or very large items.
