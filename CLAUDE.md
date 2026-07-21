# pokemon-tcg-mcp

## What this is

A simple, clean MCP server in Go that exposes the Pokemon TCG API
(pokemontcg.io) as tools for any MCP client. Built as a portfolio/LinkedIn
piece to show MCP + Go skills — not tied to the resale business, no
overengineering.

Similar projects already exist (Python and TypeScript versions), so the
differentiators here are simply: it's Go, and it has a proper in-memory
cache instead of hitting the API on every call.

## Tech stack

- Go (1.23+)
- Official MCP SDK: `github.com/modelcontextprotocol/go-sdk`
- Data source: pokemontcg.io REST API (`https://api.pokemontcg.io/v2`) —
  no key needed for light usage (1000 req/day, 30/min); add one later via
  `POKEMONTCG_API_KEY` env var if needed (20,000 req/day with a key)
- stdio transport only — this runs as a local process launched by the MCP
  client (Claude Desktop, Claude Code), same as every other local MCP
  server. No hosting, no deployment.

## Tools

1. **`search_cards(name, set?, page?)`**
   Search cards by name, optionally filtered by set name. Returns a
   trimmed list: id, name, set, number, rarity, small image URL.

2. **`get_card(id)`**
   Full detail for one card by id (e.g. `base1-4`): name, set, rarity,
   artist, large image URL, and TCGPlayer market price if available.

3. **`list_sets(series?)`**
   List sets, most recent first, optionally filtered by series name.

4. **`get_set(id)`**
   Full detail for one set by id (e.g. `base1`): name, series, release
   date, printed/total card counts, PTCGO code, logo image.

5. **`get_card_prices(id)`**
   Full raw price breakdown for one card: TCGPlayer (USD, by finish —
   normal/holofoil/reverse holo/1st edition holo) and Cardmarket (EUR).
   No graded (PSA/BGS) prices — the API doesn't have that data.

6. **`get_card_legality(id)`**
   Standard/Expanded/Unlimited tournament legality for one card.

7. **`list_types()`**
   All Pokemon TCG energy types (Fire, Water, etc).

8. **`list_subtypes()`**
   All card subtypes (Stage 1, VMAX, EX, etc).

9. **`list_supertypes()`**
   Pokemon, Trainer, Energy.

10. **`list_rarities()`**
    All card rarities (Common, Rare Holo, etc).

Ten tools, all backed by endpoints/fields the pokemontcg.io API already
exposes — no new data sources, no derived/invented functionality.

## The one thing that makes this not a toy wrapper: caching

Every tool call that hits pokemontcg.io goes through a single shared
in-memory cache with a TTL (10 minutes is fine — this data doesn't change
fast). Same query within the TTL window returns the cached result instead
of a new HTTP call.

```go
type cacheEntry struct {
    data      []byte
    expiresAt time.Time
}

type cache struct {
    mu    sync.Mutex
    items map[string]cacheEntry
}
```

Key the cache on the full request path + query string. This is a small
amount of code but it's the difference between "wrapper that calls an API"
and "wrapper that respects the API's rate limit and its own client's
UX" — worth calling out explicitly in the README and the LinkedIn post.

## Conventions

- Single `main.go` is fine for this scope — don't split into packages
  prematurely.
- All HTTP calls go through one shared client + cache helper, never a raw
  `http.Get` scattered in a handler.
- Tool input/output types are plain structs with `json` + `jsonschema`
  struct tags, using the SDK's generic `mcp.AddTool[In, Out]` — let it
  infer the schema, don't hand-write JSON schemas.
- Return plain Go errors from handlers; the SDK's generic `AddTool`
  converts them to a proper tool error automatically.
- No secrets committed. If an API key is added, read it from
  `POKEMONTCG_API_KEY`, never hardcode it.

## Commands

```bash
go mod tidy
go run .                              # run on stdio
go build -o pokemon-tcg-mcp .         # binary for Claude Desktop config
go vet ./...
```

## Claude Desktop config (for the README / demo)

```json
{
  "mcpServers": {
    "pokemon-tcg": {
      "command": "/absolute/path/to/pokemon-tcg-mcp"
    }
  }
}
```

## Out of scope for v1

- No deploy, no HTTP transport, no persistence, no auth.
- No price comparison across markets, no deck validation — those are
  separate, bigger projects if this one gets legs.

## Git workflow

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
