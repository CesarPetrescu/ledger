// Package calendar bridges Ledger to one Nextcloud CalDAV account.
package calendar

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/cesarpetrescu/ledger/internal/store"
	"github.com/emersion/go-ical"
	"github.com/emersion/go-webdav"
	"github.com/emersion/go-webdav/caldav"
	"github.com/jackc/pgx/v5"
)

var (
	ErrNotConnected = errors.New("calendar is not connected")
	ErrPending      = errors.New("Nextcloud authorization is pending")
	ErrConflict     = errors.New("calendar event changed; refresh and try again")
	ErrNotFound     = errors.New("calendar event not found")
)

const (
	maxRange       = 366 * 24 * time.Hour
	loginFlowTTL   = 20 * time.Minute
	syncInterval   = 30 * time.Second
	productID      = "-//Ledger//Calendar Gateway//EN"
	calendarDAVURI = "/remote.php/dav/"
)

type Connection struct {
	Connected         bool      `json:"connected"`
	ServerURL         string    `json:"server_url,omitempty"`
	Username          string    `json:"username,omitempty"`
	SelectedCalendars int       `json:"selected_calendars"`
	ConnectedAt       time.Time `json:"connected_at,omitzero"`
}

type LoginFlow struct {
	ID       string `json:"id"`
	LoginURL string `json:"login_url"`
}

type Calendar struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Selected    bool   `json:"selected"`
	path        string
}

type Event struct {
	ID           string `json:"id"`
	CalendarID   string `json:"calendar_id"`
	CalendarName string `json:"calendar_name"`
	Title        string `json:"title"`
	Start        string `json:"start"`
	End          string `json:"end"`
	AllDay       bool   `json:"all_day"`
	Location     string `json:"location,omitempty"`
	Description  string `json:"description,omitempty"`
	ETag         string `json:"etag"`
	Recurring    bool   `json:"recurring"`
}

type EventInput struct {
	Title       string `json:"title"`
	Start       string `json:"start"`
	End         string `json:"end"`
	AllDay      bool   `json:"all_day"`
	Location    string `json:"location,omitempty"`
	Description string `json:"description,omitempty"`
}

type pendingFlow struct {
	serverURL, token, endpoint string
	expires                    time.Time
}

type Service struct {
	db    *store.DB
	http  *http.Client
	aead  cipher.AEAD
	mu    sync.Mutex
	flows map[string]pendingFlow
}

func NewService(db *store.DB, databaseURL string, client *http.Client) (*Service, error) {
	dsn, err := url.Parse(databaseURL)
	if err != nil || dsn.User == nil {
		return nil, errors.New("database URL must contain credentials")
	}
	password, ok := dsn.User.Password()
	if !ok || password == "" {
		return nil, errors.New("database URL must contain a password")
	}
	key := sha256.Sum256([]byte("ledger-calendar-credential-v1\x00" + password))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if client == nil {
		client = &http.Client{Timeout: 12 * time.Second}
	} else {
		copyClient := *client
		copyClient.Timeout = 12 * time.Second
		client = &copyClient
	}
	return &Service{db: db, http: client, aead: aead, flows: map[string]pendingFlow{}}, nil
}

func (s *Service) Connection(ctx context.Context) (Connection, error) {
	account, err := s.db.GetCalendarAccount(ctx)
	if errors.Is(err, pgx.ErrNoRows) {
		return Connection{}, nil
	}
	if err != nil {
		return Connection{}, err
	}
	return Connection{Connected: true, ServerURL: account.ServerURL, Username: account.Username, SelectedCalendars: len(account.SelectedCalendars), ConnectedAt: account.ConnectedAt}, nil
}

func (s *Service) StartLogin(ctx context.Context, rawServerURL string) (LoginFlow, error) {
	serverURL, err := normalizeServerURL(rawServerURL)
	if err != nil {
		return LoginFlow{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, serverURL+"/index.php/login/v2", nil)
	if err != nil {
		return LoginFlow{}, err
	}
	resp, err := s.http.Do(req)
	if err != nil {
		return LoginFlow{}, fmt.Errorf("connect to Nextcloud: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return LoginFlow{}, fmt.Errorf("Nextcloud login returned %s", resp.Status)
	}
	var body struct {
		Poll struct {
			Token    string `json:"token"`
			Endpoint string `json:"endpoint"`
		} `json:"poll"`
		Login string `json:"login"`
	}
	if err := decodeBoundedJSON(resp.Body, &body); err != nil {
		return LoginFlow{}, fmt.Errorf("invalid Nextcloud login response: %w", err)
	}
	if body.Poll.Token == "" || len(body.Poll.Token) > 2048 || !sameOrigin(serverURL, body.Poll.Endpoint) || !sameOrigin(serverURL, body.Login) {
		return LoginFlow{}, errors.New("Nextcloud returned an invalid login flow")
	}
	id, err := randomID(24)
	if err != nil {
		return LoginFlow{}, err
	}
	s.mu.Lock()
	now := time.Now()
	for key, flow := range s.flows {
		if now.After(flow.expires) {
			delete(s.flows, key)
		}
	}
	s.flows[id] = pendingFlow{serverURL: serverURL, token: body.Poll.Token, endpoint: body.Poll.Endpoint, expires: now.Add(loginFlowTTL)}
	s.mu.Unlock()
	return LoginFlow{ID: id, LoginURL: body.Login}, nil
}

func (s *Service) PollLogin(ctx context.Context, id string) (Connection, error) {
	s.mu.Lock()
	flow, ok := s.flows[id]
	s.mu.Unlock()
	if !ok || time.Now().After(flow.expires) {
		return Connection{}, errors.New("login flow expired; start again")
	}
	form := url.Values{"token": {flow.token}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, flow.endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return Connection{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := s.http.Do(req)
	if err != nil {
		return Connection{}, fmt.Errorf("poll Nextcloud login: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return Connection{}, ErrPending
	}
	if resp.StatusCode != http.StatusOK {
		return Connection{}, fmt.Errorf("Nextcloud login poll returned %s", resp.Status)
	}
	var body struct {
		Server      string `json:"server"`
		LoginName   string `json:"loginName"`
		AppPassword string `json:"appPassword"`
	}
	if err := decodeBoundedJSON(resp.Body, &body); err != nil {
		return Connection{}, fmt.Errorf("invalid Nextcloud credentials response: %w", err)
	}
	returnedServer, err := normalizeServerURL(body.Server)
	if err != nil || returnedServer != flow.serverURL || strings.TrimSpace(body.LoginName) == "" || len(body.LoginName) > 320 || body.AppPassword == "" || len(body.AppPassword) > 2048 {
		return Connection{}, errors.New("Nextcloud returned invalid credentials")
	}
	ciphertext, err := s.seal(body.AppPassword)
	if err != nil {
		return Connection{}, err
	}
	if err := s.db.PutCalendarAccount(ctx, store.CalendarAccount{ServerURL: returnedServer, Username: body.LoginName, PasswordCiphertext: ciphertext, SelectedCalendars: []string{}}); err != nil {
		return Connection{}, err
	}
	s.mu.Lock()
	delete(s.flows, id)
	s.mu.Unlock()
	return s.Connection(ctx)
}

func (s *Service) Disconnect(ctx context.Context) error {
	account, err := s.account(ctx)
	if errors.Is(err, ErrNotConnected) {
		return nil
	}
	if err != nil {
		return err
	}
	password, err := s.open(account.PasswordCiphertext)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, account.ServerURL+"/ocs/v2.php/core/apppassword", nil)
	if err == nil {
		req.SetBasicAuth(account.Username, password)
		req.Header.Set("OCS-APIRequest", "true")
		if resp, requestErr := s.http.Do(req); requestErr == nil {
			resp.Body.Close()
		}
	}
	return s.db.DeleteCalendarAccount(ctx)
}

func (s *Service) Calendars(ctx context.Context) ([]Calendar, error) {
	account, client, err := s.client(ctx)
	if err != nil {
		return nil, err
	}
	principal, err := client.FindCurrentUserPrincipal(ctx)
	if err != nil {
		return nil, fmt.Errorf("discover Nextcloud principal: %w", err)
	}
	home, err := client.FindCalendarHomeSet(ctx, principal)
	if err != nil {
		return nil, fmt.Errorf("discover Nextcloud calendar home: %w", err)
	}
	remote, err := client.FindCalendars(ctx, home)
	if err != nil {
		return nil, fmt.Errorf("list Nextcloud calendars: %w", err)
	}
	calendars := make([]Calendar, 0, len(remote))
	for _, item := range remote {
		if len(item.SupportedComponentSet) > 0 && !slices.Contains(item.SupportedComponentSet, ical.CompEvent) {
			continue
		}
		name := strings.TrimSpace(item.Name)
		if name == "" {
			name = path.Base(strings.TrimSuffix(item.Path, "/"))
		}
		calendars = append(calendars, Calendar{ID: encodeID(item.Path), Name: name, Description: item.Description, Selected: slices.Contains(account.SelectedCalendars, item.Path), path: item.Path})
	}
	sort.Slice(calendars, func(i, j int) bool { return strings.ToLower(calendars[i].Name) < strings.ToLower(calendars[j].Name) })
	return calendars, nil
}

func (s *Service) SelectCalendars(ctx context.Context, ids []string) error {
	available, err := s.Calendars(ctx)
	if err != nil {
		return err
	}
	paths := make([]string, 0, len(ids))
	for _, id := range ids {
		index := slices.IndexFunc(available, func(item Calendar) bool { return item.ID == id })
		if index < 0 {
			return errors.New("calendar selection contains an unknown calendar")
		}
		if !slices.Contains(paths, available[index].path) {
			paths = append(paths, available[index].path)
		}
	}
	return s.db.SetSelectedCalendars(ctx, paths)
}

func (s *Service) ListEvents(ctx context.Context, start, end time.Time, calendarID string) ([]Event, error) {
	if !end.After(start) || end.Sub(start) > maxRange {
		return nil, errors.New("event range must be between 1 second and 366 days")
	}
	account, client, err := s.client(ctx)
	if err != nil {
		return nil, err
	}
	calendars, err := s.selected(ctx, account, calendarID)
	if err != nil {
		return nil, err
	}
	query := &caldav.CalendarQuery{
		CompRequest: caldav.CalendarCompRequest{Name: ical.CompCalendar, AllProps: true, Comps: []caldav.CalendarCompRequest{{Name: ical.CompEvent, AllProps: true}}, Expand: &caldav.CalendarExpandRequest{Start: start, End: end}},
		CompFilter:  caldav.CompFilter{Name: ical.CompCalendar, Comps: []caldav.CompFilter{{Name: ical.CompEvent, Start: start, End: end}}},
	}
	events := []Event{}
	for _, item := range calendars {
		objects, err := client.QueryCalendar(ctx, item.path, query)
		if err != nil {
			return nil, fmt.Errorf("query %s: %w", item.Name, err)
		}
		for _, object := range objects {
			for _, component := range object.Data.Events() {
				event, err := eventFromComponent(component, object.Path, object.ETag, item)
				if err != nil {
					return nil, fmt.Errorf("parse calendar event: %w", err)
				}
				events = append(events, event)
			}
		}
	}
	sort.Slice(events, func(i, j int) bool {
		if events[i].Start == events[j].Start {
			return events[i].Title < events[j].Title
		}
		return events[i].Start < events[j].Start
	})
	return events, nil
}

func (s *Service) GetEvent(ctx context.Context, id string) (Event, error) {
	account, client, err := s.client(ctx)
	if err != nil {
		return Event{}, err
	}
	item, objectPath, err := s.selectedEvent(ctx, account, id)
	if err != nil {
		return Event{}, err
	}
	object, err := client.GetCalendarObject(ctx, objectPath)
	if err != nil {
		return Event{}, mapRemoteError(err)
	}
	component, ok := masterEvent(object.Data)
	if !ok {
		return Event{}, ErrNotFound
	}
	return eventFromComponent(component, object.Path, object.ETag, item)
}

func (s *Service) CreateEvent(ctx context.Context, calendarID string, input EventInput) (Event, error) {
	account, _, err := s.client(ctx)
	if err != nil {
		return Event{}, err
	}
	calendars, err := s.selected(ctx, account, calendarID)
	if err != nil {
		return Event{}, err
	}
	if len(calendars) != 1 {
		return Event{}, errors.New("calendar_id is required")
	}
	start, end, err := validateEventInput(input)
	if err != nil {
		return Event{}, err
	}
	uid, err := randomID(16)
	if err != nil {
		return Event{}, err
	}
	cal := ical.NewCalendar()
	cal.Props.SetText(ical.PropVersion, "2.0")
	cal.Props.SetText(ical.PropProductID, productID)
	event := ical.NewEvent()
	event.Props.SetText(ical.PropUID, uid+"@ledger")
	applyEventInput(event, input, start, end)
	cal.Children = append(cal.Children, event.Component)
	objectPath := strings.TrimRight(calendars[0].path, "/") + "/" + uid + ".ics"
	if err := s.put(ctx, account, objectPath, cal, "", true); err != nil {
		return Event{}, err
	}
	_ = s.db.NotifyAdminEvent(ctx, "calendar")
	return s.GetEvent(ctx, encodeID(objectPath))
}

func (s *Service) UpdateEvent(ctx context.Context, id, etag string, input EventInput) (Event, error) {
	if !validETag(etag) {
		return Event{}, errors.New("etag is required")
	}
	start, end, err := validateEventInput(input)
	if err != nil {
		return Event{}, err
	}
	account, client, err := s.client(ctx)
	if err != nil {
		return Event{}, err
	}
	_, objectPath, err := s.selectedEvent(ctx, account, id)
	if err != nil {
		return Event{}, err
	}
	object, err := client.GetCalendarObject(ctx, objectPath)
	if err != nil {
		return Event{}, mapRemoteError(err)
	}
	component, ok := masterEvent(object.Data)
	if !ok {
		return Event{}, ErrNotFound
	}
	applyEventInput(&component, input, start, end)
	if err := s.put(ctx, account, objectPath, object.Data, etag, false); err != nil {
		return Event{}, err
	}
	_ = s.db.NotifyAdminEvent(ctx, "calendar")
	return s.GetEvent(ctx, id)
}

func (s *Service) DeleteEvent(ctx context.Context, id, etag string) error {
	if !validETag(etag) {
		return errors.New("etag is required")
	}
	account, _, err := s.client(ctx)
	if err != nil {
		return err
	}
	_, objectPath, err := s.selectedEvent(ctx, account, id)
	if err != nil {
		return err
	}
	req, err := s.request(ctx, account, http.MethodDelete, objectPath, nil)
	if err != nil {
		return err
	}
	req.Header.Set("If-Match", etag)
	resp, err := s.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := statusError(resp); err != nil {
		return err
	}
	_ = s.db.NotifyAdminEvent(ctx, "calendar")
	return nil
}

// RunSyncWatch broadcasts external Nextcloud changes to the existing admin stream.
func (s *Service) RunSyncWatch(ctx context.Context) error {
	ticker := time.NewTicker(syncInterval)
	defer ticker.Stop()
	tokens := map[string]string{}
	for {
		account, err := s.account(ctx)
		if err == nil {
			for _, calendarPath := range account.SelectedCalendars {
				token, tokenErr := s.syncToken(ctx, account, calendarPath)
				if tokenErr == nil && token != "" {
					if previous := tokens[calendarPath]; previous != "" && previous != token {
						_ = s.db.NotifyAdminEvent(ctx, "calendar")
					}
					tokens[calendarPath] = token
				}
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (s *Service) account(ctx context.Context) (store.CalendarAccount, error) {
	account, err := s.db.GetCalendarAccount(ctx)
	if errors.Is(err, pgx.ErrNoRows) {
		return store.CalendarAccount{}, ErrNotConnected
	}
	return account, err
}

func (s *Service) client(ctx context.Context) (store.CalendarAccount, *caldav.Client, error) {
	account, err := s.account(ctx)
	if err != nil {
		return store.CalendarAccount{}, nil, err
	}
	password, err := s.open(account.PasswordCiphertext)
	if err != nil {
		return store.CalendarAccount{}, nil, err
	}
	authorized := webdav.HTTPClientWithBasicAuth(s.http, account.Username, password)
	client, err := caldav.NewClient(authorized, account.ServerURL+calendarDAVURI)
	return account, client, err
}

func (s *Service) selected(ctx context.Context, account store.CalendarAccount, calendarID string) ([]Calendar, error) {
	all, err := s.Calendars(ctx)
	if err != nil {
		return nil, err
	}
	selected := make([]Calendar, 0, len(all))
	for _, item := range all {
		if !slices.Contains(account.SelectedCalendars, item.path) || calendarID != "" && item.ID != calendarID {
			continue
		}
		selected = append(selected, item)
	}
	if calendarID != "" && len(selected) == 0 {
		return nil, errors.New("calendar is not selected")
	}
	return selected, nil
}

func (s *Service) selectedEvent(ctx context.Context, account store.CalendarAccount, id string) (Calendar, string, error) {
	objectPath, err := decodeID(id)
	if err != nil {
		return Calendar{}, "", errors.New("invalid event id")
	}
	calendars, err := s.selected(ctx, account, "")
	if err != nil {
		return Calendar{}, "", err
	}
	for _, item := range calendars {
		prefix := strings.TrimRight(item.path, "/") + "/"
		if strings.HasPrefix(objectPath, prefix) && !strings.Contains(strings.TrimPrefix(objectPath, prefix), "/") {
			return item, objectPath, nil
		}
	}
	return Calendar{}, "", errors.New("event is not in a selected calendar")
}

func eventFromComponent(component ical.Event, objectPath, etag string, item Calendar) (Event, error) {
	start, err := component.DateTimeStart(time.UTC)
	if err != nil {
		return Event{}, err
	}
	end, err := component.DateTimeEnd(time.UTC)
	if err != nil {
		return Event{}, err
	}
	startProp := component.Props.Get(ical.PropDateTimeStart)
	allDay := startProp != nil && startProp.ValueType() == ical.ValueDate
	title, _ := component.Props.Text(ical.PropSummary)
	location, _ := component.Props.Text(ical.PropLocation)
	description, _ := component.Props.Text(ical.PropDescription)
	format := func(value time.Time) string {
		if allDay {
			return value.Format(time.DateOnly)
		}
		return value.UTC().Format(time.RFC3339)
	}
	headerETag := ""
	if etag != "" {
		headerETag = `"` + etag + `"`
		if !validETag(headerETag) {
			return Event{}, errors.New("calendar event has an invalid etag")
		}
	}
	return Event{ID: encodeID(objectPath), CalendarID: item.ID, CalendarName: item.Name, Title: title, Start: format(start), End: format(end), AllDay: allDay, Location: location, Description: description, ETag: headerETag, Recurring: component.Props.Get(ical.PropRecurrenceRule) != nil || component.Props.Get(ical.PropRecurrenceID) != nil}, nil
}

func validETag(value string) bool {
	return len(value) >= 2 && len(value) <= 200 && value[0] == '"' && value[len(value)-1] == '"' && !strings.ContainsAny(value[1:len(value)-1], "\"\r\n")
}

func validateEventInput(input EventInput) (time.Time, time.Time, error) {
	input.Title = strings.TrimSpace(input.Title)
	if input.Title == "" || len([]rune(input.Title)) > 200 || strings.ContainsAny(input.Title, "\r\n") {
		return time.Time{}, time.Time{}, errors.New("title must be 1 to 200 characters on one line")
	}
	if len([]rune(input.Location)) > 500 || len([]rune(input.Description)) > 4000 {
		return time.Time{}, time.Time{}, errors.New("location or description is too long")
	}
	format := time.RFC3339
	if input.AllDay {
		format = time.DateOnly
	}
	start, err := time.Parse(format, input.Start)
	if err != nil {
		return time.Time{}, time.Time{}, errors.New("start has an invalid date or time")
	}
	end, err := time.Parse(format, input.End)
	if err != nil {
		return time.Time{}, time.Time{}, errors.New("end has an invalid date or time")
	}
	if !end.After(start) || end.Sub(start) > maxRange {
		return time.Time{}, time.Time{}, errors.New("end must be after start and within 366 days")
	}
	return start, end, nil
}

func applyEventInput(event *ical.Event, input EventInput, start, end time.Time) {
	now := time.Now().UTC()
	event.Props.SetDateTime(ical.PropDateTimeStamp, now)
	event.Props.SetDateTime(ical.PropLastModified, now)
	event.Props.SetText(ical.PropSummary, strings.TrimSpace(input.Title))
	if input.AllDay {
		event.Props.SetDate(ical.PropDateTimeStart, start)
		event.Props.SetDate(ical.PropDateTimeEnd, end)
	} else {
		event.Props.SetDateTime(ical.PropDateTimeStart, start.UTC())
		event.Props.SetDateTime(ical.PropDateTimeEnd, end.UTC())
	}
	setOptionalText(event.Props, ical.PropLocation, input.Location)
	setOptionalText(event.Props, ical.PropDescription, input.Description)
}

func setOptionalText(props ical.Props, name, value string) {
	if value == "" {
		props.Del(name)
	} else {
		props.SetText(name, value)
	}
}

func masterEvent(cal *ical.Calendar) (ical.Event, bool) {
	events := cal.Events()
	for _, event := range events {
		if event.Props.Get(ical.PropRecurrenceID) == nil {
			return event, true
		}
	}
	if len(events) > 0 {
		return events[0], true
	}
	return ical.Event{}, false
}

func (s *Service) put(ctx context.Context, account store.CalendarAccount, objectPath string, cal *ical.Calendar, etag string, create bool) error {
	var body bytes.Buffer
	if err := ical.NewEncoder(&body).Encode(cal); err != nil {
		return err
	}
	req, err := s.request(ctx, account, http.MethodPut, objectPath, &body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", ical.MIMEType+"; charset=utf-8")
	if create {
		req.Header.Set("If-None-Match", "*")
	} else {
		req.Header.Set("If-Match", etag)
	}
	resp, err := s.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return statusError(resp)
}

func (s *Service) request(ctx context.Context, account store.CalendarAccount, method, objectPath string, body io.Reader) (*http.Request, error) {
	password, err := s.open(account.PasswordCiphertext)
	if err != nil {
		return nil, err
	}
	base, _ := url.Parse(account.ServerURL)
	relative := &url.URL{Path: objectPath}
	destination := base.ResolveReference(relative)
	if destination.Scheme != base.Scheme || destination.Host != base.Host {
		return nil, errors.New("calendar path escaped Nextcloud origin")
	}
	req, err := http.NewRequestWithContext(ctx, method, destination.String(), body)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(account.Username, password)
	return req, nil
}

func (s *Service) syncToken(ctx context.Context, account store.CalendarAccount, calendarPath string) (string, error) {
	body := strings.NewReader(`<?xml version="1.0"?><d:propfind xmlns:d="DAV:"><d:prop><d:sync-token/></d:prop></d:propfind>`)
	req, err := s.request(ctx, account, "PROPFIND", calendarPath, body)
	if err != nil {
		return "", err
	}
	req.Header.Set("Depth", "0")
	req.Header.Set("Content-Type", "application/xml; charset=utf-8")
	resp, err := s.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if err := statusError(resp); err != nil {
		return "", err
	}
	var multistatus struct {
		Responses []struct {
			Propstats []struct {
				Prop struct {
					SyncToken string `xml:"sync-token"`
				} `xml:"prop"`
			} `xml:"propstat"`
		} `xml:"response"`
	}
	if err := xml.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&multistatus); err != nil {
		return "", err
	}
	for _, response := range multistatus.Responses {
		for _, propstat := range response.Propstats {
			if propstat.Prop.SyncToken != "" {
				return propstat.Prop.SyncToken, nil
			}
		}
	}
	return "", nil
}

func (s *Service) seal(plaintext string) ([]byte, error) {
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return s.aead.Seal(nonce, nonce, []byte(plaintext), nil), nil
}

func (s *Service) open(ciphertext []byte) (string, error) {
	nonceSize := s.aead.NonceSize()
	if len(ciphertext) <= nonceSize {
		return "", errors.New("calendar credential is invalid")
	}
	plain, err := s.aead.Open(nil, ciphertext[:nonceSize], ciphertext[nonceSize:], nil)
	if err != nil {
		return "", errors.New("calendar credential cannot be decrypted")
	}
	return string(plain), nil
}

func statusError(resp *http.Response) error {
	switch resp.StatusCode {
	case http.StatusOK, http.StatusCreated, http.StatusNoContent, http.StatusMultiStatus:
		return nil
	case http.StatusPreconditionFailed, http.StatusConflict:
		return ErrConflict
	case http.StatusNotFound:
		return ErrNotFound
	default:
		return fmt.Errorf("Nextcloud returned %s", resp.Status)
	}
}

func mapRemoteError(err error) error {
	text := err.Error()
	if strings.Contains(text, "412 Precondition Failed") || strings.Contains(text, "409 Conflict") {
		return ErrConflict
	}
	if strings.Contains(text, "404 Not Found") {
		return ErrNotFound
	}
	return err
}

func normalizeServerURL(raw string) (string, error) {
	if len(raw) > 2048 {
		return "", errors.New("Nextcloud URL is too long")
	}
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("Nextcloud URL must be an absolute server URL")
	}
	host := strings.ToLower(u.Hostname())
	if u.Scheme != "https" && !(u.Scheme == "http" && (host == "localhost" || host == "127.0.0.1" || host == "::1")) {
		return "", errors.New("Nextcloud URL must use HTTPS")
	}
	u.Scheme = strings.ToLower(u.Scheme)
	u.Host = strings.ToLower(u.Host)
	u.Path = strings.TrimRight(u.Path, "/")
	u.RawPath = ""
	return u.String(), nil
}

func sameOrigin(serverURL, candidate string) bool {
	server, err := url.Parse(serverURL)
	if err != nil {
		return false
	}
	u, err := url.Parse(candidate)
	return err == nil && u.User == nil && u.Fragment == "" && strings.EqualFold(server.Scheme, u.Scheme) && strings.EqualFold(server.Host, u.Host)
}

func encodeID(value string) string { return base64.RawURLEncoding.EncodeToString([]byte(value)) }

func decodeID(value string) (string, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || len(decoded) == 0 || len(decoded) > 2048 {
		return "", errors.New("invalid id")
	}
	return string(decoded), nil
}

func randomID(bytesCount int) (string, error) {
	value := make([]byte, bytesCount)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}

func decodeBoundedJSON(reader io.Reader, target any) error {
	decoder := json.NewDecoder(io.LimitReader(reader, 64<<10))
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return errors.New("response contains extra JSON")
	}
	return nil
}
