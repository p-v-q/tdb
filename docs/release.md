# Release checklist

TDB ships one static binary per platform plus the source. The release workflow
(`.github/workflows/release.yml`) builds the archives when a version tag is
pushed; this checklist is what has to be true before tagging.

## 1. Code

- [ ] `make check` is clean (gofmt, `go vet`, all unit and end-to-end tests).
- [ ] `make race` is clean (stateful packages under the race detector).
- [ ] CI is green on `main` (check + race + PostgreSQL/MySQL integration jobs).
- [ ] The full integration suite passes against real databases:
      `.itest/run-all.ps1` (98 scenarios; see `docs/integration-testing.md`).
      It writes `.artifacts/itest-summary.md`.
- [ ] No new TODOs marked as blockers; the findings of the last review round
      are either fixed or listed in
      [`pre-release-review.md`](pre-release-review.md) and
      [`product-gaps.md`](product-gaps.md).
- [ ] The pre-release review fixes are merged and listed in `CHANGELOG.md`.

## 2. Version and changelog

- [ ] `cmd/tdb/main.go` `const version` matches the tag (v0.9.2 → `0.9.2`).
- [ ] `CHANGELOG.md`: the `[Unreleased]` section became `## [0.9.2] - <date>`,
      a new empty `[Unreleased]` section sits above it, and the comparison links
      at the bottom point at the new tag.
- [ ] `tdb -version` prints the new version after a rebuild.
- [ ] `README.md` matches the code: flags, key bindings, supported databases,
      configuration sample, screenshots and the demo GIF.

## 3. Assets

- [ ] `docs/demo.gif` is regenerated from the release binary:
      `cd .itest && go run ./cmd/demo -root .. -out ../.artifacts/demo -python python`
      then copy `demo.gif` to `docs/`.
- [ ] Screenshots in `docs/img/` are current (no removed menu entries or
      renamed dialogs).
- [ ] The README renders correctly on GitHub (tables, images, relative links).
- [ ] The container image is published for `linux/amd64` and `linux/arm64`:
      `docker run --rm ghcr.io/p-v-q/tdb:<version> -version` prints the tag.

## 4. Build and tag

- [ ] `make build` succeeds and `bin/tdb` runs (`tdb -drivers`).
- [ ] Cross-compilation works for every matrix entry the workflow builds:
      `linux/amd64`, `linux/arm64`, `darwin/amd64`, `darwin/arm64`,
      `windows/amd64`, `windows/arm64`.
- [ ] Tag and push:
      ```bash
      git tag -a v0.9.2 -m "TDB v0.9.2"
      git push origin main
      git push origin v0.9.2
      ```
      The tag push starts the release workflow: a `check` job verifies the tag
      matches `cmd/tdb/main.go` and runs gofmt/vet/unit tests, then the build
      matrix produces the archives, `SHA256SUMS` and the GitHub release notes.

## 5. After publishing

- [ ] Download one archive per OS, unpack, run `tdb -version` and the demo
      session.
- [ ] Pull `ghcr.io/p-v-q/tdb:<version>` and run `tdb -version`.
- [ ] Check the release page: notes, assets, checksums, no draft left behind.
- [ ] `go install`/build instructions in the README still work from the tag.
- [ ] Post the release link where users are (no automatic announcement).

## Versioning

The version is a plain constant (no ldflags). Minor releases may change the
settings format in a backwards-compatible way; the `version` field in the JSON
guards that. The connection vault carries its own parameters, so raising the
KDF/cipher parameters later stays compatible with old files.
