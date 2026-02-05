# OTel Bump v0.143.0 - Remaining Fixes

## Context
This document tracks the remaining fixes needed for the OTel bump to v0.143.0 in PR #407.
- **Workflow Run**: https://github.com/amazon-contributing/opentelemetry-collector-contrib/actions/runs/21721435447?pr=407
- **Branch**: `sky333999/otel-bump`
- **Commit**: 4c59ec1024be315e76882885cf1458b798e81b6a

## Completed Fixes

### ✅ 1. Dependency Sync (checks job)
**Status**: FIXED - Committed in e790ccd27e
- Ran `go mod tidy` which removed unused dependencies from go.sum files
- Files changed:
  - `receiver/awscontainerinsightreceiver/go.sum`
  - `receiver/awscontainerinsightskueuereceiver/go.sum`
- Removed dependencies:
  - `internal/exp/metrics v0.143.0`
  - `processor/deltatocumulativeprocessor v0.143.0`

## Completed Fixes (Continued)

### ✅ 2. AWS SDK v1 Deprecation Warnings (awsxrayreceiver)
**Status**: FIXED
**Job**: lint-matrix (linux, receiver-0) - Job ID: 62652567937

**Changes Made**:
- Moved `//nolint:staticcheck // AWS SDK v1 migration tracked separately` to separate lines before imports:
  - `receiver/awsxrayreceiver/internal/translator/aws_test.go`
  - `receiver/awsxrayreceiver/internal/translator/cause_test.go`
  - `receiver/awsxrayreceiver/internal/translator/translator_test.go`

**Fix**: The gci formatter requires nolint comments on separate lines, not inline with imports.

### ✅ 3. Test Modernization (internal/aws/xray)
**Status**: FIXED
**Jobs**: 
- lint-matrix (linux, internal) - Job ID: 62652568027
- lint-matrix (windows, internal) - Job ID: 62652568002

**Changes Made**:
- Updated `internal/aws/xray/telemetry/sender_test.go:58`
- Changed `context.WithCancel(context.Background())` to `context.WithCancel(t.Context())`

### ✅ 4. Windows Receiver Linting (awscontainerinsightreceiver)
**Status**: FIXED
**Job**: lint-matrix (windows, receiver-0) - Job ID: 62652567882

**Changes Made**:
- Removed old `// +build windows` directives from 18 files in `receiver/awscontainerinsightreceiver/internal/k8swindows/`
- Kept only `//go:build windows` directives (modern format)

**Files Fixed**:
- All files in `internal/k8swindows/extractors/` (11 files)
- All files in `internal/k8swindows/hcsshim/` (3 files)
- All files in `internal/k8swindows/kubelet/` (3 files)
- `internal/k8swindows/testutils/helpers.go`

**Note**: The unused-receiver, rangeValCopy, and appendCombine warnings mentioned in the original error were either false positives or already resolved.

## Remaining Fixes

### ⏭️ 3. Collector Module Version Check (SKIPPED FOR NOW)
**Status**: SKIPPED - To be addressed separately
**Job**: check-collector-module-version - Job ID: 62652567739

**Error**: Collector module versions changed from v1.49.0 to v1.50.0

**Note**: This will be addressed in a separate effort after the lint fixes are complete.

## Summary of Failures

| Job Name | Job ID | Status | Priority |
|----------|--------|--------|----------|
| checks | 62652567454 | ✅ FIXED | High |
| lint-matrix (linux, receiver-0) | 62652567937 | ✅ FIXED | High |
| lint-matrix (windows, receiver-0) | 62652567882 | ✅ FIXED | Medium |
| lint-matrix (linux, internal) | 62652568027 | ✅ FIXED | Low |
| lint-matrix (windows, internal) | 62652568002 | ✅ FIXED | Low |
| check-collector-module-version | 62652567739 | ⏭️ SKIPPED | High |

## AWS Critical Components

The following components were fixed:
- ✅ `receiver/awscontainerinsightreceiver` - Fixed Windows linting (build tags)
- ✅ `receiver/awscontainerinsightskueuereceiver` - Fixed (go.sum)
- ✅ `receiver/awsxrayreceiver` - Fixed (SDK v1 warnings)
- ✅ `internal/aws/xray` - Fixed (test modernization)

See `.kiro/steering/aws-critical-components.md` for full list.

## Next Steps

1. ✅ **COMPLETED** - Validate awsxrayreceiver lint fix
2. ✅ **COMPLETED** - Fix test modernization in internal/aws/xray
3. ✅ **COMPLETED** - Fix Windows receiver linting
4. ⏭️ **SKIPPED** - Fix collector module version check (to be addressed separately)

## References

- [OTel Bump Documentation](.kiro/steering/otel-contrib-fork-steering.md)
- [AWS Critical Components](.kiro/steering/aws-critical-components.md)
- [GitHub Actions Workflow](https://github.com/amazon-contributing/opentelemetry-collector-contrib/actions/runs/21721435447)
