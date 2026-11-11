# Contributing

Use Go 1.27+, Node 22+, Docker Compose, and Python 3.12. Run `make test`, `make test-integration`, `make smoke`, and the Playwright suite for behavioral changes. Keep the synthetic demo adapter consistent with user-visible workflows, but keep its simulated behavior clearly labeled. Update the OpenAPI contract and document architectural tradeoffs. Never commit credentials, environment files, or Terraform state. Include actual benchmark conditions with performance claims.
