# Supported invoice units

The catalog, editable invoice lines, finalization readiness, and both CII generators share `units.Code`. Comparisons ignore surrounding whitespace and letter case. The visible supplied label is preserved; the XML quantity uses its exact canonical code. Unknown values are rejected with a unit validation error. Historical unsupported values are not rewritten; finalization and generation reject them until a supported unit is selected in an editable draft/correction.

| Canonical UNECE Rec20 code | Accepted input labels |
| --- | --- |
| HUR | hour, hours, hour(s), h, Stunde, Stunden, Stunde(n) |
| DAY | day, days, Tag, Tage, Tag(e) |
| C62 | piece, pieces, unit, flat, Stück, stueck, stk, stk., pauschal, pausch. |
| MIN | minute, minutes, Minuten |
| WEE | week, weeks, Woche, Wochen |
| MON | month, months, Monat, Monate, Monat(e) |
| KGM | kg, kilogram, kilograms |
| KMT | km, kilometre, kilometer |
| MTR | m, metre, meter |
| LTR | l, liter, litre |
| MTK | m2, m² |
| MTQ | m3, m³ |
| KWH | kWh |

Every canonical code above is also accepted directly, including HUR, MIN and WEE. Minute and week are explicitly supported, rather than mapped to pieces. Other Rec20 codes are not accepted until intentionally added to this boundary.

Code meanings verified against [UNECE Recommendation 20](https://unece.org/code-list-recommendations) and the [OpenPeppol May 2026 Rec20 list](https://docs.peppol.eu/poacc/billing/3.0/codelist/UNECERec20/) on 2026-09-06. This is the application's supported subset, not a copied full code list.
