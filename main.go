package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	baseURL  = "https://api.pokemontcg.io/v2"
	cacheTTL = 10 * time.Minute
)

// --- cache -----------------------------------------------------------------

type cacheEntry struct {
	data      []byte
	expiresAt time.Time
}

type cache struct {
	mu    sync.Mutex
	items map[string]cacheEntry
}

func newCache() *cache {
	return &cache{items: make(map[string]cacheEntry)}
}

func (c *cache) get(key string) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	entry, ok := c.items[key]
	if !ok || time.Now().After(entry.expiresAt) {
		return nil, false
	}
	return entry.data, true
}

func (c *cache) set(key string, data []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.items[key] = cacheEntry{data: data, expiresAt: time.Now().Add(cacheTTL)}
}

// --- HTTP client -------------------------------------------------------------

var (
	httpClient = &http.Client{Timeout: 15 * time.Second}
	apiCache   = newCache()
)

// fetchJSON performs a GET against the Pokemon TCG API, serving from the
// shared cache when the request path+query was fetched within cacheTTL.
func fetchJSON(ctx context.Context, path string, query url.Values) ([]byte, error) {
	fullURL := baseURL + path
	if len(query) > 0 {
		fullURL += "?" + query.Encode()
	}

	if data, ok := apiCache.get(fullURL); ok {
		return data, nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fullURL, nil)
	if err != nil {
		return nil, err
	}
	if apiKey := os.Getenv("POKEMONTCG_API_KEY"); apiKey != "" {
		req.Header.Set("X-Api-Key", apiKey)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("pokemontcg.io request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading pokemontcg.io response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("pokemontcg.io returned %s for %s: %s", resp.Status, fullURL, string(body))
	}

	apiCache.set(fullURL, body)
	return body, nil
}

// --- API response shapes (subset of fields we care about) ------------------

type apiSet struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Series      string `json:"series"`
	ReleaseDate string `json:"releaseDate"`
}

type apiCardImages struct {
	Small string `json:"small"`
	Large string `json:"large"`
}

type apiTCGPlayerPrices struct {
	Normal          *apiPriceBlock `json:"normal,omitempty"`
	Holofoil        *apiPriceBlock `json:"holofoil,omitempty"`
	Reverse         *apiPriceBlock `json:"reverseHolofoil,omitempty"`
	FirstEdHolofoil *apiPriceBlock `json:"1stEditionHolofoil,omitempty"`
}

type apiPriceBlock struct {
	Market float64 `json:"market"`
}

type apiTCGPlayer struct {
	URL    string             `json:"url"`
	Prices apiTCGPlayerPrices `json:"prices"`
}

type apiCard struct {
	ID        string        `json:"id"`
	Name      string        `json:"name"`
	Set       apiSet        `json:"set"`
	Number    string        `json:"number"`
	Rarity    string        `json:"rarity"`
	Artist    string        `json:"artist"`
	Images    apiCardImages `json:"images"`
	TCGPlayer *apiTCGPlayer `json:"tcgplayer,omitempty"`
}

func (p apiTCGPlayerPrices) marketPrice() (float64, bool) {
	for _, block := range []*apiPriceBlock{p.Normal, p.Holofoil, p.Reverse, p.FirstEdHolofoil} {
		if block != nil && block.Market > 0 {
			return block.Market, true
		}
	}
	return 0, false
}

// --- search_cards ------------------------------------------------------------

type SearchCardsInput struct {
	Name string `json:"name" jsonschema:"card name to search for"`
	Set  string `json:"set,omitempty" jsonschema:"optional set name to filter by"`
	Page int    `json:"page,omitempty" jsonschema:"page number, defaults to 1"`
}

type CardSummary struct {
	ID       string `json:"id" jsonschema:"card id, e.g. base1-4"`
	Name     string `json:"name" jsonschema:"card name"`
	Set      string `json:"set" jsonschema:"set name"`
	Number   string `json:"number" jsonschema:"card number within its set"`
	Rarity   string `json:"rarity" jsonschema:"card rarity"`
	SmallImg string `json:"smallImage" jsonschema:"small card image URL"`
}

type SearchCardsOutput struct {
	Cards []CardSummary `json:"cards" jsonschema:"matching cards"`
}

func searchCards(ctx context.Context, req *mcp.CallToolRequest, in SearchCardsInput) (*mcp.CallToolResult, SearchCardsOutput, error) {
	q := fmt.Sprintf("name:%q", in.Name)
	if in.Set != "" {
		q += fmt.Sprintf(" set.name:%q", in.Set)
	}

	query := url.Values{}
	query.Set("q", q)
	page := in.Page
	if page < 1 {
		page = 1
	}
	query.Set("page", fmt.Sprintf("%d", page))

	body, err := fetchJSON(ctx, "/cards", query)
	if err != nil {
		return nil, SearchCardsOutput{}, err
	}

	var parsed struct {
		Data []apiCard `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, SearchCardsOutput{}, fmt.Errorf("parsing pokemontcg.io response: %w", err)
	}

	out := SearchCardsOutput{Cards: make([]CardSummary, 0, len(parsed.Data))}
	for _, c := range parsed.Data {
		out.Cards = append(out.Cards, CardSummary{
			ID:       c.ID,
			Name:     c.Name,
			Set:      c.Set.Name,
			Number:   c.Number,
			Rarity:   c.Rarity,
			SmallImg: c.Images.Small,
		})
	}
	return nil, out, nil
}

// --- get_card ------------------------------------------------------------

type GetCardInput struct {
	ID string `json:"id" jsonschema:"card id, e.g. base1-4"`
}

type GetCardOutput struct {
	Name        string  `json:"name" jsonschema:"card name"`
	Set         string  `json:"set" jsonschema:"set name"`
	Rarity      string  `json:"rarity" jsonschema:"card rarity"`
	Artist      string  `json:"artist" jsonschema:"illustrator name"`
	LargeImg    string  `json:"largeImage" jsonschema:"large card image URL"`
	MarketPrice float64 `json:"marketPrice,omitempty" jsonschema:"TCGPlayer market price in USD, if available"`
}

func getCard(ctx context.Context, req *mcp.CallToolRequest, in GetCardInput) (*mcp.CallToolResult, GetCardOutput, error) {
	body, err := fetchJSON(ctx, "/cards/"+url.PathEscape(in.ID), nil)
	if err != nil {
		return nil, GetCardOutput{}, err
	}

	var parsed struct {
		Data apiCard `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, GetCardOutput{}, fmt.Errorf("parsing pokemontcg.io response: %w", err)
	}

	out := GetCardOutput{
		Name:     parsed.Data.Name,
		Set:      parsed.Data.Set.Name,
		Rarity:   parsed.Data.Rarity,
		Artist:   parsed.Data.Artist,
		LargeImg: parsed.Data.Images.Large,
	}
	if parsed.Data.TCGPlayer != nil {
		if market, ok := parsed.Data.TCGPlayer.Prices.marketPrice(); ok {
			out.MarketPrice = market
		}
	}
	return nil, out, nil
}

// --- list_sets ------------------------------------------------------------

type ListSetsInput struct {
	Series string `json:"series,omitempty" jsonschema:"optional series name to filter by"`
}

type SetSummary struct {
	ID          string `json:"id" jsonschema:"set id"`
	Name        string `json:"name" jsonschema:"set name"`
	Series      string `json:"series" jsonschema:"series the set belongs to"`
	ReleaseDate string `json:"releaseDate" jsonschema:"set release date"`
}

type ListSetsOutput struct {
	Sets []SetSummary `json:"sets" jsonschema:"sets, most recent first"`
}

func listSets(ctx context.Context, req *mcp.CallToolRequest, in ListSetsInput) (*mcp.CallToolResult, ListSetsOutput, error) {
	query := url.Values{}
	query.Set("orderBy", "-releaseDate")
	if in.Series != "" {
		query.Set("q", fmt.Sprintf("series:%q", in.Series))
	}

	body, err := fetchJSON(ctx, "/sets", query)
	if err != nil {
		return nil, ListSetsOutput{}, err
	}

	var parsed struct {
		Data []apiSet `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, ListSetsOutput{}, fmt.Errorf("parsing pokemontcg.io response: %w", err)
	}

	out := ListSetsOutput{Sets: make([]SetSummary, 0, len(parsed.Data))}
	for _, s := range parsed.Data {
		out.Sets = append(out.Sets, SetSummary{
			ID:          s.ID,
			Name:        s.Name,
			Series:      s.Series,
			ReleaseDate: s.ReleaseDate,
		})
	}
	return nil, out, nil
}

// --- main ------------------------------------------------------------------

func main() {
	server := mcp.NewServer(&mcp.Implementation{Name: "pokemon-tcg", Version: "v1.0.0"}, nil)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "search_cards",
		Description: "Search Pokemon TCG cards by name, optionally filtered by set name.",
	}, searchCards)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_card",
		Description: "Get full detail for one Pokemon TCG card by id.",
	}, getCard)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "list_sets",
		Description: "List Pokemon TCG sets, most recent first, optionally filtered by series.",
	}, listSets)

	if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		log.Fatal(err)
	}
}
