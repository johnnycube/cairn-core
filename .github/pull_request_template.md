## What and why

## Checklist

- [ ] `make check` is green locally (lint, tests, coverage, audit, build, fleetlint)
- [ ] New behaviour has tests; `CHANGELOG.md` has an entry under `[Unreleased]`
- [ ] Schema changes are a new migration (never an edit to an existing one)
- [ ] Proto changes: `make proto` run and `gen/proto` committed
- [ ] Commits are signed off (`git commit -s`)
