package domain_test

import (
	"os/exec"
	"strings"
	"testing"
)

func TestDomainDependencies(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", "-f", "{{.ImportPath}} {{.Standard}}", ".").Output()
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			t.Fatalf("unexpected go list output: %q", line)
		}
		path := fields[0]
		if path == "context" || path == "net/http" || path == "database/sql" || path == "log/slog" ||
			strings.HasPrefix(path, "go.opentelemetry.io/") ||
			(fields[1] != "true" && path != "example.com/booklog/domain") {
			t.Errorf("domain depends on %s", path)
		}
	}
}
