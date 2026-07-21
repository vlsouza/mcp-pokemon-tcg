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

	maxAttempts = 3
	retryWait   = 500 * time.Millisecond
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
// pokemontcg.io is known to return intermittent, transient 5xx errors, so
// requests are retried a few times with a short backoff before giving up.
func fetchJSON(ctx context.Context, path string, query url.Values) ([]byte, error) {
	fullURL := baseURL + path
	if len(query) > 0 {
		fullURL += "?" + query.Encode()
	}

	if data, ok := apiCache.get(fullURL); ok {
		return data, nil
	}

	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		body, retryable, err := doFetch(ctx, fullURL)
		if err == nil {
			apiCache.set(fullURL, body)
			return body, nil
		}

		lastErr = err
		if !retryable || attempt == maxAttempts {
			break
		}

		select {
		case <-time.After(retryWait * time.Duration(attempt)):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return nil, lastErr
}

// doFetch performs a single HTTP attempt. retryable is true for errors worth
// retrying (network failures, 5xx) and false for errors that won't improve
// on retry (4xx, response body issues).
func doFetch(ctx context.Context, fullURL string) (body []byte, retryable bool, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fullURL, nil)
	if err != nil {
		return nil, false, err
	}
	if apiKey := os.Getenv("POKEMONTCG_API_KEY"); apiKey != "" {
		req.Header.Set("X-Api-Key", apiKey)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, true, fmt.Errorf("pokemontcg.io request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, true, fmt.Errorf("reading pokemontcg.io response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, resp.StatusCode >= 500, fmt.Errorf("pokemontcg.io returned %s for %s: %s", resp.Status, fullURL, string(respBody))
	}

	return respBody, false, nil
}

// --- API response shapes (subset of fields we care about) ------------------

type apiLegalities struct {
	Unlimited string `json:"unlimited,omitempty"`
	Standard  string `json:"standard,omitempty"`
	Expanded  string `json:"expanded,omitempty"`
}

type apiSet struct {
	ID           string        `json:"id"`
	Name         string        `json:"name"`
	Series       string        `json:"series"`
	ReleaseDate  string        `json:"releaseDate"`
	PrintedTotal int           `json:"printedTotal"`
	Total        int           `json:"total"`
	PTCGOCode    string        `json:"ptcgoCode"`
	Legalities   apiLegalities `json:"legalities"`
	Images       apiSetImages  `json:"images"`
}

type apiSetImages struct {
	Symbol string `json:"symbol"`
	Logo   string `json:"logo"`
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
	Low    float64 `json:"low"`
	Mid    float64 `json:"mid"`
	High   float64 `json:"high"`
	Market float64 `json:"market"`
}

type apiTCGPlayer struct {
	URL    string             `json:"url"`
	Prices apiTCGPlayerPrices `json:"prices"`
}

type apiCardMarketPrices struct {
	AverageSellPrice float64 `json:"averageSellPrice"`
	LowPrice         float64 `json:"lowPrice"`
	TrendPrice       float64 `json:"trendPrice"`
	SuggestedPrice   float64 `json:"suggestedPrice"`
}

type apiCardMarket struct {
	URL    string              `json:"url"`
	Prices apiCardMarketPrices `json:"prices"`
}

type apiCard struct {
	ID         string         `json:"id"`
	Name       string         `json:"name"`
	Set        apiSet         `json:"set"`
	Number     string         `json:"number"`
	Rarity     string         `json:"rarity"`
	Artist     string         `json:"artist"`
	Images     apiCardImages  `json:"images"`
	Legalities apiLegalities  `json:"legalities"`
	TCGPlayer  *apiTCGPlayer  `json:"tcgplayer,omitempty"`
	CardMarket *apiCardMarket `json:"cardmarket,omitempty"`
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

// --- get_set ------------------------------------------------------------

type GetSetInput struct {
	ID string `json:"id" jsonschema:"set id, e.g. base1"`
}

type GetSetOutput struct {
	ID           string `json:"id" jsonschema:"set id"`
	Name         string `json:"name" jsonschema:"set name"`
	Series       string `json:"series" jsonschema:"series the set belongs to"`
	ReleaseDate  string `json:"releaseDate" jsonschema:"set release date"`
	PrintedTotal int    `json:"printedTotal" jsonschema:"number of cards printed in the set, excluding secret rares"`
	Total        int    `json:"total" jsonschema:"total number of cards in the set, including secret rares"`
	PTCGOCode    string `json:"ptcgoCode,omitempty" jsonschema:"code used for this set in the Pokemon TCG Online client"`
	LogoImage    string `json:"logoImage" jsonschema:"set logo image URL"`
}

func getSet(ctx context.Context, req *mcp.CallToolRequest, in GetSetInput) (*mcp.CallToolResult, GetSetOutput, error) {
	body, err := fetchJSON(ctx, "/sets/"+url.PathEscape(in.ID), nil)
	if err != nil {
		return nil, GetSetOutput{}, err
	}

	var parsed struct {
		Data apiSet `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, GetSetOutput{}, fmt.Errorf("parsing pokemontcg.io response: %w", err)
	}

	return nil, GetSetOutput{
		ID:           parsed.Data.ID,
		Name:         parsed.Data.Name,
		Series:       parsed.Data.Series,
		ReleaseDate:  parsed.Data.ReleaseDate,
		PrintedTotal: parsed.Data.PrintedTotal,
		Total:        parsed.Data.Total,
		PTCGOCode:    parsed.Data.PTCGOCode,
		LogoImage:    parsed.Data.Images.Logo,
	}, nil
}

// --- get_card_prices ------------------------------------------------------------

type GetCardPricesInput struct {
	ID string `json:"id" jsonschema:"card id, e.g. base1-4"`
}

type PriceRange struct {
	Low    float64 `json:"low,omitempty"`
	Mid    float64 `json:"mid,omitempty"`
	High   float64 `json:"high,omitempty"`
	Market float64 `json:"market,omitempty"`
}

type TCGPlayerPrices struct {
	URL             string      `json:"url,omitempty"`
	Normal          *PriceRange `json:"normal,omitempty"`
	Holofoil        *PriceRange `json:"holofoil,omitempty"`
	ReverseHolofoil *PriceRange `json:"reverseHolofoil,omitempty"`
	FirstEdHolofoil *PriceRange `json:"firstEditionHolofoil,omitempty"`
}

type CardMarketPrices struct {
	URL              string  `json:"url,omitempty"`
	AverageSellPrice float64 `json:"averageSellPriceEUR,omitempty"`
	LowPrice         float64 `json:"lowPriceEUR,omitempty"`
	TrendPrice       float64 `json:"trendPriceEUR,omitempty"`
	SuggestedPrice   float64 `json:"suggestedPriceEUR,omitempty"`
}

type GetCardPricesOutput struct {
	TCGPlayer  *TCGPlayerPrices  `json:"tcgplayer,omitempty" jsonschema:"USD prices by finish, from TCGPlayer, if available"`
	CardMarket *CardMarketPrices `json:"cardmarket,omitempty" jsonschema:"EUR prices, from Cardmarket, if available"`
}

func getCardPrices(ctx context.Context, req *mcp.CallToolRequest, in GetCardPricesInput) (*mcp.CallToolResult, GetCardPricesOutput, error) {
	body, err := fetchJSON(ctx, "/cards/"+url.PathEscape(in.ID), nil)
	if err != nil {
		return nil, GetCardPricesOutput{}, err
	}

	var parsed struct {
		Data apiCard `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, GetCardPricesOutput{}, fmt.Errorf("parsing pokemontcg.io response: %w", err)
	}

	out := GetCardPricesOutput{}

	if tp := parsed.Data.TCGPlayer; tp != nil {
		toRange := func(b *apiPriceBlock) *PriceRange {
			if b == nil {
				return nil
			}
			return &PriceRange{Low: b.Low, Mid: b.Mid, High: b.High, Market: b.Market}
		}
		out.TCGPlayer = &TCGPlayerPrices{
			URL:             tp.URL,
			Normal:          toRange(tp.Prices.Normal),
			Holofoil:        toRange(tp.Prices.Holofoil),
			ReverseHolofoil: toRange(tp.Prices.Reverse),
			FirstEdHolofoil: toRange(tp.Prices.FirstEdHolofoil),
		}
	}

	if cm := parsed.Data.CardMarket; cm != nil {
		out.CardMarket = &CardMarketPrices{
			URL:              cm.URL,
			AverageSellPrice: cm.Prices.AverageSellPrice,
			LowPrice:         cm.Prices.LowPrice,
			TrendPrice:       cm.Prices.TrendPrice,
			SuggestedPrice:   cm.Prices.SuggestedPrice,
		}
	}

	return nil, out, nil
}

// --- get_card_legality ------------------------------------------------------------

type GetCardLegalityInput struct {
	ID string `json:"id" jsonschema:"card id, e.g. base1-4"`
}

type GetCardLegalityOutput struct {
	Unlimited string `json:"unlimited,omitempty" jsonschema:"Legal or Banned in the Unlimited format, if applicable"`
	Standard  string `json:"standard,omitempty" jsonschema:"Legal or Banned in the Standard format, if applicable"`
	Expanded  string `json:"expanded,omitempty" jsonschema:"Legal or Banned in the Expanded format, if applicable"`
}

func getCardLegality(ctx context.Context, req *mcp.CallToolRequest, in GetCardLegalityInput) (*mcp.CallToolResult, GetCardLegalityOutput, error) {
	body, err := fetchJSON(ctx, "/cards/"+url.PathEscape(in.ID), nil)
	if err != nil {
		return nil, GetCardLegalityOutput{}, err
	}

	var parsed struct {
		Data apiCard `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, GetCardLegalityOutput{}, fmt.Errorf("parsing pokemontcg.io response: %w", err)
	}

	return nil, GetCardLegalityOutput{
		Unlimited: parsed.Data.Legalities.Unlimited,
		Standard:  parsed.Data.Legalities.Standard,
		Expanded:  parsed.Data.Legalities.Expanded,
	}, nil
}

// --- list_types / list_subtypes / list_supertypes / list_rarities ---------

type ListStringsOutput struct {
	Values []string `json:"values" jsonschema:"the list of values"`
}

func fetchStringList(ctx context.Context, path string) (ListStringsOutput, error) {
	body, err := fetchJSON(ctx, path, nil)
	if err != nil {
		return ListStringsOutput{}, err
	}

	var parsed struct {
		Data []string `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return ListStringsOutput{}, fmt.Errorf("parsing pokemontcg.io response: %w", err)
	}

	return ListStringsOutput{Values: parsed.Data}, nil
}

type NoInput struct{}

func listTypes(ctx context.Context, req *mcp.CallToolRequest, in NoInput) (*mcp.CallToolResult, ListStringsOutput, error) {
	out, err := fetchStringList(ctx, "/types")
	return nil, out, err
}

func listSubtypes(ctx context.Context, req *mcp.CallToolRequest, in NoInput) (*mcp.CallToolResult, ListStringsOutput, error) {
	out, err := fetchStringList(ctx, "/subtypes")
	return nil, out, err
}

func listSupertypes(ctx context.Context, req *mcp.CallToolRequest, in NoInput) (*mcp.CallToolResult, ListStringsOutput, error) {
	out, err := fetchStringList(ctx, "/supertypes")
	return nil, out, err
}

func listRarities(ctx context.Context, req *mcp.CallToolRequest, in NoInput) (*mcp.CallToolResult, ListStringsOutput, error) {
	out, err := fetchStringList(ctx, "/rarities")
	return nil, out, err
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

	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_set",
		Description: "Get full detail for one Pokemon TCG set by id, including card counts and legalities.",
	}, getSet)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_card_prices",
		Description: "Get the full price breakdown for one card: TCGPlayer (USD, by finish) and Cardmarket (EUR).",
	}, getCardPrices)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_card_legality",
		Description: "Get Standard/Expanded/Unlimited tournament legality for one card.",
	}, getCardLegality)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "list_types",
		Description: "List all Pokemon TCG energy types (Fire, Water, etc).",
	}, listTypes)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "list_subtypes",
		Description: "List all Pokemon TCG card subtypes (Stage 1, VMAX, EX, etc).",
	}, listSubtypes)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "list_supertypes",
		Description: "List all Pokemon TCG supertypes (Pokemon, Trainer, Energy).",
	}, listSupertypes)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "list_rarities",
		Description: "List all Pokemon TCG card rarities (Common, Rare Holo, etc).",
	}, listRarities)

	if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		log.Fatal(err)
	}
}
