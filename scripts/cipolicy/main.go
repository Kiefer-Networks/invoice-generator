package main

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/kiefer-networks/invoice-generator/internal/cipolicy"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	if len(os.Args) < 2 {
		return fmt.Errorf("expected actions, container, workflows, summary or coverage")
	}
	switch os.Args[1] {
	case "actions", "workflows":
		if err := cipolicy.Workflows(".github/workflows"); err != nil {
			return err
		}
		installer, err := os.ReadFile("scripts/install-ci-tool.sh")
		if err != nil {
			return err
		}
		return cipolicy.BuildxInstaller(installer)
	case "container":
		c, e := os.ReadFile("compose.yaml")
		if e != nil {
			return e
		}
		d, e := os.ReadFile("Dockerfile")
		if e != nil {
			return e
		}
		return cipolicy.Container(c, d)
	case "summary", "coverage":
		if len(os.Args) != 4 {
			return fmt.Errorf("input and output required")
		}
		input := strings.TrimPrefix(os.Args[2], ".ci-private/")
		output := strings.TrimPrefix(os.Args[3], "artifacts/")
		name := regexp.MustCompile(`^[a-z][a-z0-9-]*\.(json|out)$`)
		if input == os.Args[2] || output == os.Args[3] || !name.MatchString(input) || !name.MatchString(output) {
			return fmt.Errorf("only named private inputs and scrubbed artifact outputs are allowed")
		}
		private, e := os.OpenRoot(".ci-private")
		if e != nil {
			return e
		}
		defer func() { _ = private.Close() }()
		artifacts, e := os.OpenRoot("artifacts")
		if e != nil {
			return e
		}
		defer func() { _ = artifacts.Close() }()
		b, e := private.ReadFile(input)
		if e != nil {
			return e
		}
		if os.Args[1] == "summary" {
			b, e = cipolicy.TestSummary(b)
		} else {
			b, e = cipolicy.Coverage(b)
		}
		if e != nil {
			return e
		}
		return artifacts.WriteFile(output, b, 0600)
	default:
		return fmt.Errorf("unknown policy")
	}
}
