These synthetic invoice goldens are generated only by the explicit command
`bash scripts/test-visual.sh --update-goldens`, outside CI. Review every changed
PNG before committing it. Never regenerate a baseline to hide an unexplained
failure. Run `bash scripts/test-visual.sh` to compare without modifying files.

The container pins linux/amd64, Go, Debian, Chromium, the Debian package snapshot
(including Poppler and Liberation fonts), UTC, and fontconfig aliases. HTML uses
a 794 x 1123 CSS-pixel viewport and device scale 1. PDF uses the production
FromSnapshot renderer, A4 pagination and footer, rasterized by Poppler at 96 DPI.
Chromium runs as nonroot with the production seccomp profile and its sandbox
enabled, without networking, with a read-only root, no capabilities and bounded
CPU, memory, processes and tmpfs. This harness verifies layout; production
container security is verified separately by the Task 12 container tests.
The fixture has fixed dates, 36 detailed lines, 7%/19% VAT, discounts, correction
reference, totals and payment terms. All identities are synthetic.

Comparison ignores channel differences of at most 12/255 (small antialiasing
noise). It permits at most 0.1% changed pixels globally and at most 1% in any
32 x 32 tile. The local bound catches a clipped amount or shifted small label
which a whole-page percentage could hide. Image dimensions and page count must
match exactly. DOM bounds additionally reject horizontal overflow, overlapping
sections/cells and clipped text. PDF word bounds check page margins, footer/body
separation, repeated footers, and every numbered line item.

Tests also inject HTML clipping/overlap and synthetic pixel changes to prove the
guards reject regressions. PNGs contain no timestamps, temporary paths or PDF
metadata. Intermediate HTML/PDF/text files live in automatically removed test
directories and are never uploaded. On failure the log reports only the golden
basename and measurements; inspect locally using the explicit update command
and review the resulting diff before accepting an intentional layout change.
