# Optional AWS deployment

This definition is **not deployed**. Validate it, inspect a plan in your own account, verify currently supported engine versions/instance availability, and review costs before applying. The default ECS desired count is zero. RDS, MSK, ALB, Redis, and NAT incur charges even when the app is stopped.

## What it creates

A two-AZ VPC, public ALB subnets, private app/data subnets, one NAT gateway, HTTPS ALB, ECS Fargate task (Go + Python), ECR repositories, PostgreSQL RDS, Redis, a two-broker provisioned MSK cluster, Secrets Manager database URL, least-scoped execution-role access to the configured secrets, and CloudWatch logs. A single NAT and one Redis node are economical evaluation choices, not highly available production design.

The initial backend configuration uses private Kafka plaintext and private Redis plaintext traffic, protected by security groups; broker replication traffic and database traffic are encrypted. Full client TLS/auth for Kafka and Redis is a follow-up before private production workloads. Database credentials and sensitive resource attributes exist in Terraform state: use an encrypted, access-controlled remote backend. No credentials belong in tfvars or Git.

## Deploy sequence

1. Configure an AWS profile, billing alerts, and a validated ACM certificate. Store an API-key-to-tenant-UUID JSON map in Secrets Manager. These are account-specific inputs, not values to put in chat or this repository.
2. Configure remote state, copy the example inputs, and set your allowed client CIDR. Verify engine versions supported in your region.
3. Run `terraform init`, `terraform fmt -check`, `terraform validate`, and `terraform plan -out=review.tfplan`.
4. Review the actual resources/costs, then apply that reviewed plan using your own approved account workflow. Keep desired_count at zero until images are present.
5. Build `infra/docker/api.Dockerfile` and `workers/embeddings/Dockerfile`, authenticate Docker to the two ECR output repositories, tag both with image_tag, and push. Use immutable release tags, not latest.
6. Provision the organization and services in the private database through a controlled migration task or approved private connection. The application creates the schema on startup; demo seed is deliberately disabled. Use the same tenant UUID as your API-key map. For an evaluation-only dataset, run the local seed logic inside that private task with intentional synthetic-tenant provisioning.
7. Set desired_count=1, review/apply the new plan, and bind a hostname matching the ACM certificate to the ALB. Test health and authenticated API calls.
8. A production browser session/ticket flow is not implemented in this release. The static Pages demo remains independent. Do not put a bearer API key in a VITE variable or client bundle.

## Cleanup

Export any required synthetic evidence, then review `terraform plan -destroy`. RDS deletion protection is intentionally on; explicitly change it after reviewing data retention. A final snapshot is retained and can continue to incur storage charges. Remove ECR images before deleting repositories, review retained snapshots/secrets/logs, and verify AWS billing after teardown. `terraform destroy` is never part of CI.
