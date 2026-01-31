# Security Policy

## Scope

CARD is a local-only CLI tool. It does not run servers, accept network connections, or transmit data externally. Security concerns are primarily around:

- Local file handling
- Execution of the `claude` CLI subprocess
- Markdown/YAML parsing

## Reporting a Vulnerability

If you discover a security issue, please open a [GitHub issue](https://github.com/kokistudios/card/issues/new).

**Important:** If the vulnerability could be exploited before a fix is released, please avoid including specific exploit details in the public issue. Instead, describe the general nature of the problem and we'll coordinate privately on the details.

## Supported Versions

Only the latest release is supported with security updates.
