# Cloud Auth Extension

The `cloudauth` extension provides OIDC token management for authenticating
to AWS from non-AWS environments (e.g., Azure VMs). It auto-detects the cloud
provider, fetches OIDC tokens, and writes them to a file for the credential
chain to use with `AssumeRoleWithWebIdentity`.

## How It Works

1. Extension detects the cloud provider (or reads a user-managed token file)
2. Fetches an OIDC token and writes it to disk
3. Sets `AWS_WEB_IDENTITY_TOKEN_FILE` env var pointing to the token file
4. The collector's credential chain uses each component's `role_arn` to call
   `AssumeRoleWithWebIdentity`

## Supported Providers

- **Azure** — Uses Azure IMDS managed identity tokens. Detection leverages
  the existing `internal/metadataproviders/azure` package.
- **File** — Reads a user-managed token from disk (`token_file` config option).

## Configuration

```yaml
extensions:
  cloudauth:
    # Optional: path to user-managed OIDC token file (skips auto-detection)
    token_file: /var/run/oidc/token
    # Optional: audience/resource claim for the OIDC token request
    sts_resource: https://monitoring.azure.com/
    # Optional: directory for the fetched token file (defaults to os.TempDir())
    token_dir: /opt/aws/amazon-cloudwatch-agent/var
```

All fields are optional. With an empty config, the extension auto-detects Azure
and uses default settings.
