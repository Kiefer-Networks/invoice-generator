package zugferd

import (
	"bytes"
	"context"
	"embed"
	"encoding/xml"
	"errors"
	"fmt"
	"github.com/kiefer-networks/invoice-generator/internal/invoicing"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/template"
	"time"
)

//go:embed schema/*.xsd schema/Validate.java exact.xml
var exactResources embed.FS

func decimal(v int64) string { return fmt.Sprintf("%d.%02d", v/100, v%100) }
func GenerateSnapshot(s invoicing.Snapshot) ([]byte, error) {
	if e := invoicing.ValidateFinalization(s.Draft, s.Company); e != nil {
		return nil, e
	}
	f := template.FuncMap{"esc": xmlEsc, "money": decimal, "unit": mapUnitCode, "qty": func(v int64) string { return fmt.Sprintf("%d.%04d", v/10000, v%10000) }, "base": func(l invoicing.DraftLine) string { return decimal(lineBase(l)) }, "allowance": func(l invoicing.DraftLine) string { return decimal(lineBase(l) - l.NetMinor) }, "date": func(v time.Time) string { return v.Format("20060102") }, "service": func(v string) string { return strings.ReplaceAll(v, "-", "") }, "inc": func(v int) int { return v + 1 }, "cat": func(v int64) string {
		if v == 0 {
			return "Z"
		}
		return "S"
	}}
	src, _ := exactResources.ReadFile("exact.xml")
	t, e := template.New("cii").Funcs(f).Parse(string(src))
	if e != nil {
		return nil, e
	}
	var b bytes.Buffer
	if e = t.Execute(&b, s); e != nil {
		return nil, e
	}
	return b.Bytes(), nil
}

// ValidateCII performs actual UNECE D16B XSD validation with the pinned official
// schemas. A Java 17+ JDK is required; absence fails closed. This is schema
// validation, not a claim of certification or full EN16931 Schematron compliance.
func ValidateCII(ctx context.Context, data []byte) error {
	if len(data) > 4<<20 {
		return errors.New("CII too large")
	}
	dec := xml.NewDecoder(bytes.NewReader(data))
	for {
		tok, e := dec.Token()
		if e != nil {
			if errors.Is(e, io.EOF) {
				break
			}
			return errors.New("invalid CII XML")
		}
		if _, ok := tok.(xml.Directive); ok {
			return errors.New("CII directives forbidden")
		}
	}
	dir, e := os.MkdirTemp("", "invoice-cii-schema-*")
	if e != nil {
		return e
	}
	defer os.RemoveAll(dir)
	e = fs.WalkDir(exactResources, "schema", func(path string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if d.IsDir() {
			return nil
		}
		b, e := exactResources.ReadFile(path)
		if e != nil {
			return e
		}
		return os.WriteFile(filepath.Join(dir, filepath.Base(path)), b, 0600)
	})
	if e != nil {
		return e
	}
	if e = os.WriteFile(filepath.Join(dir, "invoice.xml"), data, 0600); e != nil {
		return e
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "java", "-Xmx128m", filepath.Join(dir, "Validate.java"), filepath.Join(dir, "CrossIndustryInvoice_100pD16B.xsd"), filepath.Join(dir, "invoice.xml"))
	if e = cmd.Run(); e != nil {
		return errors.New("CII schema validation failed (Java 17+ JDK required)")
	}
	return nil
}

func lineBase(l invoicing.DraftLine) int64 {
	p := l.QuantityScaled * l.UnitPriceMinor
	n := p / 10000
	if p%10000 >= 5000 {
		n++
	}
	return n
}
