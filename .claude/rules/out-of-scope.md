# Out of scope for v1

- No deploy, no HTTP transport, no persistence, no auth.
- No price comparison across markets, no deck validation — those are
  separate, bigger projects if this one gets legs.
- No migration to Scrydex (the paid successor to pokemontcg.io) — the
  free v2 API still works and is the right fit for this project's scope.
  Revisit only if its intermittent 500s (see README) get bad enough that
  caching + retry stop being enough; it needs a paid API key and an
  unverified schema change, not a `baseURL` swap.
