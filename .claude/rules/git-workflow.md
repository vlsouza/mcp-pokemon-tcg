# Git workflow

- Commit small and often: one commit per logical unit of work (e.g. "add
  cache layer", "add search_cards tool"), not one commit per work session.
- Commit messages follow Conventional Commits (`feat:`, `fix:`, `chore:`,
  `docs:`, `refactor:`), matching the direct/technical tone of the rest of
  this file.
- Use the `/commit` command (from the `commit-commands` plugin, enabled for
  this project) as the default way to commit instead of raw `git commit`, so
  messages stay consistent. `/commit-push-pr` is available for larger
  features that warrant a PR, even on a single-maintainer repo.
- Never commit secrets — this repeats the existing rule about
  `POKEMONTCG_API_KEY` above, now as a commit-time rule too.
- Single `main` branch is fine for this project's scope; no PR requirement
  for solo work.
