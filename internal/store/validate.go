package store

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"
)

// The registry contract shared by the MCP tools, the seed command, and the admin API.
const (
	maxContextHeaderRunes = 200
	maxEntryBodyRunes     = 4000
)

var (
	projectSlugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,63}$`)
	Tiers              = []string{"focus", "maintain", "park"}
	EntryKinds         = []string{"decision", "note", "todo", "status"}
)

func ValidateProjectSlug(slug string) error {
	if !projectSlugPattern.MatchString(slug) {
		return fmt.Errorf("slug must match %s", projectSlugPattern)
	}
	return nil
}

// ValidateContextHeader bounds single-line fields that are rendered into prompt headers.
func ValidateContextHeader(name, value string, required bool) error {
	length := utf8.RuneCountInString(value)
	if (required && length == 0) || length > maxContextHeaderRunes || strings.ContainsAny(value, "\r\n") {
		return fmt.Errorf("%s must be %s200 characters on one line", name, map[bool]string{true: "1 to ", false: "at most "}[required])
	}
	return nil
}

func ValidateProject(p Project) error {
	if err := ValidateProjectSlug(p.Slug); err != nil {
		return err
	}
	if err := ValidateContextHeader("name", p.Name, true); err != nil {
		return err
	}
	if err := ValidateContextHeader("deadline", p.Deadline, false); err != nil {
		return err
	}
	if !slices.Contains(Tiers, p.Tier) {
		return fmt.Errorf("tier must be one of %s", strings.Join(Tiers, ", "))
	}
	if p.HoursWK < 0 || p.HoursWK > 168 {
		return fmt.Errorf("hours_wk must be between 0 and 168")
	}
	return nil
}

func ValidateEntry(kind, body string) error {
	if !slices.Contains(EntryKinds, kind) {
		return fmt.Errorf("kind must be one of %s", strings.Join(EntryKinds, ", "))
	}
	if length := utf8.RuneCountInString(body); length == 0 || length > maxEntryBodyRunes {
		return fmt.Errorf("body must be 1 to %d characters", maxEntryBodyRunes)
	}
	return nil
}
