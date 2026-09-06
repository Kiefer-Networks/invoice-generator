package invoicing

import (
	"math"
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
