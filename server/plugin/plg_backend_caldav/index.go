package plg_backend_caldav

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"regexp"
	"strings"
	"sync"
	"time"

	. "github.com/mickael-kerjean/filestash/server/common"

	"github.com/emersion/go-ical"
	"github.com/emersion/go-webdav"
	"github.com/emersion/go-webdav/caldav"
)

// Calendars as a storage: each calendar is a folder and each event an .ics
// file named after its date and title. Works with any CalDAV server: iCloud
// (https://caldav.icloud.com), Fastmail, Nextcloud, Radicale, ... Google
// Calendar requires OAuth and isn't supported here.

var CaldavCache AppCache

func init() {
	Backend.Register("caldav", &Caldav{})
	CaldavCache = NewAppCache(5, 1)
}

type Caldav struct {
	client    *caldav.Client
	ctx       context.Context
	mu        *sync.Mutex
	calendars map[string]string // folder name -> calendar path
}

func (this Caldav) Init(params map[string]string, app *App) (IBackend, error) {
	ctx := context.Background()
	if app != nil && app.Context != nil {
		ctx = app.Context
	}
	if c, ok := CaldavCache.Get(params).(*Caldav); ok && c != nil {
		clone := *c
		clone.ctx = ctx
		return &clone, nil
	}
	if params["url"] == "" {
		return nil, NewError("Missing server url", 400)
	}
	httpClient := webdav.HTTPClientWithBasicAuth(HTTPClient(), params["username"], params["password"])
	client, err := caldav.NewClient(httpClient, params["url"])
	if err != nil {
		return nil, ErrNotValid
	}
	backend := &Caldav{client: client, ctx: ctx, mu: &sync.Mutex{}}
	if err := backend.discover(); err != nil {
		Log.Debug("plg_backend_caldav::init err=%s", err.Error())
		return nil, ErrAuthenticationFailed
	}
	CaldavCache.Set(params, backend)
	return backend, nil
}

func (this *Caldav) discover() error {
	principal, err := this.client.FindCurrentUserPrincipal(this.ctx)
	if err != nil {
		return err
	}
	home, err := this.client.FindCalendarHomeSet(this.ctx, principal)
	if err != nil {
		return err
	}
	cals, err := this.client.FindCalendars(this.ctx, home)
	if err != nil {
		return err
	}
	m := map[string]string{}
	for _, c := range cals {
		if len(c.SupportedComponentSet) > 0 && !contains(c.SupportedComponentSet, "VEVENT") {
			continue // reminders / tasks lists
		}
		name := sanitize(c.Name)
		if name == "" {
			name = sanitize(path.Base(strings.TrimSuffix(c.Path, "/")))
		}
		for base, i := name, 2; m[name] != ""; i++ {
			name = fmt.Sprintf("%s (%d)", base, i)
		}
		m[name] = c.Path
	}
	this.mu.Lock()
	this.calendars = m
	this.mu.Unlock()
	return nil
}

func (this Caldav) LoginForm() Form {
	return Form{
		Elmnts: []FormElement{
			{Name: "type", Type: "hidden", Value: "caldav"},
			{Name: "url", Type: "text", Placeholder: "CalDAV url*, eg: https://caldav.icloud.com"},
			{Name: "username", Type: "text", Placeholder: "Username*"},
			{Name: "password", Type: "password", Placeholder: "Password or app password*"},
		},
	}
}

func (this Caldav) Home() (string, error) {
	return "/", nil
}

// ---------------------------------------------------------------- paths

var (
	idRegex     = regexp.MustCompile(`\[([^\[\]]+)\]\.ics$`)
	unsafeChars = regexp.MustCompile(`[/\\:*?"<>|\[\]\x00-\x1f]+`)
)

func sanitize(s string) string {
	s = strings.Join(strings.Fields(unsafeChars.ReplaceAllString(s, " ")), " ")
	if len(s) > 80 {
		s = strings.TrimSpace(s[:80])
	}
	return s
}

func contains(list []string, s string) bool {
	for _, l := range list {
		if strings.EqualFold(l, s) {
			return true
		}
	}
	return false
}

// parse returns the calendar path and, for an event, the event resource path
func (this *Caldav) parse(p string) (cal string, event string, err error) {
	parts := strings.Split(strings.Trim(p, "/"), "/")
	if parts[0] == "" {
		return "", "", nil
	}
	if len(parts) > 2 {
		return "", "", ErrNotFound
	}
	this.mu.Lock()
	cal = this.calendars[parts[0]]
	this.mu.Unlock()
	if cal == "" {
		return "", "", ErrNotFound
	}
	if len(parts) == 1 {
		return cal, "", nil
	}
	m := idRegex.FindStringSubmatch(parts[1])
	if m == nil {
		return cal, "", ErrNotFound
	}
	return cal, strings.TrimSuffix(cal, "/") + "/" + m[1] + ".ics", nil
}

func filename(obj caldav.CalendarObject) string {
	id := strings.TrimSuffix(path.Base(obj.Path), ".ics")
	summary, date := "(no title)", obj.ModTime
	if obj.Data != nil {
		for _, ev := range obj.Data.Events() {
			if s, err := ev.Props.Text(ical.PropSummary); err == nil && strings.TrimSpace(s) != "" {
				summary = s
			}
			if t, err := ev.DateTimeStart(time.UTC); err == nil {
				date = t
			}
			break
		}
	}
	if s := sanitize(summary); s != "" {
		summary = s
	}
	return fmt.Sprintf("%s %s [%s].ics", date.Format("2006-01-02"), summary, id)
}

// ---------------------------------------------------------------- operations

func (this Caldav) Ls(p string) ([]os.FileInfo, error) {
	cal, event, err := this.parse(p)
	if err != nil {
		return nil, err
	} else if event != "" {
		return nil, ErrNotValid
	}
	files := []os.FileInfo{}
	if cal == "" {
		this.discover()
		this.mu.Lock()
		for name := range this.calendars {
			files = append(files, File{FName: name, FType: "directory", FSize: -1})
		}
		this.mu.Unlock()
		return files, nil
	}
	objs, err := this.client.QueryCalendar(this.ctx, cal, &caldav.CalendarQuery{
		CompRequest: caldav.CalendarCompRequest{
			Name:  "VCALENDAR",
			Comps: []caldav.CalendarCompRequest{{Name: "VEVENT", Props: []string{"SUMMARY", "DTSTART", "UID"}}},
		},
		CompFilter: caldav.CompFilter{Name: "VCALENDAR", Comps: []caldav.CompFilter{{Name: "VEVENT"}}},
	})
	if err != nil {
		Log.Debug("plg_backend_caldav::ls err=%s", err.Error())
		return nil, ErrFilesystemError
	}
	for _, obj := range objs {
		files = append(files, File{FName: filename(obj), FType: "file", FSize: obj.ContentLength, FTime: obj.ModTime.Unix()})
	}
	return files, nil
}

func (this Caldav) Stat(p string) (os.FileInfo, error) {
	cal, event, err := this.parse(p)
	if err != nil {
		return nil, err
	}
	if event == "" {
		name := path.Base(strings.TrimSuffix(p, "/"))
		if cal == "" {
			name = "/"
		}
		return File{FName: name, FType: "directory", FSize: -1}, nil
	}
	obj, err := this.client.GetCalendarObject(this.ctx, event)
	if err != nil {
		return nil, ErrNotFound
	}
	return File{FName: filename(*obj), FType: "file", FSize: obj.ContentLength, FTime: obj.ModTime.Unix()}, nil
}

func (this Caldav) Cat(p string) (io.ReadCloser, error) {
	_, event, err := this.parse(p)
	if err != nil {
		return nil, err
	} else if event == "" {
		return nil, ErrNotValid
	}
	obj, err := this.client.GetCalendarObject(this.ctx, event)
	if err != nil {
		return nil, ErrNotFound
	}
	var buf bytes.Buffer
	if err := ical.NewEncoder(&buf).Encode(obj.Data); err != nil {
		return nil, ErrFilesystemError
	}
	return io.NopCloser(&buf), nil
}

func (this Caldav) Save(p string, file io.Reader) error {
	parts := strings.Split(strings.Trim(p, "/"), "/")
	if len(parts) != 2 || !strings.HasSuffix(strings.ToLower(parts[1]), ".ics") {
		return NewError("Only .ics files can be saved inside a calendar", 400)
	}
	cal, _, err := this.parse("/" + parts[0] + "/")
	if err != nil {
		return err
	}
	data, err := ical.NewDecoder(io.LimitReader(file, 10<<20)).Decode()
	if err != nil {
		return NewError("Not a valid calendar file", 400)
	}
	return this.put(cal, data)
}

func (this Caldav) put(cal string, data *ical.Calendar) error {
	id := ""
	for _, ev := range data.Events() {
		if uid, err := ev.Props.Text(ical.PropUID); err == nil {
			id = sanitize(uid)
		}
		break
	}
	if id == "" {
		return NewError("Event without UID", 400)
	}
	if _, err := this.client.PutCalendarObject(this.ctx, strings.TrimSuffix(cal, "/")+"/"+id+".ics", data); err != nil {
		Log.Debug("plg_backend_caldav::put err=%s", err.Error())
		return ErrFilesystemError
	}
	return nil
}

func (this Caldav) Rm(p string) error {
	_, event, err := this.parse(p)
	if err != nil {
		return err
	} else if event == "" {
		return NewError("Deleting a whole calendar isn't supported, do it from your calendar app", 400)
	}
	if err := this.client.RemoveAll(this.ctx, event); err != nil {
		return ErrFilesystemError
	}
	return nil
}

func (this Caldav) Mv(from, to string) error {
	_, event, err := this.parse(from)
	if err != nil {
		return err
	} else if event == "" {
		return NewError("Calendars can't be renamed from here", 400)
	}
	parts := strings.Split(strings.Trim(to, "/"), "/")
	dst, _, err := this.parse("/" + parts[0] + "/")
	if err != nil {
		return err
	}
	if strings.HasPrefix(event, dst) {
		return NewError("Events can be moved to another calendar but can't be renamed", 400)
	}
	obj, err := this.client.GetCalendarObject(this.ctx, event)
	if err != nil {
		return ErrNotFound
	}
	if err := this.put(dst, obj.Data); err != nil {
		return err
	}
	if err := this.client.RemoveAll(this.ctx, event); err != nil {
		return ErrFilesystemError
	}
	return nil
}

func (this Caldav) Mkdir(p string) error {
	return NewError("Create new calendars from your calendar app", 400)
}

func (this Caldav) Touch(p string) error {
	return ErrNotSupported
}
