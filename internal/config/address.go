package config

import (
	"strings"

	"github.com/kiefer-networks/invoice-generator/internal/locale"
)

// AddressLines formats a postal address as up to three lines following
// the common DIN 5008 / ISO postal layout used on invoices:
//
//	Street
//	ZIP City
//	Country
//
// Empty components are omitted. If countryName is empty but country is
// set, the country is resolved to a localized display name (e.g. "DE"
// -> "Deutschland" for lang "de"), falling back to the raw code for
// unmapped combinations.
func AddressLines(street, zip, city, country, countryName, lang string) []string {
	var lines []string
	if street != "" {
		lines = append(lines, street)
	}
	if zipCity := strings.TrimSpace(zip + " " + city); zipCity != "" {
		lines = append(lines, zipCity)
	}
	switch {
	case countryName != "":
		lines = append(lines, countryName)
	case country != "":
		lines = append(lines, locale.CountryName(country, lang))
	}
	return lines
}
