# Security Policy

## Supported versions

TDB 0.9.x is the current supported release line. Security fixes land on `main` and in the next 0.9.x patch/minor release:

| Version | Supported |
| --- | --- |
| `0.9.x` | ✅ |
| `main` (latest commit) | ✅ |
| Older versions, tags, forks and downstream builds | ❌ |

## Reporting a vulnerability

Please **do not** open a public issue or pull request for a security
vulnerability.

Report it privately through GitHub Security Advisories: open the repository's
**Security** tab and click **Report a vulnerability**. The report is delivered
to the maintainers privately and stays hidden until a fix is available.

A useful report includes:

- the affected commit or build (a `tdb -version` line helps);
- what the issue is and what an attacker could gain from it;
- steps to reproduce, including the database kind (SQLite / PostgreSQL /
  MySQL / MariaDB) and a *shape* of the configuration involved 閳?never paste
  real credentials, hostnames or DSNs with passwords.

You should receive an acknowledgment within a reasonable time. Fixes are
published as an advisory, and credit is given with your consent.

## Credential storage 閳?current facts

TDB is a local client; be aware of how it handles what you give it:

- By default connection settings, **including passwords in clear text**, live in
  `settings.json` (the README lists the path per platform). The file is written
  atomically with mode `0600`. On Unix `0600` means only your account can read
  it; on Windows Go maps the mode onto the file's read-only flag, and access is
  governed by the ACL the file inherits from `%AppData%` (your user profile).
  Either way it is **not encrypted at rest**.
- You can make it encrypted: **Settings 閳?Connection passphrase閳?* (or accept
  the first-run offer) seals the whole saved-connection list behind a master
  passphrase with PBKDF2-HMAC-SHA256 (600,000 iterations) and AES-256-GCM. While
  a vault exists, `settings.json`, its `.bak` copy and `-print-settings` carry no
  connection fields, and a locked session cannot read or use them. The
  passphrase is never stored and there is no recovery: keep a backup of the
  connections before enabling it. Details: [`docs/secrets-encryption.md`](docs/secrets-encryption.md).
- Prefer connections whose password comes from an environment variable
  (`passwordEnv`) when you do not want the secret in the file at all.
- Moving credentials into the operating system's keychain is still on the
  roadmap; it would complement the vault by removing the unlock prompt.
- The read-only mode and the execution confirmation dialogs are accident
  guardrails, not security boundaries: anything running as your user can edit
  `settings.json` and bypass them.

## Scope notes

- TDB runs the SQL you write with the credentials you supply; it does not
  sandbox statements. Treat a compromised TDB session as a compromised database
  session.
- Vulnerabilities in third-party dependencies (see `go.mod`) are in scope too;
  include the module and version.
