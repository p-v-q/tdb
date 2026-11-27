# Contributing to TDB

Thanks for helping out. TDB is a terminal database client written in Go; the
module is `github.com/p-v-q/tdb`, the product name shown in the interface is
TDB.

## Getting started

```bash
git clone https://github.com/p-v-q/tdb
cd tdb
make build        # builds bin/tdb
./bin/tdb -d demo
```

You need a Go toolchain at least as new as the version declared in `go.mod`
(currently **1.27**). No cgo and no external services: the test suite runs
against SQLite and the offline demo by default.

## Before you open a pull request

`make check` and `make race` must both pass:

```bash
make check        # gofmt check + go vet + all tests
make race         # race detector over the stateful packages
make build        # bin/tdb still builds
```

The equivalent raw commands:

```bash
gofmt -l .        # must print nothing (make fmt fixes it in place)
go vet ./...
go test ./...
```

CI runs `make check` and `make race` on every push and pull request. Integration
tests guarded by DSN environment variables are skipped unless you set them, so
a passing local run translates to a passing CI run.

## Code style

- `gofmt` is law: run `make fmt` before committing.
- Follow the surrounding style. Packages carry file-level comments that explain
  *why* the code is shaped the way it is; keep that habit.
- One implementation per user action: menu, shortcut, toolbar and mouse all go
  through `menuDefs` in `ui/menu_defs.go` and `runMenuAction` — do not add a
  second key map (see `docs/architecture.md`).
- `ui` never imports a concrete driver, and a driver never imports `ui`. New
  database support lives in its own `driver/<name>` package plus a blank import
  in `cmd/tdb`.

## Tests

- New behavior ships with `_test.go` coverage in the same package; the `ui`
  tests drive the Bubble Tea model headlessly, so follow the existing tests as
  templates.
- Bug fixes ship with a regression test that fails without the fix — mention it
  in the PR description.
- The pure packages (`sql`, `complete`, `catalog`, `dialect`, `settings`,
  `history`, `textbuf`) are where logic is cheapest to test; keep them covered.

## Localization

Every user-visible string goes through the `i18n` package. Adding a message
means adding it in **three** places:

1. `i18n/keys.go` — the `Key…` constant;
2. `i18n/en.go` — the English string;
3. `i18n/zh.go` — the Simplified Chinese string.

`i18n.Validate()` (run from the test suite) fails when a catalog misses a key or
declares an unknown one, so an incomplete translation breaks `make check`. Do
not hardcode user-visible text in `ui` code.

## Integration tests (optional)

Driver integration tests skip themselves unless the matching environment
variable points at a disposable database. They create and drop their own
objects — never point them at anything you care about:

```bash
TDB_POSTGRES_DSN='postgres://user:pass@localhost:5432/testdb?sslmode=disable' \
  go test ./driver/postgres

TDB_MYSQL_DSN='user:pass@tcp(localhost:3306)/testdb' \
  go test ./driver/mysql
```

## Pull requests

1. Fork and create a topic branch; keep each PR to one change.
2. Update tests and docs for the behavior you touch; user-visible changes also
   belong in `CHANGELOG.md` under **Unreleased**.
3. Make sure `make check` and `make race` pass.
4. Describe the *why* in the PR body and reference any issue it closes.
5. Reviews check correctness, test coverage and that the architecture rules in
   `docs/architecture.md` stay intact.

Security problems do not belong in issues or pull requests — see
[SECURITY.md](SECURITY.md).
