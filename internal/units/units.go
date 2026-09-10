// Package units defines the supported invoice-unit vocabulary and its exact
// UNECE Recommendation 20 representation. Input labels remain visible as entered;
// only the electronic invoice uses the canonical code. Unknown labels are errors.
package units

import (
	"fmt"
	"strings"
)

const Help = "hour/HUR, day/DAY, piece/C62, minute/MIN, week/WEE, month/MON, kg/KGM, km/KMT, m/MTR, l/LTR, m2/MTK, m3/MTQ or kWh/KWH"

var aliases = map[string]string{
	"hur": "HUR", "hour": "HUR", "hours": "HUR", "hour(s)": "HUR", "h": "HUR", "stunde": "HUR", "stunden": "HUR", "stunde(n)": "HUR",
	"day": "DAY", "days": "DAY", "tag": "DAY", "tage": "DAY", "tag(e)": "DAY",
	"c62": "C62", "piece": "C62", "pieces": "C62", "unit": "C62", "flat": "C62", "st\u00fcck": "C62", "stueck": "C62", "stk": "C62", "stk.": "C62", "pauschal": "C62", "pausch.": "C62",
	"min": "MIN", "minute": "MIN", "minutes": "MIN", "minuten": "MIN",
	"wee": "WEE", "week": "WEE", "weeks": "WEE", "woche": "WEE", "wochen": "WEE",
	"mon": "MON", "month": "MON", "months": "MON", "monat": "MON", "monate": "MON", "monat(e)": "MON",
	"kgm": "KGM", "kg": "KGM", "kilogram": "KGM", "kilograms": "KGM",
	"kmt": "KMT", "km": "KMT", "kilometre": "KMT", "kilometer": "KMT",
	"mtr": "MTR", "m": "MTR", "metre": "MTR", "meter": "MTR",
	"ltr": "LTR", "l": "LTR", "liter": "LTR", "litre": "LTR",
	"mtk": "MTK", "m2": "MTK", "m\u00b2": "MTK",
	"mtq": "MTQ", "m3": "MTQ", "m\u00b3": "MTQ",
	"kwh": "KWH",
}

func Code(value string) (string, error) {
	if code, ok := aliases[strings.ToLower(strings.TrimSpace(value))]; ok {
		return code, nil
	}
	return "", fmt.Errorf("unsupported unit %q; use %s", value, Help)
}
