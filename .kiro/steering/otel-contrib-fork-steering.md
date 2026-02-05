---
inclusion: always
---
# AWS Fork of OpenTelemetry Collector Contrib

## Repository Overview

This repository is the **AWS fork** of [opentelemetry-collector-contrib](https://github.com/open-telemetry/opentelemetry-collector-contrib). It contains OpenTelemetry components (receivers, processors, exporters, connectors, extensions) that are imported and used by the [Amazon CloudWatch Agent](https://github.com/aws/amazon-cloudwatch-agent).

**Module Path**: `github.com/amazon-contributing/opentelemetry-collector-contrib`

## Fork Maintenance & OTel Bump Process

### Update Cycle
- The fork is updated from upstream **every few months** in a process called **"OTel bump"**
- Between bumps, AWS continues making changes to the fork for AWS-specific use cases
- Some changes are upstreamed first, then brought back into the fork
- Other changes remain AWS-specific customizations

### Merge Conflict Resolution Strategy

When performing an OTel bump, merge conflicts are common and require careful analysis:

#### Decision Framework

1. **For components NOT used by CloudWatch Agent**:
   - Prefer upstream changes to minimize divergence
   - Less critical to preserve AWS customizations

2. **For components USED by CloudWatch Agent** (see aws-critical-components.md):
   - Requires careful analysis of both upstream and fork changes
   - Consider:
     - Is the upstream change a valuable improvement we should adopt?
     - Does our AWS customization address a specific requirement that must be preserved?
     - Can both changes be merged harmoniously?

#### Analysis Techniques

**Use Git history and annotations**:
```bash
# View commit history for a file
git log -p <file>

# Find associated GitHub PRs
git log --grep="PR" <file>

# View blame to understand change context
git blame <file>
```

**Research linked PRs and issues**:
- Look up GitHub PR descriptions
- Read associated GitHub issues
- Understand the motivation behind changes
- Check if changes address bugs, features, or performance

**Compare implementations**:
- Understand what problem each version solves
- Assess impact on CloudWatch Agent functionality
- Consider long-term maintenance burden

## Critical Guidelines

### ⚠️ EXTREME CAUTION REQUIRED

**Before making ANY changes to this repository**:

1. **Understand the component's usage**: Check if it's used by CloudWatch Agent (see aws-critical-components.md)
2. **Review git history**: Understand why current code exists
3. **Research upstream context**: Look up related PRs and issues
4. **Assess impact**: Consider downstream effects on CloudWatch Agent
5. **Minimize divergence**: When safe, prefer staying close to upstream

### Change Safety Checklist

- [ ] Verified component usage in CloudWatch Agent
- [ ] Reviewed git history for context
- [ ] Researched upstream PRs/issues if applicable
- [ ] Understood AWS-specific customizations
- [ ] Assessed impact on CloudWatch Agent functionality
- [ ] Considered long-term maintenance implications
- [ ] Tested changes if modifying critical components

## AWS-Specific Components

### Override Packages
- `override/aws/` - AWS credential and IMDS retry logic customizations

### Extensions
- `extension/awsmiddleware/` - AWS middleware for SDK integration
- `extension/awsproxy/` - AWS proxy configuration

### Other AWS Components
See `aws-critical-components.md` for the complete list of components used by CloudWatch Agent.

## Repository Structure

```
.
├── connector/          # Pipeline connectors
├── exporter/          # Data exporters (e.g., CloudWatch, S3, Kinesis)
├── extension/         # Extensions (auth, observers, etc.)
├── processor/         # Data processors
├── receiver/          # Telemetry receivers
├── override/aws/      # AWS-specific overrides
├── pkg/               # Shared packages
└── internal/          # Internal utilities
```

## Build & Test

Each component directory contains a `Makefile` that includes `Makefile.Common` from the repository root. This provides a consistent set of build and test targets across all components.

### Common Makefile Targets

Navigate to a component directory first, then use these targets:

```bash
cd receiver/awscontainerinsightreceiver
```

| Target | Description |
|--------|-------------|
| `make test` | Run all unit tests with race detection (uses gotestsum with auto-retry on failures) |
| `make lint` | Run golangci-lint, license checks, and misspell checks |
| `make fmt` | Format code with gofumpt and goimports |
| `make tidy` | Clean up go.mod and go.sum |
| `make generate` | Run go generate for code generation |
| `make test-with-cover` | Run tests with coverage reporting |
| `make mod-integration-test` | Run integration tests (tests with `integration` build tag) |
| `make govulncheck` | Check for known vulnerabilities in dependencies |
| `make common` | Run both `lint` and `test` (default target) |

### Running Specific Tests

```bash
# Using make (preferred - includes race detection and proper timeouts)
make test

# Run a specific test directly
go test -v -run TestName ./...

# Run tests in a specific package
go test -v ./internal/gpu/...
```

### Test Configuration

The Makefile.Common sets these test defaults:
- **Timeout**: 900 seconds (15 minutes)
- **Race detection**: Enabled (except on Windows ARM64)
- **Parallelism**: 4 concurrent tests
- **Auto-retry**: Failed tests are automatically retried once via gotestsum

### Building All Packages

From the repository root, you can build/test all packages:

```bash
# Build all packages recursively
make for-all CMD="make build"

# Test all packages recursively  
make for-all CMD="make test"
```

## Dependency Management

### Local Overrides
Individual component `go.mod` files may reference local overrides:
```go
replace github.com/open-telemetry/opentelemetry-collector-contrib/pkg/aws => ../../pkg/aws
```

**Pay attention to these when**:
- Making changes to shared packages
- Resolving merge conflicts
- Understanding component dependencies

## Related Resources

- [CloudWatch Agent Repository](https://github.com/aws/amazon-cloudwatch-agent)
- [Upstream OpenTelemetry Collector Contrib](https://github.com/open-telemetry/opentelemetry-collector-contrib)
- [OpenTelemetry Documentation](https://opentelemetry.io/docs/)

## OTel Bump Documentation

Issues encountered and resolved during OTel bump merges are documented in version-specific folders under `.kiro/steering/`:

- `otel-bump-v0.143.0/` - Documentation for v0.143.0 bump issues

These documents capture:
- Original error messages and test failures
- Root cause analysis
- Solution approach and code changes
- Key learnings for future reference

## Changelog Management

Changes are tracked in `.chloggen/` directory:
- Add YAML files for changes following the template
- Entries are compiled into CHANGELOG.md during releases

