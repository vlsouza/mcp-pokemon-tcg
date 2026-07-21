# pokemon-tcg-mcp

A small MCP server in Go that exposes the [Pokemon TCG API](https://pokemontcg.io)
(`pokemontcg.io`) as tools for any MCP client (Claude Desktop, Claude Code,
etc). Built as a portfolio piece to show MCP + Go — not tied to any
resale business, no overengineering.

## Why this one

Similar wrappers already exist in Python and TypeScript. The differentiators
here: it's Go, and it has a proper **in-memory cache with a TTL** instead of
hitting the API on every single call. Every tool call goes through one shared
cache keyed on the full request URL — a repeated query within the 10-minute
TTL window is served from memory instead of firing a new HTTP request. That's
the difference between "a wrapper that calls an API" and "a wrapper that
respects the API's rate limit and its own client's UX."

## Tools

1. **`search_cards(name, set?, page?)`** — search cards by name, optionally
   filtered by set name. Returns a trimmed list: id, name, set, number,
   rarity, small image URL.
2. **`get_card(id)`** — full detail for one card by id (e.g. `base1-4`):
   name, set, rarity, artist, large image URL, and TCGPlayer market price
   if available.
3. **`list_sets(series?)`** — list sets, most recent first, optionally
   filtered by series name.

## Running it

```bash
go mod tidy
go run .                              # runs on stdio
go build -o pokemon-tcg-mcp .         # binary for Claude Desktop config
go vet ./...
```

No API key is required for light usage (1000 requests/day, 30/min). To raise
that limit, set `POKEMONTCG_API_KEY` in the environment — never commit it.

## Claude Desktop config

```json
{
  "mcpServers": {
    "pokemon-tcg": {
      "command": "/absolute/path/to/pokemon-tcg-mcp"
    }
  }
}
```
