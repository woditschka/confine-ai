---
name: commit-safety
description: >-
  Pre-commit safety checks for secrets, credentials, local settings,
  and sensitive file patterns. Load before committing or when reviewing
  staged changes for accidental exposure.
compatibility:
  - claude-code
  - opencode
  - github-copilot
metadata:
  version: "1.0"
  author: team
---

## Purpose

Prevent accidental commits of secrets, credentials, private keys, and machine-local settings. Run these checks on staged files before every commit.

## Forbidden File Patterns

Reject any staged file matching these patterns:

| Pattern | Reason |
|---|---|
| `.env`, `.env.*` | Environment variables often contain secrets |
| `*.pem`, `*.key`, `*.p12`, `*.pfx`, `*.jks` | Private keys and keystores |
| `*credentials*`, `*secret*` | Likely credential files |
| `*.local.json`, `*.local.yaml`, `*.local.toml` | Machine-local settings |
| `id_rsa`, `id_ed25519`, `id_ecdsa` | SSH private keys |
| `*.keystore`, `*.truststore` | Java key/trust stores |
| `.npmrc`, `.pypirc` | Package registry tokens |
| `.netrc`, `.docker/config.json` | Auth config files |

### Project-Specific Exclusions

| File | Reason |
|---|---|
| `.claude/settings.local.json` | Machine-local Claude Code permissions |

## Forbidden Content Patterns

Scan staged file content (diffs only) for these patterns:

| Pattern | Example Match |
|---|---|
| `-----BEGIN.*PRIVATE KEY-----` | PEM private keys |
| `sk-[a-zA-Z0-9\-]{20,}` | OpenAI/Anthropic API keys (including `sk-ant-`, `sk-proj-` formats) |
| `ghp_[a-zA-Z0-9]{36}` | GitHub personal access tokens |
| `ghs_[a-zA-Z0-9]{36}` | GitHub server tokens |
| `glpat-[a-zA-Z0-9\-]{20,}` | GitLab personal access tokens |
| `AKIA[0-9A-Z]{16}` | AWS access key IDs |
| `xox[bpors]-[a-zA-Z0-9\-]+` | Slack tokens |
| `password\s*[:=]\s*["'][^"']+["']` | Hardcoded passwords |
| `token\s*[:=]\s*["'][^"']+["']` | Hardcoded tokens |

**Test fixture exception:** Files under `testdata/` directories or lines containing `// test fixture` are excluded from content pattern matches.

## Check Procedure

### 1. List staged files

```bash
git diff --cached --name-only --diff-filter=ACM
```

### 2. Match against forbidden file patterns

Compare each staged file path against the forbidden file patterns table. Report any matches.

### 3. Scan staged diffs for forbidden content

```bash
git diff --cached -U0
```

Match diff additions (lines starting with `+`) against forbidden content patterns. Ignore files under `testdata/` directories and lines containing `// test fixture`.

### 4. Cross-check with .gitignore (manual)

Manually verify that all forbidden file patterns have corresponding `.gitignore` entries. Report any missing entries as warnings. This step is not automated by the pre-commit hook.

## Severity

| Finding | Severity | Action |
|---|---|---|
| Private key file staged | BLOCKED | Remove from staging, add to .gitignore |
| API key or token in diff | BLOCKED | Remove secret, use environment variable |
| Machine-local settings staged | BLOCKED | Remove from staging, verify .gitignore |
| Missing .gitignore entry for known pattern | WARNING | Add entry to .gitignore |

## When to Load

- Before every commit (manual or automated).
- When the `code-quality-gate` skill runs.
- When reviewing staged changes.

## Output Format

```
## Commit Safety Check

### Staged Files: [count]

### Findings

[BLOCKED] [file]: [reason]
[WARNING] [file]: [reason]

### Result: PASS | BLOCKED
[If BLOCKED, list remediation steps]
```
