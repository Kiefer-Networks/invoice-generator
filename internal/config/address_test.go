package config

import (
	"reflect"
	"testing"
)

func TestAddressLines(t *testing.T) {
	cases := []struct {
		name                                  string
		street, zip, city, country, countryNm string
		lang                                  string
		want                                  []string
	}{
		{
			name:   "full DE address, no explicit country name",
			street: "Adalbert-Stifter-Str. 6", zip: "95512", city: "Neudrossenfeld",
			country: "DE", lang: "de",
			want: []string{"Adalbert-Stifter-Str. 6", "95512 Neudrossenfeld", "Deutschland"},
		},
		{
			name:   "explicit country name takes precedence over code lookup",
			street: "Client Road 42", zip: "54321", city: "Munich",
			country: "DE", countryNm: "Germany", lang: "de",
			want: []string{"Client Road 42", "54321 Munich", "Germany"},
		},
		{
			name:   "unmapped country code falls back to the raw code",
			street: "Rue de Paris 1", zip: "75001", city: "Paris",
			country: "XX", lang: "de",
			want: []string{"Rue de Paris 1", "75001 Paris", "XX"},
		},
		{
			name:   "no country at all",
			street: "Somewhere 1", zip: "12345", city: "Anytown",
			want: []string{"Somewhere 1", "12345 Anytown"},
		},
		{
			name: "missing street",
			zip:  "12345", city: "Anytown", country: "DE", lang: "en",
			want: []string{"12345 Anytown", "Germany"},
		},
		{
			name:   "only street, nothing else",
			street: "Just A Street",
			want:   []string{"Just A Street"},
		},
		{
			name: "everything empty",
			want: nil,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := AddressLines(c.street, c.zip, c.city, c.country, c.countryNm, c.lang)
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("AddressLines(...) = %#v, want %#v", got, c.want)
			}
		})
	}
}

func TestAddressLinesLocalizesCountryName(t *testing.T) {
	got := AddressLines("Street 1", "1000", "Vienna", "AT", "", "fr")
	want := []string{"Street 1", "1000 Vienna", "Autriche"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("AddressLines(...) = %#v, want %#v", got, want)
	}
}
