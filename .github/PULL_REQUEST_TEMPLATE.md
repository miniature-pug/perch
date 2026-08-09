<!--
Thanks for contributing to perch. Keep the title a Conventional Commit, e.g.
  feat(gui): add per-worktree color
  fix(pty): keep the shell drawer alive on resize
Scope is the subsystem: gui, frontend, agent, pty, hooklistener, registry, config.
-->

## What and why

<!-- What does this change and what problem does it solve? Link any issue with "Closes #123". -->

## Checklist

- [ ] PR title follows [Conventional Commits](https://www.conventionalcommits.org/) (`type(scope): summary`).
- [ ] `make test-all` passes locally (the full gate: test, test-integration, test-front, lint, vet, vulncheck, test-e2e).
- [ ] Updated the docs that this change affects (README, ARCHITECTURE.md, CONTRIBUTING.md, package READMEs, diagrams).
- [ ] No `Co-authored-by` trailers in the commits.
- [ ] New external command call goes through `proc.Runner` and has a `FakeRunner` test (see CONTRIBUTING.md).

## Notes for the reviewer

<!-- Anything that needs a manual smoke (WebKit window, real agent, D-Bus), screenshots, or context that helps review. -->
