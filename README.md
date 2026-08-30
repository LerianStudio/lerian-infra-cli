# lerian-infra-cli
CLI and Go library for provisioning the infrastructure the Lerian products run on. Drives the Lerian Terraform and CloudFormation templates across AWS, GCP and Azure: discovers deployable stacks, runs them in dependency order, guards which account each environment may touch, and hands endpoints to the Helm   charts.
