package config

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"
)

const exchangeRateTolerance = 1e-9

type exchangeEdge struct {
	to    string
	price float64
}

// ExchangeRateGraph 保存以 USD 计价的单位价格图，并支持原子替换。
type ExchangeRateGraph struct {
	mu            sync.RWMutex
	version       uint64
	builtAt       time.Time
	usdUnitPrices map[string]float64
	quotes        []ExchangeRateQuote
}

func NewExchangeRateGraph(quotes []ExchangeRateQuote, version uint64, now time.Time) (*ExchangeRateGraph, error) {
	prices, normalizedQuotes, err := buildExchangeRateGraph(quotes)
	if err != nil {
		return nil, err
	}
	return &ExchangeRateGraph{
		version:       version,
		builtAt:       now.UTC(),
		usdUnitPrices: prices,
		quotes:        normalizedQuotes,
	}, nil
}

func buildExchangeRateGraph(quotes []ExchangeRateQuote) (map[string]float64, []ExchangeRateQuote, error) {
	adj := map[string][]exchangeEdge{}
	normalized := make([]ExchangeRateQuote, 0, len(quotes))
	for i, quote := range quotes {
		sourceUnit := normalizeExchangeUnit(quote.SourceUnit)
		targetUnit := normalizeExchangeUnit(quote.TargetUnit)
		if sourceUnit == "" || targetUnit == "" {
			return nil, nil, fmt.Errorf("exchange rate quote %d has empty unit", i)
		}
		if !isFinitePositive(quote.SourceAmount) || !isFinitePositive(quote.TargetAmount) {
			return nil, nil, fmt.Errorf("exchange rate quote %d has invalid amount", i)
		}
		if sourceUnit == targetUnit {
			if !nearlyEqual(quote.SourceAmount, quote.TargetAmount) {
				return nil, nil, fmt.Errorf("exchange rate quote %d is self-contradictory for %s", i, sourceUnit)
			}
			normalized = append(normalized, ExchangeRateQuote{
				SourceAmount: quote.SourceAmount,
				SourceUnit:   sourceUnit,
				TargetAmount: quote.TargetAmount,
				TargetUnit:   targetUnit,
				UpdatedAt:    quote.UpdatedAt,
				Note:         quote.Note,
			})
			continue
		}
		forward := quote.TargetAmount / quote.SourceAmount
		backward := quote.SourceAmount / quote.TargetAmount
		if err := addExchangeEdge(adj, sourceUnit, targetUnit, forward); err != nil {
			return nil, nil, err
		}
		if err := addExchangeEdge(adj, targetUnit, sourceUnit, backward); err != nil {
			return nil, nil, err
		}
		normalized = append(normalized, ExchangeRateQuote{
			SourceAmount: quote.SourceAmount,
			SourceUnit:   sourceUnit,
			TargetAmount: quote.TargetAmount,
			TargetUnit:   targetUnit,
			UpdatedAt:    quote.UpdatedAt,
			Note:         quote.Note,
		})
	}

	prices := map[string]float64{"USD": 1}
	queue := []string{"USD"}
	for len(queue) > 0 {
		unit := queue[0]
		queue = queue[1:]
		for _, edge := range adj[unit] {
			candidate := prices[unit] / edge.price
			if existing, ok := prices[edge.to]; ok {
				if !nearlyEqual(existing, candidate) {
					return nil, nil, fmt.Errorf("exchange rate graph has conflicting path for %s", edge.to)
				}
				continue
			}
			prices[edge.to] = candidate
			queue = append(queue, edge.to)
		}
	}

	if err := validateCycles(adj, prices); err != nil {
		return nil, nil, err
	}
	return prices, normalized, nil
}

func addExchangeEdge(adj map[string][]exchangeEdge, from, to string, price float64) error {
	for _, edge := range adj[from] {
		if edge.to != to {
			continue
		}
		if !nearlyEqual(edge.price, price) {
			return fmt.Errorf("exchange rate graph has conflicting edge %s -> %s", from, to)
		}
		return nil
	}
	adj[from] = append(adj[from], exchangeEdge{to: to, price: price})
	return nil
}

func validateCycles(adj map[string][]exchangeEdge, prices map[string]float64) error {
	for from, edges := range adj {
		fromPrice, fromOK := prices[from]
		for _, edge := range edges {
			toPrice, toOK := prices[edge.to]
			if !fromOK || !toOK {
				continue
			}
			expected := fromPrice / edge.price
			if !nearlyEqual(expected, toPrice) {
				return fmt.Errorf("exchange rate graph has conflicting cycle %s -> %s", from, edge.to)
			}
		}
	}
	return nil
}

func (g *ExchangeRateGraph) ResolveUSDPrice(unit string) (price float64, ok bool, version uint64) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	price, ok = g.usdUnitPrices[normalizeExchangeUnit(unit)]
	return price, ok, g.version
}

func (g *ExchangeRateGraph) Snapshot() ExchangeRateSnapshot {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return ExchangeRateSnapshot{
		Version:       g.version,
		USDUnitPrices: cloneUSDUnitPrices(g.usdUnitPrices),
		BuiltAt:       g.builtAt,
	}
}

func (g *ExchangeRateGraph) ReplaceQuotes(quotes []ExchangeRateQuote, now time.Time) error {
	prices, normalizedQuotes, err := buildExchangeRateGraph(quotes)
	if err != nil {
		return err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.version++
	g.builtAt = now.UTC()
	g.usdUnitPrices = prices
	g.quotes = normalizedQuotes
	return nil
}

func cloneUSDUnitPrices(prices map[string]float64) map[string]float64 {
	if prices == nil {
		return nil
	}
	copied := make(map[string]float64, len(prices))
	keys := make([]string, 0, len(prices))
	for unit := range prices {
		keys = append(keys, unit)
	}
	sort.Strings(keys)
	for _, unit := range keys {
		copied[unit] = prices[unit]
	}
	return copied
}

func normalizeExchangeUnit(unit string) string {
	return strings.ToUpper(strings.TrimSpace(unit))
}

func isFinitePositive(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value > 0
}

func nearlyEqual(a, b float64) bool {
	delta := math.Abs(a - b)
	scale := math.Max(1, math.Max(math.Abs(a), math.Abs(b)))
	return delta <= exchangeRateTolerance*scale
}
