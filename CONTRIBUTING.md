# Contributing

Thanks for helping improve `agent-runtime`.

## Before You Start

1. Read `README.md`.
2. Read `docs/README.md`.
3. Check open issues and existing PRs before starting a large change.

## Development Setup

1. Clone and enter repo:

```bash
git clone <repo-url> agent-runtime
cd agent-runtime
```

2. Prepare env:

```bash
cp .env.example .env
```

3. Run locally:

```bash
make run
```

4. Run tests:

```bash
make test
```

## Typical Workflow

1. Create a branch.
2. Make focused changes.
3. Add or update tests/docs with the code change.
4. Run local validation.
5. Open a PR with clear scope and rationale.

## Validation Checklist

Run these before opening a PR:

```bash
make test
make docs-check
```

If your change affects runtime behavior, also smoke-test with one connector flow
(Telegram, Discord, or Codex CLI).

## Pull Request Guidelines

- Keep PRs focused and reviewable.
- Include migration notes for env vars, APIs, or storage changes.
- Update docs for any user-visible behavior changes.
- Prefer additive, backward-compatible changes when possible.

## Coding Expectations

- Keep package boundaries clean; avoid circular dependencies.
- Preserve existing style and naming conventions.
- Avoid unrelated refactors in the same PR.
- Add tests for bug fixes and new behavior.

## Documentation Expectations

When adding/changing behavior, update the relevant docs:

- Product entrypoint: `README.md`
- User/operator docs: `docs/`
- API behavior: `docs/api.md`

## Reporting Bugs and Feature Requests

Use GitHub Issues with:

- clear reproduction steps
- expected vs actual behavior
- runtime environment details
- relevant logs or payload examples

For security issues, do not open a public issue. Follow
`SECURITY.md`.
