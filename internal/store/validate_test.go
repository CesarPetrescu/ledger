package store

import (
	"strings"
	"testing"
)

func TestValidateProjectEnforcesRegistryContract(t *testing.T) {
	valid := Project{Slug: "atlas", Name: "Atlas", Tier: "focus", HoursWK: 8, Deadline: "Friday"}
	if err := ValidateProject(valid); err != nil {
		t.Fatalf("valid project rejected: %v", err)
	}
	invalid := map[string]Project{
		"slug uppercase":     {Slug: "Atlas", Name: "Atlas", Tier: "focus"},
		"slug too short":     {Slug: "a", Name: "Atlas", Tier: "focus"},
		"slug too long":      {Slug: strings.Repeat("a", 65), Name: "Atlas", Tier: "focus"},
		"name empty":         {Slug: "atlas", Name: "", Tier: "focus"},
		"name multiline":     {Slug: "atlas", Name: "Atlas\nforged", Tier: "focus"},
		"name too long":      {Slug: "atlas", Name: strings.Repeat("n", 201), Tier: "focus"},
		"deadline too long":  {Slug: "atlas", Name: "Atlas", Tier: "focus", Deadline: strings.Repeat("d", 201)},
		"tier unknown":       {Slug: "atlas", Name: "Atlas", Tier: "urgent"},
		"hours negative":     {Slug: "atlas", Name: "Atlas", Tier: "focus", HoursWK: -1},
		"hours above a week": {Slug: "atlas", Name: "Atlas", Tier: "focus", HoursWK: 169},
	}
	for name, project := range invalid {
		if err := ValidateProject(project); err == nil {
			t.Errorf("%s: accepted %#v", name, project)
		}
	}
}

func TestValidateEntryEnforcesKindAndBodyBounds(t *testing.T) {
	for _, kind := range []string{"decision", "note", "todo", "status"} {
		if err := ValidateEntry(kind, "body"); err != nil {
			t.Errorf("kind %s rejected: %v", kind, err)
		}
	}
	if err := ValidateEntry("note", strings.Repeat("Ș", 4000)); err != nil {
		t.Errorf("4000-rune body rejected: %v", err)
	}
	for name, test := range map[string]struct{ kind, body string }{
		"unknown kind":  {"memo", "body"},
		"empty body":    {"note", ""},
		"body too long": {"note", strings.Repeat("b", 4001)},
	} {
		if err := ValidateEntry(test.kind, test.body); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestValidateContextHeaderMatchesMCPRules(t *testing.T) {
	if err := ValidateContextHeader("name", strings.Repeat("Ș", 200), true); err != nil {
		t.Fatalf("200-rune header rejected: %v", err)
	}
	if err := ValidateContextHeader("deadline", "", false); err != nil {
		t.Fatalf("optional empty header rejected: %v", err)
	}
	for name, test := range map[string]struct {
		value    string
		required bool
	}{
		"required empty": {"", true},
		"too long":       {strings.Repeat("x", 201), false},
		"newline":        {"a\nb", false},
		"carriage":       {"a\rb", false},
	} {
		if err := ValidateContextHeader("field", test.value, test.required); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	if err := ValidateProjectSlug("bad slug"); err == nil {
		t.Error("slug with space accepted")
	}
}
