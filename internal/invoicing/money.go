// Package invoicing contains the business rules for editable invoices.
package invoicing

import (
	"errors"
	"fmt"
	"math"
	"sort"
)

const quantityScale int64 = 10000

// Line contains money in currency minor units and rates in basis points.
// QuantityScaled is expressed in ten-thousandths of the selected unit.
type Line struct {
	QuantityScaled, UnitPriceMinor, DiscountBasisPoints, TaxRateBasisPoints int64
}

type LineTotal struct{ NetMinor, TaxMinor, GrossMinor int64 }
type TaxGroup struct {
	TaxRateBasisPoints, NetMinor, TaxMinor, GrossMinor int64
}
type Totals struct {
	Lines                          []LineTotal
	TaxGroups                      []TaxGroup
	NetMinor, TaxMinor, GrossMinor int64
}

// Calculate derives all line and document totals with integer arithmetic.
func Calculate(lines []Line) (Totals, error) {
	result := Totals{Lines: make([]LineTotal, len(lines))}
	groups := map[int64]*TaxGroup{}
	for i, line := range lines {
		if line.QuantityScaled <= 0 || line.UnitPriceMinor < 0 || line.DiscountBasisPoints < 0 || line.DiscountBasisPoints > 10000 || line.TaxRateBasisPoints < 0 || line.TaxRateBasisPoints > 10000 {
			return Totals{}, fmt.Errorf("line %d: %w", i+1, errors.New("invalid money values"))
		}
		product, err := mul(line.QuantityScaled, line.UnitPriceMinor)
		if err != nil {
			return Totals{}, fmt.Errorf("line %d net: %w", i+1, err)
		}
		base, err := roundedDiv(product, quantityScale)
		if err != nil {
			return Totals{}, fmt.Errorf("line %d net: %w", i+1, err)
		}
		product, err = mul(base, line.DiscountBasisPoints)
		if err != nil {
			return Totals{}, fmt.Errorf("line %d discount: %w", i+1, err)
		}
		discount, err := roundedDiv(product, 10000)
		if err != nil {
			return Totals{}, fmt.Errorf("line %d discount: %w", i+1, err)
		}
		net := base - discount
		product, err = mul(net, line.TaxRateBasisPoints)
		if err != nil {
			return Totals{}, fmt.Errorf("line %d tax: %w", i+1, err)
		}
		tax, err := roundedDiv(product, 10000)
		if err != nil {
			return Totals{}, fmt.Errorf("line %d tax: %w", i+1, err)
		}
		gross, err := add(net, tax)
		if err != nil {
			return Totals{}, fmt.Errorf("line %d gross: %w", i+1, err)
		}
		result.Lines[i] = LineTotal{NetMinor: net, TaxMinor: tax, GrossMinor: gross}
		if result.NetMinor, err = add(result.NetMinor, net); err != nil {
			return Totals{}, err
		}
		if result.TaxMinor, err = add(result.TaxMinor, tax); err != nil {
			return Totals{}, err
		}
		if result.GrossMinor, err = add(result.GrossMinor, gross); err != nil {
			return Totals{}, err
		}
		group := groups[line.TaxRateBasisPoints]
		if group == nil {
			group = &TaxGroup{TaxRateBasisPoints: line.TaxRateBasisPoints}
			groups[line.TaxRateBasisPoints] = group
		}
		if group.NetMinor, err = add(group.NetMinor, net); err != nil {
			return Totals{}, err
		}
		if group.TaxMinor, err = add(group.TaxMinor, tax); err != nil {
			return Totals{}, err
		}
		if group.GrossMinor, err = add(group.GrossMinor, gross); err != nil {
			return Totals{}, err
		}
	}
	for _, group := range groups {
		result.TaxGroups = append(result.TaxGroups, *group)
	}
	sort.Slice(result.TaxGroups, func(i, j int) bool {
		return result.TaxGroups[i].TaxRateBasisPoints < result.TaxGroups[j].TaxRateBasisPoints
	})
	return result, nil
}

func mul(a, b int64) (int64, error) {
	if a == 0 || b == 0 {
		return 0, nil
	}
	if a != 0 && (a > math.MaxInt64/b || a < math.MinInt64/b) {
		return 0, errors.New("integer overflow")
	}
	return a * b, nil
}
func add(a, b int64) (int64, error) {
	if (b > 0 && a > math.MaxInt64-b) || (b < 0 && a < math.MinInt64-b) {
		return 0, errors.New("integer overflow")
	}
	return a + b, nil
}
func roundedDiv(value, divisor int64) (int64, error) {
	if divisor <= 0 || value < 0 {
		return 0, errors.New("invalid division")
	}
	if value > math.MaxInt64-divisor/2 {
		return 0, errors.New("integer overflow")
	}
	return (value + divisor/2) / divisor, nil
}
