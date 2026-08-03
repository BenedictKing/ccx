package autopilot

import (
	"math"
	"testing"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
)

func TestResolveExchangeTerms(t *testing.T) {
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	graph, err := config.NewExchangeRateGraph([]config.ExchangeRateQuote{
		{SourceAmount: 1, SourceUnit: "USD", TargetAmount: 7, TargetUnit: "CNY", UpdatedAt: now},
		{SourceAmount: 500, SourceUnit: "LDC", TargetAmount: 10, TargetUnit: "CNY", UpdatedAt: now},
	}, 11, now)
	if err != nil {
		t.Fatalf("NewExchangeRateGraph() error = %v", err)
	}

	result := ResolveExchangeTerms(EffectiveCostInput{
		Graph:       graph,
		PaymentUnit: "CNY",
		CreditUnit:  "LDC",
	})
	if !result.OK {
		t.Fatalf("expected OK result, got %+v", result)
	}
	if result.Version != 11 {
		t.Fatalf("expected version 11, got %d", result.Version)
	}
	if !nearlyEqual(result.PaymentUSDPrice, 1.0/7.0) {
		t.Fatalf("unexpected payment USD price: %+v", result)
	}
	if !nearlyEqual(result.CreditUSDPrice, 1.0/350.0) {
		t.Fatalf("unexpected credit USD price: %+v", result)
	}
	if !nearlyEqual(result.ExchangeFactor, 50.0) {
		t.Fatalf("expected exchange factor 50, got %+v", result)
	}
}

func TestResolveExchangeTermsFailsWithoutGraph(t *testing.T) {
	result := ResolveExchangeTerms(EffectiveCostInput{PaymentUnit: "USD", CreditUnit: "CNY"})
	if result.OK {
		t.Fatalf("expected missing graph to fail, got %+v", result)
	}
}

func TestResolveEffectiveCostUSD(t *testing.T) {
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	graph, err := config.NewExchangeRateGraph([]config.ExchangeRateQuote{
		{SourceAmount: 1, SourceUnit: "USD", TargetAmount: 7, TargetUnit: "CNY", UpdatedAt: now},
		{SourceAmount: 500, SourceUnit: "LDC", TargetAmount: 10, TargetUnit: "CNY", UpdatedAt: now},
	}, 12, now)
	if err != nil {
		t.Fatal(err)
	}
	result := ResolveEffectiveCostUSD(EffectiveCostInput{
		Graph: graph, ListCostAmount: 1, ListCostUnit: "LDC",
		GroupMultiplier: 0.8, TimeMultiplier: 1,
		PaymentAmount: 500, PaymentUnit: "LDC", CreditAmount: 10, CreditUnit: "USD",
	})
	if !result.Available {
		t.Fatalf("expected available, got %+v", result)
	}
	// 1 LDC list cost = 1/350 USD; 500 LDC 购买 10 USD，G=0.8，只应用一次。
	if !nearlyEqual(result.EffectiveCostUSD, (1.0/350.0)*0.8*(500.0/350.0)/10.0) {
		t.Fatalf("unexpected effective cost: %+v", result)
	}
}

func TestResolveEffectiveCostUSDUnavailableUnit(t *testing.T) {
	graph, err := config.NewExchangeRateGraph([]config.ExchangeRateQuote{{SourceAmount: 1, SourceUnit: "USD", TargetAmount: 7, TargetUnit: "CNY"}}, 1, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	result := ResolveEffectiveCostUSD(EffectiveCostInput{Graph: graph, ListCostAmount: 1, ListCostUnit: "USD", GroupMultiplier: 1, TimeMultiplier: 1, PaymentAmount: 1, PaymentUnit: "MISSING", CreditAmount: 1, CreditUnit: "USD"})
	if result.Available || result.Reason == "" {
		t.Fatalf("expected explicit unavailable result, got %+v", result)
	}
}

func nearlyEqual(a, b float64) bool {
	return math.Abs(a-b) <= 1e-9*math.Max(1, math.Max(math.Abs(a), math.Abs(b)))
}
