# Security Policy

Perch runs coding agents against your repositories, executes the tool calls they
propose, and opens a small local listener while a session is live. A surface like
that deserves a clear, private way to report a flaw. This document sets one out.

## Supported versions

Perch has not yet cut a tagged release. Until it does, the actively developed
line on the default branch (`main`) is the only supported version, and security
fixes land there. Older checkouts are not maintained. Update to the current
`main` before you report an issue, so a fix has somewhere to land.

## Reporting a vulnerability

Report privately through GitHub. Never open a public issue or pull request for a
suspected vulnerability.

Open the repository's **Security** tab and choose **Report a vulnerability**, or
go straight to:

https://github.com/miniature-pug/perch/security/advisories/new

Reporting this way opens a private advisory visible only to you and the
maintainers. Tell us
what you need to reproduce the issue: the affected version or commit, the agent
and configuration in play, the steps you took, and what you saw. A proof of
concept helps, though a clear description is enough to begin.

Please give the maintainers a reasonable window to ship a fix before you disclose
publicly.

## What to expect

- **Acknowledgement.** The maintainers acknowledge a report within a few days of
  receiving it.
- **Assessment.** They confirm the issue, weigh its severity, and tell you
  whether it falls in scope.
- **A fix.** The maintainers fix a confirmed vulnerability on `main` as a
  priority. Its severity sets the timeline, and they keep you informed as the
  work proceeds.
- **Credit.** With your consent, the maintainers credit you in the published
  advisory.

## Threat surface in scope

Perch's security model appears in the section of that name in
[ARCHITECTURE.md](ARCHITECTURE.md). Reports touching these areas are especially
welcome.

- **The agent hook listener.** Each live Claude session opens a loopback listener
  on `127.0.0.1` at an ephemeral port, guarded by a random per-listener bearer
  token. Anything that lets an unauthorized caller reach it, defeat the token, or
  slip a tool call past the approval block belongs here.
- **Always-allow rules.** Choosing "Always" on an approval stores a rule keyed on
  the agent, the tool, and a SHA-256 hash of the exact tool input. Anything that
  makes a rule auto-approve a call it did not exactly match belongs here.
- **Repository-carried code execution.** A repository can commit agent
  configuration into its tree, such as a `.claude/settings.json`, whose hooks the
  agent runtime executes on session start. perch does not gate those hooks, and
  the documentation says so. A report belongs here if it shows perch running
  committed repository code in a way the documentation does not describe.
- **Input validation.** The backend validates session and pane identifiers,
  worktree paths, and git refs. A crafted value that slips past those checks
  belongs here.

## Out of scope

- Vulnerabilities in the coding agents themselves (`claude`, `opencode`) or in
  their upstream dependencies. Report those to their own projects.
- The behavior of hooks carried by a repository you have chosen to trust.
  Opening an untrusted repository is a documented caveat, covered in the
  README's Security section, not a perch vulnerability.
- Anything that already requires a local attacker holding your user account or an
  interactive session on your machine.
