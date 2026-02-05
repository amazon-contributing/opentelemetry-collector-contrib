---
inclusion: manual
---
# Merge Active Mode

This steering file should be **manually included** (via `#merge-active-mode`) when actively performing an OTel bump merge from upstream.

## Overview

"Merge Active Mode" indicates you are in the process of merging upstream OpenTelemetry Collector Contrib changes into the AWS fork. This mode relies on specific branch conventions and provides additional context for merge conflict resolution.

## Branch Structure

When in Merge Active Mode, the repository should have **5 local branches**:

### 1. `aws-cwa-dev` (AWS Mainline)
- **Purpose**: The mainline branch for AWS purposes
- **Usage**: This is what CloudWatch Agent actually uses
- **Represents**: Current production state of the AWS fork
- **Note**: May have additional changes since the last OTel bump (i.e., since `bump/downstream-old` was merged)

### 2. `bump/downstream-old` (Previous AWS State)
- **Purpose**: Snapshot of `aws-cwa-dev` at the time of the last OTel bump
- **Usage**: Baseline for understanding what AWS customizations existed previously
- **Represents**: The AWS fork state before the last upstream merge

### 3. `bump/upstream-old` (Previous Upstream State)
- **Purpose**: The upstream commit used in the last OTel bump
- **Usage**: Baseline for understanding what upstream looked like previously
- **Represents**: The upstream release that was merged in the last OTel bump

### 4. `bump/upstream-new` (New Upstream State)
- **Purpose**: The upstream commit for the current OTel bump
- **Usage**: The new upstream code being merged
- **Represents**: The upstream release being merged now

### 5. `bump/downstream-new` (Merge Result - Work in Progress)
- **Purpose**: Result of merging `bump/upstream-new` into latest `aws-cwa-dev`
- **Usage**: Active merge work happens here
- **Represents**: The future state that will be merged back to `aws-cwa-dev` via PR
- **Note**: This is where merge conflicts are resolved

## Branch Verification

Before starting merge work, verify all branches exist:

```bash
git branch | grep -E "(aws-cwa-dev|bump/downstream-old|bump/upstream-old|bump/upstream-new|bump/downstream-new)"
```

Expected output should show all 5 branches.

## Divergence Analysis Workflow

The branch structure enables powerful divergence analysis:

### Understanding Changes Over Time

1. **Previous AWS customizations** (last OTel bump):
   ```bash
   git diff bump/upstream-old..bump/downstream-old <file>
   ```
   Shows what AWS changed in the previous OTel bump

2. **Upstream evolution** (between bumps):
   ```bash
   git diff bump/upstream-old..bump/upstream-new <file>
   ```
   Shows how upstream changed between releases

3. **AWS changes since last bump**:
   ```bash
   git diff bump/downstream-old..aws-cwa-dev <file>
   ```
   Shows AWS changes made after the last OTel bump

4. **Current merge state**:
   ```bash
   git diff aws-cwa-dev..bump/downstream-new <file>
   ```
   Shows what will change when the merge is completed

### Comprehensive Component Analysis

When resolving conflicts for a component, perform a thorough three-way analysis:

#### Step 1: Understand the Scope of Changes

```bash
# Get statistics for each comparison
git diff --stat bump/downstream-old..HEAD -- <component-path>/
git diff --stat bump/downstream-old..aws-cwa-dev -- <component-path>/
git diff --stat bump/upstream-new..HEAD -- <component-path>/
```

This shows:
- Total changes since last bump
- What AWS team changed (if anything)
- Current divergence from upstream

#### Step 2: Identify Upstream Commits

```bash
# List all commits that touched the component
git log bump/upstream-old..bump/upstream-new --oneline -- <component-path>/

# Filter for component-specific changes (not just dependency updates)
git log bump/upstream-old..bump/upstream-new --oneline --no-merges \
  --grep="<component-name>" -- <component-path>/

# Get detailed view of a specific commit
git show <commit-hash> --stat
git show <commit-hash> -- <specific-file>
```

#### Step 3: Research Commit Context

For each significant commit:

1. **View the full commit message**:
   ```bash
   git show <commit-hash> --no-patch
   ```
   Look for:
   - PR number (e.g., #37297)
   - Issue references (e.g., Fixes #31382)
   - Description of what changed and why

2. **Look up the GitHub PR using GitHub CLI**:
   ```bash
   # Get PR details
   gh pr view <PR-number> --repo open-telemetry/opentelemetry-collector-contrib \
     --json title,body,url
   
   # For core collector PRs
   gh pr view <PR-number> --repo open-telemetry/opentelemetry-collector \
     --json title,body,url
   ```
   
   Read the PR description for:
   - Motivation and context
   - Design decisions
   - Breaking changes
   - Related issues

3. **Look up linked issues**:
   ```bash
   # Get issue details
   gh issue view <issue-number> --repo open-telemetry/opentelemetry-collector-contrib \
     --json title,body,url
   ```
   
   Understand:
   - The problem being solved
   - Whether it affects AWS use cases
   - Community discussion and requirements

#### Step 4: Analyze AWS Team Changes

```bash
# List AWS commits since last bump
git log bump/downstream-old..aws-cwa-dev --oneline -- <component-path>/

# View each commit
git show <commit-hash>
```

Determine if AWS changes:
- Are just dependency updates (can be ignored)
- Add new functionality (must be preserved)
- Fix bugs (must be preserved)
- Conflict with upstream changes (need careful merge)

#### Step 5: Analyze Current Divergence

```bash
# See what we're changing from upstream
git diff bump/upstream-new..HEAD -- <component-path>/<file>

# For each divergent file, understand:
# - What upstream changed
# - What we're keeping different
# - Why we're diverging
```

For each divergent section, ask:
- **Is this an AWS customization?** (SDK v1, type changes, etc.)
- **Is this a bug fix?** (should we upstream it?)
- **Is this a feature?** (should we upstream it?)
- **Is this technical debt?** (can we adopt upstream instead?)

#### Step 6: Document Decisions

Create a merge analysis document (see `awscloudwatchlogsexporter-merge-analysis.md` as example):

```markdown
# <Component> Merge Analysis

## Summary Statistics
- Changes since last bump
- AWS changes between bumps  
- Divergence from upstream

## Key Upstream Changes
For each major commit:
- Commit hash and author
- What it does
- AWS decision (adopted/rejected/partial)
- Rationale

## AWS Customizations Preserved
- List each customization
- Explain why it's needed
- Document impact

## Merge Conflict Resolution Details
- Problem encountered
- Resolution approach
- Code changes made

## Testing Results
- Number of tests
- Any failures and fixes

## Recommendations
- Short term actions
- Long term considerations
```

### Example Analysis Workflow

```bash
# 1. Get overview
git diff --stat bump/downstream-old..HEAD -- exporter/awsxrayexporter/
git diff --stat bump/downstream-old..aws-cwa-dev -- exporter/awsxrayexporter/
git diff --stat bump/upstream-new..HEAD -- exporter/awsxrayexporter/

# 2. Find upstream commits
git log bump/upstream-old..bump/upstream-new --oneline \
  --grep="awsxray" -- exporter/awsxrayexporter/

# 3. Research each commit
git show 99934f4368 --no-patch  # Read commit message
# Then look up PR #39314 on GitHub for full context

# 4. Check AWS changes
git log bump/downstream-old..aws-cwa-dev --oneline -- exporter/awsxrayexporter/

# 5. Analyze divergence
git diff bump/upstream-new..HEAD -- exporter/awsxrayexporter/config.go

# 6. Document in merge analysis file
```

### Divergence Patterns

Use the branches to identify:

- **Was it divergent before?** Compare `bump/upstream-old` vs `bump/downstream-old`
- **Is it still divergent?** Compare `bump/upstream-new` vs `bump/downstream-new`
- **Did divergence increase?** Compare the diff sizes or complexity
- **Did divergence decrease?** Check if AWS customizations were upstreamed
- **New divergence?** Files that match upstream-old but differ in downstream-new

### Example Analysis Commands

```bash
# Check if a file was previously customized
git diff bump/upstream-old bump/downstream-old -- <file>

# Check if the same file is still customized after merge
git diff bump/upstream-new bump/downstream-new -- <file>

# See what changed in upstream between bumps
git diff bump/upstream-old bump/upstream-new -- <file>

# See what AWS changed since last bump
git diff bump/downstream-old aws-cwa-dev -- <file>

# Three-way comparison for complex conflicts
git diff bump/upstream-old bump/upstream-new -- <file>  # Upstream changes
git diff bump/upstream-old bump/downstream-old -- <file>  # Previous AWS changes
git diff bump/upstream-new bump/downstream-new -- <file>  # Current merge result
```

## Merge Conflict Resolution Strategy

When resolving conflicts in `bump/downstream-new`:

### 1. Assess Historical Context
- Check if the file was divergent in the last bump
- Understand why AWS made previous customizations
- Review git history and linked PRs/issues

### 2. Analyze Current Changes
- What did upstream change and why?
- What did AWS change since the last bump?
- Are the changes compatible or conflicting?

### 3. Make Informed Decisions
- If previously divergent and still needed: Preserve AWS customizations carefully
- If upstream improved the code: Consider adopting upstream changes
- If both have valuable changes: Merge both thoughtfully
- If component is not critical: Prefer upstream to minimize divergence

### 4. Evaluate Backwards Compatibility

**CRITICAL**: Before deciding to keep AWS customizations or adopt upstream changes, perform backwards compatibility analysis:

#### Questions to Ask:

1. **Does the upstream change break existing AWS configs?**
   - Check CloudWatch Agent sample configs
   - Look for field renames, type changes, removed fields
   - Test if existing YAML configs would fail validation

2. **Is the AWS customization still necessary?**
   - Was it a workaround for an upstream limitation?
   - Has upstream fixed the underlying issue?
   - Is it AWS-specific or generally useful?

3. **Can we adopt upstream AND preserve AWS functionality?**
   - Are the changes complementary?
   - Can we keep both with minor adjustments?
   - Example: Optional pattern is backwards compatible with plain structs

4. **What's the maintenance burden?**
   - Keeping AWS customization: Ongoing divergence maintenance
   - Adopting upstream: Potential config migration for users
   - Which has lower long-term cost?

#### Decision Framework:

```
┌─────────────────────────────────────────────────────────────┐
│ Is upstream change backwards compatible with AWS configs?   │
└────────────┬────────────────────────────────────────────────┘
             │
      ┌──────┴──────┐
      │             │
     YES           NO
      │             │
      ▼             ▼
┌─────────────┐  ┌──────────────────────────────────────┐
│ ADOPT       │  │ Does AWS customization provide value? │
│ UPSTREAM    │  └────────────┬─────────────────────────┘
└─────────────┘               │
                       ┌──────┴──────┐
                       │             │
                      YES           NO
                       │             │
                       ▼             ▼
              ┌────────────────┐  ┌─────────────┐
              │ KEEP AWS       │  │ ADOPT       │
              │ CUSTOMIZATION  │  │ UPSTREAM    │
              └────────────────┘  └─────────────┘
```

#### Consultation Required:

**ALWAYS consult before making these decisions**:

1. **Type changes** (int32→int64, string→*string, etc.)
   - May affect API compatibility
   - May require validation function updates
   - Check with team on AWS API requirements

2. **Structural changes** (plain struct → Optional, new required fields)
   - May break existing configs
   - May require migration guide
   - Verify against CloudWatch Agent configs

3. **Feature additions** (new fields, new functionality)
   - Assess value for AWS users
   - Check if it conflicts with AWS-specific features
   - Consider adoption vs. divergence trade-offs

4. **API signature changes** (parameter order, return types)
   - May indicate architectural changes
   - May affect downstream consumers
   - Understand the motivation before deciding

#### Process:

1. **Analyze the change** using the steps above
2. **Document your findings**:
   - What changed and why (from PR/issue research)
   - Backwards compatibility assessment
   - Impact on CloudWatch Agent
   - Pros/cons of each option

3. **Present options to team**:
   - Option A: Keep AWS customization (explain why)
   - Option B: Adopt upstream (explain benefits)
   - Option C: Hybrid approach (if applicable)
   - Your recommendation with rationale

4. **Wait for approval** before implementing
5. **Document the decision** in merge analysis file

#### Example Consultation:

```
Found upstream change: Optional Queue Config (#44320)

Analysis:
- Upstream wraps QueueSettings in configoptional.Optional
- Allows users to set enabled: true/false
- CloudWatch Agent configs already have enabled: true
- Backwards compatible - existing configs work unchanged
- Provides new capability to disable queueing

Options:
A) Keep plain struct (current AWS): Simpler but less flexible
B) Adopt Optional (upstream): More flexible, aligns with upstream
C) Hybrid: Keep plain struct, wrap at usage (inconsistent)

Recommendation: Adopt Optional (Option B)
- Backwards compatible
- Aligns with upstream
- No downside

Awaiting approval to proceed...
```

### 5. Document Decisions
- Add clear commit messages explaining resolution choices
- Reference GitHub PRs/issues that provide context
- Note any technical debt or future upstreaming opportunities

## Workflow Phases

### Phase 1: Setup
- Ensure all 5 branches exist and are at correct commits
- Verify `bump/downstream-new` is created from latest `aws-cwa-dev`
- Begin merge of `bump/upstream-new` into `bump/downstream-new`

### Phase 2: Conflict Resolution
- Work through merge conflicts file by file
- Use divergence analysis to inform decisions
- Test changes for critical components
- Commit resolved conflicts with detailed messages

### Phase 3: Validation
- Build and test all critical components
- Verify CloudWatch Agent integration
- Review changes with team
- Document significant decisions

### Phase 4: Completion
- Create PR from `bump/downstream-new` to `aws-cwa-dev`
- Update `aws-critical-components.md` if component list changed
- Tag the merge commit for future reference
- Archive or delete bump branches after merge

## Quick Reference Commands

```bash
# List all bump branches
git branch | grep bump

# Show branch commit info
git log --oneline --graph --all --decorate | grep -E "(aws-cwa-dev|bump/)"

# Compare file across all relevant branches
for branch in bump/upstream-old bump/downstream-old bump/upstream-new bump/downstream-new aws-cwa-dev; do
  echo "=== $branch ==="
  git show $branch:<file> | head -20
done

# Find files that differ between upstream-new and downstream-new (current divergence)
git diff --name-only bump/upstream-new bump/downstream-new

# Find files that differed in last bump (previous divergence)
git diff --name-only bump/upstream-old bump/downstream-old
```

## Tips for Effective Merge Work

1. **Work incrementally**: Resolve conflicts in small batches, commit often
2. **Test frequently**: Build and test after resolving conflicts in critical components
3. **Document thoroughly**: Future you (and teammates) will thank you
4. **Use git history**: `git log`, `git blame`, and `git show` are your friends
5. **Research context**: Look up GitHub PRs and issues for both upstream and AWS changes
6. **Ask for help**: Complex conflicts may need team discussion
7. **Consider upstreaming**: If AWS changes are valuable, plan to contribute them upstream
8. **Stage before committing**: Always stage changes and allow review before committing - use `git add` but wait for explicit approval before `git commit`

## Commit Guidelines

**IMPORTANT**: When working on merge conflicts:
- Stage changes with `git add` after fixing issues
- Show diffs or summaries for review
- Wait for explicit approval before running `git commit`
- Do NOT automatically commit changes without review
- Use `git reset --soft HEAD~1` if a commit needs to be undone for review (only safe when not pushed)

## Deactivating Merge Active Mode

When the OTel bump is complete:
- Remove the `#merge-active-mode` context reference
- The steering file will no longer be included automatically
- Re-activate for the next OTel bump by including it again
