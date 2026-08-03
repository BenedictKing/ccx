package config

import (
	"testing"
	"time"
)

func TestExchangeRateGraphResolveUSDPrice(t *testing.T) {
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	graph, err := NewExchangeRateGraph(defaultExchangeRateQuotes(), 7, now)
	if err != nil {
		t.Fatalf("NewExchangeRateGraph() error = %v", err)
	}

	usd, ok, version := graph.ResolveUSDPrice("USD")
	if !ok || version != 7 || usd != 1 {
		t.Fatalf("ResolveUSDPrice(USD) = %v, %v, %d", usd, ok, version)
	}
	cny, ok, _ := graph.ResolveUSDPrice("CNY")
	if !ok || !nearlyEqual(cny, 1.0/7.0) {
		t.Fatalf("ResolveUSDPrice(CNY) = %v, %v", cny, ok)
	}
	ldc, ok, _ := graph.ResolveUSDPrice("LDC")
	if !ok || !nearlyEqual(ldc, 10.0/500.0*cny) {
		t.Fatalf("ResolveUSDPrice(LDC) = %v, %v", ldc, ok)
	}
}

func TestExchangeRateGraphRejectsInvalidQuotes(t *testing.T) {
	now := time.Now().UTC()
	tests := []struct {
		name   string
		quotes []ExchangeRateQuote
	}{
		{name: "zero source", quotes: []ExchangeRateQuote{{SourceAmount: 0, SourceUnit: "USD", TargetAmount: 7, TargetUnit: "CNY"}}},
		{name: "empty unit", quotes: []ExchangeRateQuote{{SourceAmount: 1, SourceUnit: "", TargetAmount: 7, TargetUnit: "CNY"}}},
		{name: "self conflict", quotes: []ExchangeRateQuote{{SourceAmount: 1, SourceUnit: "USD", TargetAmount: 2, TargetUnit: "USD"}}},
		{name: "edge conflict", quotes: []ExchangeRateQuote{{SourceAmount: 1, SourceUnit: "USD", TargetAmount: 7, TargetUnit: "CNY"}, {SourceAmount: 1, SourceUnit: "USD", TargetAmount: 8, TargetUnit: "CNY"}}},
		{name: "cycle conflict", quotes: []ExchangeRateQuote{{SourceAmount: 1, SourceUnit: "USD", TargetAmount: 7, TargetUnit: "CNY"}, {SourceAmount: 7, SourceUnit: "CNY", TargetAmount: 500, TargetUnit: "LDC"}, {SourceAmount: 1, SourceUnit: "USD", TargetAmount: 60, TargetUnit: "LDC"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := NewExchangeRateGraph(tt.quotes, 1, now); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestExchangeRateGraphAllowsConsistentRedundantPath(t *testing.T) {
	now := time.Now().UTC()
	quotes := []ExchangeRateQuote{
		{SourceAmount: 1, SourceUnit: "USD", TargetAmount: 7, TargetUnit: "CNY"},
		{SourceAmount: 500, SourceUnit: "LDC", TargetAmount: 10, TargetUnit: "CNY"},
		{SourceAmount: 1, SourceUnit: "USD", TargetAmount: 350, TargetUnit: "LDC"},
	}
	if _, err := NewExchangeRateGraph(quotes, 1, now); err != nil {
		t.Fatalf("expected redundant consistent path to pass, got %v", err)
	}
}

func TestExchangeRateGraphSnapshotIsDefensiveCopy(t *testing.T) {
	graph, err := NewExchangeRateGraph(defaultExchangeRateQuotes(), 3, time.Now().UTC())
	if err != nil {
		t.Fatalf("NewExchangeRateGraph() error = %v", err)
	}
	snapshot := graph.Snapshot()
	snapshot.USDUnitPrices["CNY"] = 99
	again := graph.Snapshot()
	if nearlyEqual(again.USDUnitPrices["CNY"], 99) {
		t.Fatal("snapshot mutation leaked into graph")
	}
}

func TestExchangeRateGraphReplaceQuotesKeepsOldSnapshotOnFailure(t *testing.T) {
	graph, err := NewExchangeRateGraph(defaultExchangeRateQuotes(), 9, time.Now().UTC())
	if err != nil {
		t.Fatalf("NewExchangeRateGraph() error = %v", err)
	}
	before := graph.Snapshot()
	err = graph.ReplaceQuotes([]ExchangeRateQuote{{SourceAmount: 1, SourceUnit: "USD", TargetAmount: 0, TargetUnit: "CNY"}}, time.Now().UTC())
	if err == nil {
		t.Fatal("expected ReplaceQuotes to fail")
	}
	after := graph.Snapshot()
	if before.Version != after.Version || !nearlyEqual(before.USDUnitPrices["CNY"], after.USDUnitPrices["CNY"]) {
		t.Fatalf("failed replace must not mutate snapshot: before=%+v after=%+v", before, after)
	}
}
