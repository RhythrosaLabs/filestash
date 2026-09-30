package plg_backend_caldav

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/emersion/go-ical"
	"github.com/emersion/go-webdav"
	"github.com/emersion/go-webdav/caldav"
	. "github.com/mickael-kerjean/filestash/server/common"
)

// memBackend is a tiny in-memory CalDAV server
type memBackend struct {
	mu      sync.Mutex
	objects map[string]*ical.Calendar
}

var calendars = []caldav.Calendar{
	{Path: "/me/calendars/work/", Name: "Work", SupportedComponentSet: []string{"VEVENT"}},
	{Path: "/me/calendars/home/", Name: "Home", SupportedComponentSet: []string{"VEVENT"}},
	{Path: "/me/calendars/todo/", Name: "Reminders", SupportedComponentSet: []string{"VTODO"}},
}

func (b *memBackend) CurrentUserPrincipal(ctx context.Context) (string, error) { return "/me/", nil }
func (b *memBackend) CalendarHomeSetPath(ctx context.Context) (string, error) {
	return "/me/calendars/", nil
}
func (b *memBackend) CreateCalendar(ctx context.Context, c *caldav.Calendar) error {
	return webdav.NewHTTPError(http.StatusForbidden, nil)
}
func (b *memBackend) ListCalendars(ctx context.Context) ([]caldav.Calendar, error) {
	return calendars, nil
}
func (b *memBackend) GetCalendar(ctx context.Context, p string) (*caldav.Calendar, error) {
	for _, c := range calendars {
		if c.Path == p {
			return &c, nil
		}
	}
	return nil, webdav.NewHTTPError(http.StatusNotFound, nil)
}
func (b *memBackend) GetCalendarObject(ctx context.Context, p string, req *caldav.CalendarCompRequest) (*caldav.CalendarObject, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if cal, ok := b.objects[p]; ok {
		return &caldav.CalendarObject{Path: p, Data: cal, ModTime: time.Now(), ETag: "x"}, nil
	}
	return nil, webdav.NewHTTPError(http.StatusNotFound, nil)
}
func (b *memBackend) ListCalendarObjects(ctx context.Context, p string, req *caldav.CalendarCompRequest) ([]caldav.CalendarObject, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := []caldav.CalendarObject{}
	for k, cal := range b.objects {
		if strings.HasPrefix(k, p) {
			out = append(out, caldav.CalendarObject{Path: k, Data: cal, ModTime: time.Now(), ETag: "x"})
		}
	}
	return out, nil
}
func (b *memBackend) QueryCalendarObjects(ctx context.Context, p string, q *caldav.CalendarQuery) ([]caldav.CalendarObject, error) {
	return b.ListCalendarObjects(ctx, p, &q.CompRequest)
}
func (b *memBackend) PutCalendarObject(ctx context.Context, p string, cal *ical.Calendar, opts *caldav.PutCalendarObjectOptions) (*caldav.CalendarObject, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.objects[p] = cal
	return &caldav.CalendarObject{Path: p, Data: cal, ETag: "x"}, nil
}
func (b *memBackend) DeleteCalendarObject(ctx context.Context, p string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.objects, p)
	return nil
}

const event = "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:test\r\nBEGIN:VEVENT\r\nUID:abc-123\r\nDTSTAMP:20260101T000000Z\r\nDTSTART:20261015T090000Z\r\nSUMMARY:Dentist: checkup\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"

func TestCaldav(t *testing.T) {
	mem := &memBackend{objects: map[string]*ical.Calendar{}}
	srv := httptest.NewServer(&caldav.Handler{Backend: mem})
	defer srv.Close()

	b, err := (Caldav{}).Init(map[string]string{"url": srv.URL, "username": "u", "password": "p"}, &App{Context: context.Background()})
	if err != nil {
		t.Fatal(err)
	}
	root, err := b.Ls("/")
	if err != nil || len(root) != 2 {
		t.Fatalf("expected 2 event calendars (reminders excluded): %v %v", root, err)
	}
	if err := b.Save("/Work/new.ics", strings.NewReader(event)); err != nil {
		t.Fatal(err)
	}
	files, err := b.Ls("/Work/")
	if err != nil || len(files) != 1 {
		t.Fatalf("ls work: %v %v", files, err)
	}
	name := files[0].Name()
	if name != "2026-10-15 Dentist checkup [abc-123].ics" {
		t.Fatalf("unexpected name %q", name)
	}
	r, err := b.Cat("/Work/" + name)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(r)
	if !strings.Contains(string(body), "SUMMARY:Dentist: checkup") {
		t.Fatalf("unexpected body %s", body)
	}
	if err := b.Mv("/Work/"+name, "/Work/other.ics"); err == nil {
		t.Fatal("renaming an event should fail")
	}
	if err := b.Mv("/Work/"+name, "/Home/"+name); err != nil {
		t.Fatal(err)
	}
	if files, _ := b.Ls("/Work/"); len(files) != 0 {
		t.Fatalf("work should be empty: %v", files)
	}
	if files, _ := b.Ls("/Home/"); len(files) != 1 {
		t.Fatalf("home should have the event: %v", files)
	}
	if err := b.Rm("/Home/"); err == nil {
		t.Fatal("deleting a calendar must be refused")
	}
	if err := b.Rm("/Home/" + name); err != nil {
		t.Fatal(err)
	}
	if len(mem.objects) != 0 {
		t.Fatalf("event not deleted")
	}
	if _, err := b.Ls("/Nope/"); err != ErrNotFound {
		t.Fatalf("expected not found, got %v", err)
	}
}
