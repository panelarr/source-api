# Contributing

Thanks for helping. This page covers the development setup and how changes get in.

## Development setup

You need Go (the version in `go.mod`; the `go` command downloads it if yours is older) and `make`.

```sh
make setup   # installs pinned tools into ./bin
make check   # format check, vet, lint, breaking-change check, govulncheck and tests: run this before every PR
make help    # lists every target
```

### Changing the protos

The protos are a public contract that plugins and hosts build against, so changes must stay backwards compatible:

- Add fields and RPCs; never remove, renumber or retype them. `make breaking` compares your branch with `main` and fails on a breaking change.
- Run `make gen` and commit `gen/` together with the proto change.
- Document new fields in the proto: comments become the API docs.

### No third-party sources

Never put real extension, store or site names in code, tests, fixtures or docs. Tests use made-up sources served from local test servers.

## Working conventions

- **Tasks:** everything runs through `make`.
- **Branches:** `<type>/<scope>-<slug>`, e.g. `feat/sdk-conformance`.
- **Commits and PR titles:** [Conventional Commits](https://www.conventionalcommits.org/): `feat`, `fix`, `docs`, `refactor`, `test`, `chore`, `perf`, `build`, `ci`. Add `!` for breaking changes. PRs are squash-merged, so the PR title becomes the commit message.
- **Reviews:** at least one approval and green checks before merge.
- **AI agents:** agents may open PRs and post reviews. Anything an agent posts from a person's account starts with a **⚠️ AI agent** line. Humans stay responsible for what they merge.
