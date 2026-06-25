# Security Policy

Ghostable is built around a local-first, serverless, device-scoped
cryptographic architecture for protecting environment data. Secret values are
encrypted locally, and Ghostable is designed so plaintext environment values and
private device identity material stay under your control.

In this project, "zero-knowledge" means Ghostable is designed so secret values
are encrypted locally before they are written to repository-backed Ghostable
files. Encrypted value files, public device records, signed policy records,
signed activity, environment keys, and access grants live under `.ghostable/`
and are intended to be committed to git. Private device keys are stored outside
the repository in the platform's native secret store when available, or in a
restrictive file-backed identity store otherwise.

## Cryptographic Model

- Device identity uses Ed25519 for signatures and X25519 for key exchange.
- Value encryption uses XChaCha20-Poly1305 with random 24-byte nonces.
- Environment values use a per-environment key, then derive separate encryption
  and HMAC keys with HKDF-SHA256 scoped to the project and environment.
- Environment keys are wrapped by a random data encryption key, then shared to
  authorized devices through per-device encrypted grants.
- Device records, policy records, access grants, access envelopes, activity
  events, key metadata, and value payloads are signed with Ed25519.

## Operational Expectations

Ghostable's security model depends on protecting local device identity material
and keeping repository metadata honest.

- Do not commit plaintext `.env` files or private device identity files.
- Do not put secrets in key annotations, schema descriptions, comments, or other
  metadata that is not encrypted.
- Revoke access for lost, retired, or compromised devices.
- Review policy, device, and access grant changes with the same care as code
  changes.
- Keep Ghostable updated so you receive security fixes and cryptographic model
  improvements.

## Reporting a Vulnerability

If you believe you have identified a security vulnerability, please do not open
a public GitHub issue. Email the details to security@ghostable.dev instead.

Please include as much useful context as you can safely share:

- The affected Ghostable version, operating system, and installation method.
- A clear description of the issue and its potential impact.
- Reproduction steps, proof-of-concept code, or sample files when available.
- Any relevant logs or command output, with secrets and personal data removed.

We will acknowledge your report within 24 hours and work with you to
investigate and address the vulnerability as quickly as possible.

## Disclosure Guidelines

When researching or reporting a vulnerability, please:

- Test only against projects, repositories, devices, and accounts you own or are
  authorized to assess.
- Avoid accessing, modifying, deleting, or exfiltrating data that does not belong
  to you.
- Avoid social engineering, spam, denial-of-service testing, and destructive
  testing.
- Give us a reasonable opportunity to investigate and release a fix before
  public disclosure.

Thank you for helping keep Ghostable secure for all users.
