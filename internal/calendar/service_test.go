package calendar

import (
	"testing"
	"time"

	"github.com/emersion/go-ical"
)

func TestCalendarBoundaryValidationAndCredentialEncryption(t *testing.T) {
	service, err := NewService(nil, "postgres://ledger:secret@database/ledger", nil)
	if err != nil {
		t.Fatal(err)
	}
	ciphertext, err := service.seal("nextcloud-app-password")
	if err != nil {
		t.Fatal(err)
	}
	if string(ciphertext) == "nextcloud-app-password" {
		t.Fatal("credential was stored as plaintext")
	}
	if plaintext, err := service.open(ciphertext); err != nil || plaintext != "nextcloud-app-password" {
		t.Fatalf("credential round trip = %q, %v", plaintext, err)
	}
	ciphertext[len(ciphertext)-1] ^= 1
	if _, err := service.open(ciphertext); err == nil {
		t.Fatal("tampered credential decrypted")
	}

	if got, err := normalizeServerURL("https://Cloud.Example.com:443/nextcloud/"); err != nil || got != "https://cloud.example.com:443/nextcloud" {
		t.Fatalf("normalized URL = %q, %v", got, err)
	}
	for _, unsafe := range []string{"http://cloud.example.com", "https://user@cloud.example.com", "https://cloud.example.com?redirect=evil"} {
		if _, err := normalizeServerURL(unsafe); err == nil {
			t.Errorf("unsafe server URL accepted: %s", unsafe)
		}
	}

	if _, _, err := validateEventInput(EventInput{Title: "Plan", Start: "2026-09-05T10:00:00Z", End: "2026-09-05T09:00:00Z"}); err == nil {
		t.Fatal("backwards event accepted")
	}
	if validETag("*") || validETag("unquoted") || !validETag(`"version-1"`) {
		t.Fatal("ETag validation is unsafe")
	}

	start := time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC)
	end := start.Add(24 * time.Hour)
	component := ical.NewEvent()
	component.Props.SetText(ical.PropUID, "test@ledger")
	applyEventInput(component, EventInput{Title: " Day off ", Start: "2026-09-05", End: "2026-09-06", AllDay: true}, start, end)
	event, err := eventFromComponent(*component, "/calendar/test.ics", "version-1", Calendar{ID: "calendar", Name: "Work"})
	if err != nil || event.Title != "Day off" || !event.AllDay || event.ETag != `"version-1"` {
		t.Fatalf("event conversion = %#v, %v", event, err)
	}
}
