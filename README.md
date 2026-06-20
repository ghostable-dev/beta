# Ghostable CLI

Ghostable is a server-less CLI for local-first environment management:
projects, environments, variables, devices, schema validation, agent guidance,
and signed-style activity records.

This client does not call Ghostable servers. Encrypted value files, public
device records, signed policy, signed activity, environment keys, and access
grants live under `.ghostable/` and are intended to be committed to git. Private
device keys are stored outside the repository in the platform's native secret
store when available, or in a restrictive file-backed identity store otherwise.

## Build

```sh
make build
```

## Basic Flow

```sh
ghostable setup --name "My App"
ghostable env push --env default --file .env --reason "Initial default baseline"
ghostable env pull --env default --file .env
ghostable env diff --env default --file .env
ghostable env validate --env default
ghostable deploy production
ghostable scan
```

Most options can be omitted in an interactive terminal. Ghostable prompts for
project and device details when needed, and creates the `default` environment
automatically. If `.env` exists, setup asks whether to seed `default` from it.
Automation and agents should pass flags and prefer `--json`.

## Commands

- `setup` initializes `.ghostable/`, a local device record, policy, layout files,
  and a private local device identity.
- `status` prints project, environment, device, and value counts.
- `env list|create|delete|push|sync|copy|pull|diff|validate|history`
  manages environment-level workflows.
- `deploy [environment]` writes decrypted values to `.env` for deploy scripts.
- `env duplicate|rename|layout generate|file save` supports desktop and agent
  workflows.
- `var push|pull|delete|history|context` manages a single variable.
- `schema file|rule|key` manages local validation schema files.
- `device create|join|list|status|share|grants|revoke` manages local device
  records, scoped automation devices, and policy grants.
- `agent init|instructions|capabilities` emits safe instructions for coding
  agents.
- `scan` finds hard-coded secrets locally without modifying files.

## Deploy Scripts

`ghostable deploy production` decrypts the selected environment and writes it to
`.env`. It replaces `.env` by default so stale deploy values do not survive
between runs. Use `--merge` when an existing file should be preserved.

Laravel Vapor deploys can split regular environment variables from values that
should be stored through Vapor Secrets:

```sh
ghostable var vapor-secret --env production --key APP_KEY --enabled
ghostable deploy vapor production --dry-run
ghostable deploy vapor production
```

`ghostable deploy vapor` requires the `vapor` CLI on `PATH` unless `--dry-run`
is used. Regular variables are merged into Vapor's `.env.<environment>` file and
pushed with `vapor env:push`; variables marked as Vapor Secrets are synced with
Vapor Secrets instead.

Deploy systems can use a scoped automation credential instead of a local device
identity:

```sh
ghostable agent credential create --name deploy-bot --kind deploy --grant production:reader
```

Store the returned token as `GHOSTABLE_CI_TOKEN` in the deploy system, commit
the updated `.ghostable/` files, then run:

```sh
ghostable deploy production
```

## Secret Scanning

`ghostable scan` checks the current project for likely hard-coded secrets. It
uses manifest ignores from:

```yaml
scan:
  ignores:
    - .git/**
    - node_modules/**
    - .ghostable/environments/**/values/**
```

Findings are redacted by default. Use `--json` for machine-readable output.
`--show-values` exists for explicit human debugging, but agents should avoid it
unless the user asks.

## Storage

Committed project files:

```text
.ghostable/ghostable.yaml
.ghostable/policy.json
.ghostable/devices/*.json
.ghostable/environments/<env>/layout.json
.ghostable/environments/<env>/values/*.json
.ghostable/events/*.json
.ghostable/schema.yaml
.ghostable/schemas/<env>.yaml
```

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

## Security Notes

- Device identity uses Ed25519 for signatures and X25519 for key exchange.
- Value encryption uses XChaCha20-Poly1305 with 24-byte random nonces.
- Environment values use a per-environment key, then derive separate encryption
  and HMAC keys with HKDF-SHA256 scoped to `ghostable/<project>/<environment>`.
- Environment keys are wrapped by a random DEK; that DEK is shared through
  per-device X25519 + HKDF-SHA256 + XChaCha encrypted grants.
- Device records, policy records, access grants, access envelopes, activity
  events, and value payloads are signed with Ed25519.
- Secret values are never printed unless `--show-values` is provided.
- `var push` without `--file` uses a no-echo terminal prompt on Unix systems.
- `.ghostable/environments/**/values/**` is ignored by the scanner to avoid
  inspecting encrypted payloads as source text.

## Implementation Notes

This client intentionally does not include hosted Ghostable API behavior. Value
writes use the Ghostable value schema (`ghostable.value.v1`) and the same core
cryptographic model: Ed25519 device signatures, X25519 grants, HKDF-SHA256 key
derivation, and XChaCha20-Poly1305 value encryption.
