# Ghostable CLI

[![Release](https://img.shields.io/github/v/release/ghostable-dev/beta?sort=semver)](https://github.com/ghostable-dev/beta/releases/latest)
[![Downloads](https://img.shields.io/github/downloads/ghostable-dev/beta/total?label=downloads)](https://github.com/ghostable-dev/beta/releases)
[![npm](https://img.shields.io/npm/v/@ghostable/beta?label=npm)](https://www.npmjs.com/package/@ghostable/beta)
[![npm downloads](https://img.shields.io/npm/dm/@ghostable/beta?label=npm%20downloads)](https://www.npmjs.com/package/@ghostable/beta)
[![CI](https://img.shields.io/github/actions/workflow/status/ghostable-dev/beta/ci.yml?branch=main&label=ci)](https://github.com/ghostable-dev/beta/actions/workflows/ci.yml)
[![License](https://img.shields.io/github/license/ghostable-dev/beta)](LICENSE)
[![macOS](https://img.shields.io/badge/macOS-signed%20%26%20notarized-2ea44f)](https://github.com/ghostable-dev/beta/releases/latest)

Ghostable is a serverless CLI for local-first environment management:
projects, environments, variables, devices, schema validation, agent guidance,
and signed activity records.

Ghostable keeps environment management local-first and repository-backed.
Encrypted value files, public device records, signed policy, signed activity,
environment keys, and access grants live under `.ghostable/` and are intended to
be committed to git. Private device keys are stored outside the repository in
the platform's native secret store when available, or in a restrictive
file-backed identity store otherwise.

## Install

Install Ghostable once, then run it inside each project you want to manage.

macOS:

```sh
brew tap ghostable-dev/ghostable
brew install --cask ghostable
```

Node/npm projects:

```sh
npm install @ghostable/beta
```

This installs a project-local `ghostable` binary at
`node_modules/.bin/ghostable`.

Other platforms can download the matching archive from the
[latest release](https://github.com/ghostable-dev/beta/releases/latest) and put
the `ghostable` binary on `PATH`.

## Getting Started

From your project directory:

```sh
ghostable setup
```

`setup` is interactive. It prompts for project and device details, creates the
`default` environment, and asks whether to seed that environment from an
existing `.env` file. Automation can pass `--seed-dotenv` or
`--no-seed-dotenv` to make that choice explicit.

## Build

```sh
make build
```

## Basic Flow

```sh
ghostable env push --env default --file .env --reason "Initial default baseline"
ghostable env pull --env default --file .env
ghostable env diff --env default --file .env
ghostable env diff --from staging --to production
ghostable validate --env default
ghostable hygiene report --env production
ghostable review --env production
ghostable deploy production
```

Most options can be omitted in an interactive terminal. Ghostable prompts for
project and device details when needed, and creates the `default` environment
automatically. If `.env` exists, setup asks whether to seed `default` from it.
Automation and agents should pass flags, use `--seed-dotenv` or
`--no-seed-dotenv` when setup should be non-interactive, and prefer `--json`.

## Commands

- `setup` initializes `.ghostable/`, a local device record, policy, key metadata files,
  and a private local device identity.
- `status` prints project, environment, device, and value counts.
- `env list|create|delete|push|sync|pull|clean|diff|history`
  manages environment-level workflows.
- `validate` checks environment values against schema rules.
- `review` runs changed-code ENV checks and local hard-coded secret scanning.
- `deploy [target] [environment]` writes decrypted values to `.env` or syncs a
  configured provider target.
- `var push|pull|promote|delete|history|context|annotation`
  manages a single variable and its key metadata.
- `schema file|rule|key` manages local validation schema files.
- `hygiene report|rotation|suppress|rotate`
  manages operational hygiene checks, rotation policy, suppressions, and
  environment encryption key rotation.
- `access create|list|status|approvers|share|grants|revoke` manages scoped
  automation credentials and policy grants.
- `device join|list|status` manages human device records.
- `agent init|instructions|capabilities` emits safe instructions and a
  recommended read-only/dry-run command allowlist for coding agents.

## Hygiene

`ghostable hygiene report` checks stored variables and environment metadata for
operational hygiene issues. Variable rotation reminders are opt-in through
`.ghostable/hygiene.yaml`; stable config such as `APP_DEBUG=false` is not
reported as stale unless a rotation rule is configured for that key or
`--stale-after` is passed explicitly.

Unused-variable checks are also opt-in because framework and platform
conventions can use environment values without direct code references:

```sh
ghostable hygiene report --env production --unused
```

Set a project-level rotation rule:

```sh
ghostable hygiene rotation set --key STRIPE_SECRET_KEY --days 90
```

Set an environment-specific override:

```sh
ghostable hygiene rotation set --env production --key STRIPE_SECRET_KEY --days 60
```

Rotation intervals are always whole days.

List configured rules:

```sh
ghostable hygiene rotation list
```

The policy file uses the same project-default plus environment-override shape
as validation rules:

```yaml
rotation:
  keys:
    STRIPE_SECRET_KEY:
      rotationAfterDays: 90
  environments:
    production:
      keys:
        STRIPE_SECRET_KEY:
          rotationAfterDays: 60
```

## Review

`ghostable review` is the local review command. By default, it checks changed
code for both ENV usage drift and hard-coded secrets.

When `--base` is omitted, Ghostable uses the current branch upstream when
available, then `origin/main`, `origin/master`, `main`, `master`, and finally
`HEAD`. CI jobs can still pass an explicit `--base <ref>` for a fixed
comparison point.

For ENV usage drift, it scans changed lines for common ENV access patterns in
PHP/Laravel, JavaScript/TypeScript/Node, Go, Python, Ruby/Rails, Java, C#,
Rust, Swift, and shell/deploy scripts. It compares those references with
encrypted Ghostable values, schema files, `.env.example`, and signed
`.ghostable/` records. GitHub Actions workflow references under `.github/` are
ignored because those often come from GitHub Secrets or Vars.

For hard-coded secrets, it scans changed files locally and redacts findings by
default. Use `ghostable review --secrets-only` when you only want the secret
scan, or `ghostable review --env-only` when you only want the ENV metadata
checks.

The secret-scanning part of `ghostable review` uses manifest ignores from:

```yaml
scan:
  ignores:
    - .git/**
    - node_modules/**
    - .ghostable/environments/**/values/**
    - .ghostable/environments/**/keys/**
```

Use `--json` for machine-readable output. `--show-values` exists for explicit
human debugging, but agents should avoid it unless the user asks.

## Protected Production Access

Production-like environments require local user confirmation before Ghostable
writes, prints, injects, or deploys decrypted values. Environment names and
types containing `prod`, `production`, or `live` are treated as production-like.

Protected local-device commands include:

```sh
ghostable env pull --env production
ghostable env run --env production -- <command>
ghostable var pull --env production --key APP_KEY --show-values
ghostable deploy production
ghostable deploy laravel-forge production --forge-site example.com
ghostable deploy laravel-vapor production
ghostable deploy laravel-cloud production
```

On macOS, Ghostable uses LocalAuthentication and requires Touch ID biometric
verification. On Linux, Ghostable uses the local PAM-backed `sudo`
confirmation, which can use fingerprint verification when the machine's PAM
stack is configured for it. On Windows, Ghostable asks Windows to verify the
current user with Windows Hello or the machine's configured fallback.

Non-interactive local devices cannot satisfy protected access prompts. CI,
deploy scripts, and agents should use a scoped `GHOSTABLE_CI_TOKEN` automation
credential instead. Dry runs that do not write or print decrypted values do not
require confirmation.

Remove local plaintext env files after sensitive work:

```sh
ghostable env clean --dry-run
ghostable env clean
```

`env clean` removes project-root `.env` and `.env.*` files, including
Ghostable backup files. It keeps `.env.example` files by default; pass
`--include-example` only when those templates should also be removed.

## Deploy Scripts

`ghostable deploy production` decrypts the selected environment and writes it to
`.env` unless the project manifest has a provider `deployTarget`, in which case
the configured provider is used by default. It replaces `.env` by default so
stale deploy values do not survive between runs. Use `--merge` when an existing
file should be preserved.

Use the explicit local target when a project has a provider target but a script
needs a local env file:

```sh
ghostable deploy local production
```

Provider targets use `ghostable deploy <target> [environment] [options]`:

```sh
ghostable deploy laravel-forge production
ghostable deploy laravel-vapor production
ghostable deploy laravel-cloud production
```

Short aliases are also accepted:

```sh
ghostable deploy forge production
ghostable deploy vapor production
ghostable deploy cloud production
```

Laravel Vapor deploys selected Ghostable values to Vapor environment variables:

```sh
ghostable deploy laravel-vapor production --dry-run
ghostable deploy laravel-vapor production
```

`ghostable deploy laravel-vapor` requires the `vapor` CLI on `PATH` unless
`--dry-run` is used. Variables are merged into Vapor's temporary
`.env.<environment>` file and pushed with `vapor env:push`.

Laravel Cloud deploys selected Ghostable values to Laravel Cloud environment
variables using the Laravel Cloud CLI:

```sh
ghostable deploy laravel-cloud production --dry-run
ghostable deploy laravel-cloud production --cloud-env production
```

`ghostable deploy laravel-cloud` requires the `cloud` CLI on `PATH` unless
`--dry-run` is used. It calls `cloud environment:variables <environment>` with
`--action=set` for each selected key, so existing Cloud variables with matching
keys are updated and missing keys are added. The Cloud environment defaults to
the Ghostable environment name; use `--cloud-env` when Laravel Cloud uses a
different environment ID or name.

Laravel Forge deploys selected Ghostable values to a Forge site's environment
file using the Laravel Forge CLI:

```sh
ghostable deploy laravel-forge production --dry-run --forge-site example.com
ghostable deploy laravel-forge production --forge-site example.com
```

`ghostable deploy laravel-forge` requires the `forge` CLI on `PATH` unless
`--dry-run` is used. It pulls the remote site environment file with
`forge env:pull`, merges selected Ghostable values into a temporary file, then
pushes the result with `forge env:push`. Existing Forge variables with matching
keys are updated and missing keys are added. Pass `--forge-site` with the Forge
site name that should receive the variables.

Deploy systems can use a scoped automation credential instead of a local device
identity:

```sh
ghostable access create --name deploy-bot --kind deploy --grant production:reader
```

Store the returned token as `GHOSTABLE_CI_TOKEN` in the deploy system, commit
the updated `.ghostable/` files, then run a dry-run before enabling writes:

```sh
ghostable deploy production --dry-run --json
```

When the dry-run is correct, run:

```sh
ghostable deploy production
```

For Laravel Forge, install Ghostable as an npm package in the project, commit
the `.ghostable/` directory, and store the deploy credential outside the
application directory, such as `/home/forge/.ghostable-ci-token` with `0600`
permissions. Then load it in the deploy script after dependencies are installed
but before Laravel commands that read `.env`:

```sh
npm ci
export GHOSTABLE_CI_TOKEN="$(cat "$HOME/.ghostable-ci-token")"
ghostable deploy laravel-forge production --forge-site example.com
npm run build
$FORGE_PHP artisan migrate --force
```

For Laravel Cloud, run `ghostable deploy laravel-cloud` before starting a Cloud
deployment when changed variables should be available to the next deploy.

## Storage

Committed project files:

```text
.ghostable/ghostable.yaml
.ghostable/policy.json
.ghostable/devices/*.json
.ghostable/environments/<env>/keys/*.json
.ghostable/environments/<env>/values/*.json
.ghostable/events/*.json
.ghostable/hygiene.yaml
.ghostable/schema.yaml
.ghostable/schemas/<env>.yaml
```

Key metadata `position` values are sparse sortable ranks, not dense line
numbers. Generated layouts use gaps such as `1000`, `2000`, and `3000` so a
new key can usually be added without rewriting every existing key metadata
file.

Key annotations are signed plaintext key metadata with explicit `string`,
`number`, or `bool` values. They are intended for non-secret labels that future
custom rules or actions may read.

Value records may include a signed plaintext `change.reason` so reviewers can
understand why an encrypted value changed without exposing the secret.

Local-only private identity:

```text
macOS:      Keychain service dev.ghostable.identity.<project-id>
Windows:    Credential Manager target dev.ghostable.identity.<project-id>
Linux/Unix: ${XDG_CONFIG_HOME:-~/.config}/ghostable/identities/<project-id>.json
```

When `GHOSTABLE_KEYSTORE` is set, private identity material is written to:

```text
$GHOSTABLE_KEYSTORE/<project-id>.json
```

That fallback directory is created with `0700` permissions and identity files
are created with `0600` permissions.

## Implementation Notes

Value writes use the Ghostable value schema (`ghostable.value.v1`) and the core
cryptographic model: Ed25519 device signatures, X25519 grants, HKDF-SHA256 key
derivation, and XChaCha20-Poly1305 value encryption.
