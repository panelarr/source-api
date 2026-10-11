# Agent instructions

<!-- Privacy mode: this file is public. It must not name or describe any private design repository. -->

## Before you work

1. Your task brief was given to you by your operator, including where to read its full context. Read the brief and the documents it lists before changing code.
2. Read this repo's own docs: `README.md`, `docs/`, `CONTRIBUTING.md`.
3. Treat the decisions referenced in your brief as requirements. If the code can't satisfy one, stop and report the conflict to your operator. Don't diverge silently.

## Rules for public artifacts

Everything in this repo is public: code, comments, docs, commit messages, branch names, PR and issue text.

- Explain the *why* in your own words. Don't paste text from private notes, and don't use internal document identifiers or paths.
- If design reasoning is worth keeping publicly, write it as a doc in `docs/` (or `docs/adr/`) of this repo.

## Confidential terms

Your operator gives you a list of aliases for confidential names (a local file, never committed). Write **only the alias** in code, comments, docs, commits, branches and PR text. If unsure whether a name is confidential, ask.

## Working here

- Everything runs through `make` (`make help`). Before reporting, run `make check` and `make test`.
- Branch per slice: `<type>/<scope>-<slug>`. Conventional Commits.
- Commit / merge / push rights come from your brief. If unsure: commit on your branch only, never push.
- After each slice, report to your operator: what you built, what you assumed, how to test it.
- Text you post on the forge from a human's account starts with: `> ⚠️ **AI agent:** written by an AI agent, posted from <owner>'s account.`

## This repo

- **Progress:** [PROGRESS.md](PROGRESS.md) lists the planned slices for this repo and source-mihon, what's done and what's next. Re-read it whenever you lose context.
- **The protos are a public contract.** Every change must be backwards compatible: `make breaking` (in `make check`) compares with `main`. Removing or renumbering fields, or changing types, needs a new package version (`v2`).
- **Generated code is committed:** run `make gen` after changing a proto, and commit `gen/` with it. CI fails when it's stale.
- **No third-party sources.** No real extension, store or site names anywhere, including tests. Tests use made-up sources served from local test servers.
- **Never log** API keys, proxy credentials or cookies.
