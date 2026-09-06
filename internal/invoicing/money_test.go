package invoicing

import (
	"math"
	"strings"
	"testing"
)

func TestCalculateUsesScaledQuantityAndRoundsEachLineHalfUp(t *testing.T) {
	t.Parallel()
	totals, err := Calculate([]Line{{QuantityScaled: 30000, UnitPriceMinor: 3333, TaxRateBasisPoints: 1900}})
	if err != nil {
		t.Fatal(err)
	}
	if totals.NetMinor != 9999 || totals.TaxMinor != 1900 || totals.GrossMinor != 11899 {
		t.Fatalf("totals = %#v", totals)
	}
	if len(totals.TaxGroups) != 1 || totals.TaxGroups[0].TaxRateBasisPoints != 1900 || totals.TaxGroups[0].TaxMinor != 1900 {
		t.Fatalf("tax groups = %#v", totals.TaxGroups)
	}
}

func TestCalculateThreeTimesFractionalQuantityAtNineteenPercent(t *testing.T) {
	t.Parallel()
	// EUR 3.00 x 0.3333 = EUR 0.9999 -> 100 cents net, 19 VAT, 119 gross.
	totals, err := Calculate([]Line{{QuantityScaled: 3333, UnitPriceMinor: 300, TaxRateBasisPoints: 1900}})
	if err != nil || totals.NetMinor != 100 || totals.TaxMinor != 19 || totals.GrossMinor != 119 {
		t.Fatalf("3 x 0.3333: %#v %v", totals, err)
	}
	// Three separately rounded positions must not be rounded as one aggregate.
	totals, err = Calculate([]Line{{QuantityScaled: 3333, UnitPriceMinor: 100, TaxRateBasisPoints: 1900}, {QuantityScaled: 3333, UnitPriceMinor: 100, TaxRateBasisPoints: 1900}, {QuantityScaled: 3333, UnitPriceMinor: 100, TaxRateBasisPoints: 1900}})
	if err != nil || totals.NetMinor != 99 || totals.TaxMinor != 18 || totals.GrossMinor != 117 {
		t.Fatalf("three positions: %#v %v", totals, err)
	}
}

func TestCalculateVATHalfUpAndDiscountBoundaries(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name                                   string
		price, discount, rate, net, tax, gross int64
		invalid                                bool
	}{
		{"VAT below tie", 49, 0, 100, 49, 0, 49, false},
		{"VAT tie", 50, 0, 100, 50, 1, 51, false},
		{"VAT above tie", 51, 0, 100, 51, 1, 52, false},
		{"19 percent tie", 50, 0, 1900, 50, 10, 60, false},
		{"zero discount", 101, 0, 1900, 101, 19, 120, false},
		{"full discount", 101, 10000, 1900, 0, 0, 0, false},
		{"discount below tie", 1, 4999, 0, 1, 0, 1, false},
		{"discount tie", 1, 5000, 1900, 0, 0, 0, false},
		{"discount above tie", 1, 5001, 1900, 0, 0, 0, false},
		{"discount odd cent tie", 101, 5000, 1900, 50, 10, 60, false},
		{"negative discount", 100, -1, 1900, 0, 0, 0, true},
		{"over full discount", 100, 10001, 1900, 0, 0, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Calculate([]Line{{QuantityScaled: 10000, UnitPriceMinor: tc.price, DiscountBasisPoints: tc.discount, TaxRateBasisPoints: tc.rate}})
			if tc.invalid {
				if err == nil {
					t.Fatal("invalid discount accepted")
				}
				return
			}
			if err != nil || got.NetMinor != tc.net || got.TaxMinor != tc.tax || got.GrossMinor != tc.gross {
				t.Fatalf("totals: %#v %v", got, err)
			}
		})
	}
}

func TestCalculateRejectsAggregateOverflowOfIndividuallyValidLines(t *testing.T) {
	t.Parallel()
	line := Line{QuantityScaled: 1, UnitPriceMinor: math.MaxInt64 - 10000}
	one, err := Calculate([]Line{line})
	if err != nil || one.NetMinor != 922337203685477 {
		t.Fatalf("valid line: %#v %v", one, err)
	}
	lines := make([]Line, 10001)
	for i := range lines {
		lines[i] = line
	}
	if _, err = Calculate(lines); err == nil || !strings.Contains(err.Error(), "overflow") {
		t.Fatalf("aggregate overflow: %v", err)
	}
}

func TestCalculateGroupsRatesAndAppliesDiscountBeforeTax(t *testing.T) {
	t.Parallel()
	totals, err := Calculate([]Line{
		{QuantityScaled: 10000, UnitPriceMinor: 1000, DiscountBasisPoints: 1000, TaxRateBasisPoints: 1900},
		{QuantityScaled: 25000, UnitPriceMinor: 400, TaxRateBasisPoints: 0},
	})
	if err != nil {
		t.Fatal(err)
	}
	if totals.NetMinor != 1900 || totals.TaxMinor != 171 || totals.GrossMinor != 2071 {
		t.Fatalf("totals = %#v", totals)
	}
	if len(totals.Lines) != 2 || totals.Lines[0].NetMinor != 900 || totals.Lines[1].NetMinor != 1000 {
		t.Fatalf("lines = %#v", totals.Lines)
	}
	if len(totals.TaxGroups) != 2 || totals.TaxGroups[0].TaxRateBasisPoints != 0 || totals.TaxGroups[1].TaxRateBasisPoints != 1900 {
		t.Fatalf("tax groups = %#v", totals.TaxGroups)
	}
}

func TestCalculateRejectsInvalidValuesAndOverflow(t *testing.T) {
	t.Parallel()
	for _, line := range []Line{
		{QuantityScaled: 0, UnitPriceMinor: 1},
		{QuantityScaled: 1, UnitPriceMinor: -1},
		{QuantityScaled: 1, UnitPriceMinor: 1, DiscountBasisPoints: 10001},
		{QuantityScaled: 1, UnitPriceMinor: 1, TaxRateBasisPoints: 10001},
		{QuantityScaled: math.MaxInt64, UnitPriceMinor: math.MaxInt64},
	} {
		if _, err := Calculate([]Line{line}); err == nil {
			t.Fatalf("Calculate(%#v) accepted invalid input", line)
		}
	}
}

func TestCalculateFormatsFractionalQuantityAndRoundsHalfUpTies(t *testing.T) {
	t.Parallel()
	totals, err := Calculate([]Line{{QuantityScaled: 3333, UnitPriceMinor: 100, TaxRateBasisPoints: 1900}, {QuantityScaled: 50, UnitPriceMinor: 100, DiscountBasisPoints: 5000, TaxRateBasisPoints: 0}})
	if err != nil {
		t.Fatal(err)
	}
	if totals.Lines[0].NetMinor != 33 || totals.Lines[0].TaxMinor != 6 || totals.Lines[1].NetMinor != 0 {
		t.Fatalf("ties=%#v", totals.Lines)
	}
}
