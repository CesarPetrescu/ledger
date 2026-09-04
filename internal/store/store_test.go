package store

import "testing"

func TestParseMigrationNameRejectsShortNamesWithoutPanicking(t *testing.T) {
	for _, name := range []string{"", "x", "123", "1234"} {
		name := name
		t.Run(name, func(t *testing.T) {
			if _, err := parseMigrationName(name); err == nil {
				t.Fatalf("parseMigrationName(%q) succeeded, want error", name)
			}
		})
	}
}
