# Encrypting the saved connections

Date: 2026-10-23

A database client accumulates exactly the material an attacker wants: hosts,
users, passwords, DSNs. Today `settings.json` keeps them in clear text, mode
0600. That is fine against another user on the machine and nothing more — a
stolen laptop, a backup, a synced config directory or a copied dotfile hands
over every password at once.

This document describes the at-rest encryption TDB adds for that material, and
the threat model it does and does not answer.

## What is protected

The unit of protection is **the whole saved-connection list**, not only the
password field. A host, a user and a database name are the "critical
configuration information" users care about: knowing `prod-db-07` exists is
already useful to an attacker. So the encrypted payload is the JSON array of
`settings.Connection` — names, drivers, hosts, ports, users, databases, schemas,
DSNs, passwords and `passwordEnv` names alike.

Everything else (theme, locale, layout, read-only flag) stays in clear text:
it is needed to draw the UI before the user has unlocked anything, and it
reveals nothing about the databases.

While the vault is locked the settings file contains no connection data at all —
only the KDF parameters, the hint and the ciphertext.

## Threat model

Protected against:

- someone who copies `settings.json` (backup, sync, another account, a stolen
  disk) without knowing the passphrase;
- casual inspection of the file, `-print-settings`, the `.bak` copy, or a
  config directory listing.

Not protected against:

- an attacker who can run code as the user while TDB is running (the key is in
  memory, by construction);
- keyloggers, screen capture, or shoulder surfing;
- a weak passphrase (we warn, we cannot fix it);
- the query history file (`history.json`), which can contain literals from
  executed statements. That is a separate, later concern.

## Format

`settings.json` gains one optional object:

```json
{
  "vault": {
    "version": 1,
    "kdf":    { "algo": "pbkdf2-sha256", "iterations": 600000, "salt": "…" },
    "cipher": { "algo": "aes-256-gcm", "nonce": "…" },
    "hint":   "the one I used at work",
    "data":   "…"
  }
}
```

- **KDF**: PBKDF2-HMAC-SHA256, 600 000 iterations (OWASP's current
  recommendation for this construction), 16-byte random salt, 32-byte key.
  Implemented with the standard library (`crypto/pbkdf2`) — no new dependency.
- **Cipher**: AES-256-GCM, 12-byte random nonce per seal, associated data
  `"tdb-vault-v1"`. The nonce is regenerated on every save, so
  re-encrypting the same connections never reuses a nonce.
- **Versioned**: `version` and both algorithm names are stored, so a future
  release can raise the iteration count or change the cipher and still open old
  files. On unlock with outdated parameters the vault is re-sealed with the
  current ones.
- **Detection**: a wrong passphrase is indistinguishable from a damaged file by
  design (GCM authentication fails); the message says "wrong passphrase or
  damaged file" rather than pretending to know which.

The in-memory key lives only as long as the vault is unlocked and is dropped on
lock. The passphrase itself is never stored, never written to the file, never
put into a status message, and never logged.

## The hint

The hint is stored in clear text next to the ciphertext, because its whole
purpose is to help someone who does not have the passphrase. It is therefore
**not** a secret, and the UI says so while it is being typed: use a reminder
("the one from the office password manager"), never the passphrase and never a
part of it.

## Lifecycle

| Situation | Behaviour |
| --- | --- |
| No vault yet, plaintext connections | Unchanged. The UI offers to encrypt them (*Settings → Connection passphrase…*). |
| User sets a passphrase | The current connection list is sealed immediately and saved; the plaintext list disappears from the file. |
| Existing plaintext file, user enables encryption | Same path — "encrypt what is already there" is not a special case, it is the normal seal. |
| Start with a vault | The unlock dialog opens before anything can be connected. `Esc` continues **locked**: preferences work, no connection can be opened. |
| Vault unlocked, settings change (theme, layout) | Connections are re-sealed with a fresh nonce on save. The KDF runs once, at unlock; saves stay cheap. |
| Change passphrase | New salt, fresh KDF run, re-seal, save. |
| Remove encryption | Requires the passphrase; connections return to the file in clear text (the user asked for it), with a warning. |
| Save while locked | The existing vault object is written back untouched; no connection edits can happen while locked. |
| Corrupt file | A parse failure still copies the file to `<path>.bak` before overwriting it. Once the vault is on, a `.bak` left over from the plaintext era is deleted on the next save: it would otherwise sit next to the sealed file with every password in clear text. |
| Envelope with absurd KDF parameters | Iterations below the floor or above ten times the current cost are treated as a damaged envelope: PBKDF2 refuses to burn hours on a number an attacker rewrote. |

## What the UI must not do

- Print, echo or paste the passphrase anywhere, including status messages and
  error text.
- Let a locked session save or edit connections.
- Allow a hint that is the passphrase (warn, do not silently accept).
- Pretend a weak passphrase is fine: shorter than 8 characters is refused,
  shorter than 12 is accepted with a warning.

## There is no recovery

There is no back door, no recovery code and no support key: the passphrase is
the only thing that can decrypt the file, and losing it loses the connection
list. The setup dialog says so while the passphrase is being chosen, and the
hint is the only help the user gets later — which is why the hint may not be
the passphrase and why it is stored in clear text. Keeping a copy of the
connections elsewhere (or exporting them before enabling encryption) is the
user's escape hatch; the client cannot manufacture one.

## Non-goals

- The system keychain (macOS Keychain, Secret Service, Windows Credential
  Manager). It complements this feature — it removes the passphrase prompt — and
  remains on the roadmap. The format above is deliberately independent of it.
- Encrypting the whole settings file. Preferences are not secret, and keeping
  them readable is what allows the unlock dialog to be drawn in the user's
  language and theme.
