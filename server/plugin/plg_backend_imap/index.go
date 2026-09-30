package plg_backend_imap

import (
	"bytes"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	. "github.com/mickael-kerjean/filestash/server/common"

	"github.com/emersion/go-imap"
	"github.com/emersion/go-imap/client"
)

// Email as a storage: mailboxes are folders and emails are .eml files which
// makes them browsable, searchable, movable between folders and readable by
// the AI assistant like any other file.

const maxListedMessages = 500

var ImapCache AppCache

func init() {
	Backend.Register("imap", &Imap{})
	ImapCache = NewAppCache(2, 1)
	ImapCache.OnEvict(func(key string, value interface{}) {
		if c, ok := value.(*Imap); ok && c.client != nil {
			c.mu.Lock()
			c.client.Logout()
			c.mu.Unlock()
		}
	})
}

type Imap struct {
	client *client.Client
	delim  string
	mu     *sync.Mutex
}

func (this Imap) Init(params map[string]string, app *App) (IBackend, error) {
	if c, ok := ImapCache.Get(params).(*Imap); ok && c != nil {
		c.mu.Lock()
		err := c.client.Noop()
		c.mu.Unlock()
		if err == nil {
			return c, nil
		}
		ImapCache.Del(params)
	}
	if params["hostname"] == "" || params["username"] == "" {
		return nil, NewError("Missing hostname or username", 400)
	}
	security := params["security"]
	port := params["port"]
	if port == "" {
		port = "993"
		if security == "starttls" || security == "none" {
			port = "143"
		}
	}
	addr := net.JoinHostPort(params["hostname"], port)
	dialer := &net.Dialer{Timeout: 15 * time.Second}
	var (
		c   *client.Client
		err error
	)
	switch security {
	case "none":
		c, err = client.DialWithDialer(dialer, addr)
	case "starttls":
		if c, err = client.DialWithDialer(dialer, addr); err == nil {
			err = c.StartTLS(&tls.Config{ServerName: params["hostname"]})
		}
	default:
		c, err = client.DialWithDialerTLS(dialer, addr, &tls.Config{ServerName: params["hostname"]})
	}
	if err != nil {
		Log.Debug("plg_backend_imap::dial err=%s", err.Error())
		return nil, ErrNotReachable
	}
	c.Timeout = 60 * time.Second
	if err = c.Login(params["username"], params["password"]); err != nil {
		c.Logout()
		return nil, ErrAuthenticationFailed
	}
	backend := &Imap{client: c, delim: "/", mu: &sync.Mutex{}}
	ch := make(chan *imap.MailboxInfo, 1)
	if err := c.List("", "", ch); err == nil {
		for m := range ch {
			if m.Delimiter != "" {
				backend.delim = m.Delimiter
			}
		}
	}
	ImapCache.Set(params, backend)
	return backend, nil
}

func (this Imap) LoginForm() Form {
	return Form{
		Elmnts: []FormElement{
			{Name: "type", Type: "hidden", Value: "imap"},
			{Name: "hostname", Type: "text", Placeholder: "IMAP server*, eg: imap.gmail.com"},
			{Name: "username", Type: "text", Placeholder: "Email*"},
			{Name: "password", Type: "password", Placeholder: "Password or app password*"},
			{Name: "advanced", Type: "enable", Placeholder: "Advanced", Target: []string{"imap_port", "imap_security"}},
			{Id: "imap_port", Name: "port", Type: "number", Placeholder: "Port"},
			{Id: "imap_security", Name: "security", Type: "select", Opts: []string{"tls", "starttls", "none"}, Placeholder: "Security"},
		},
	}
}

func (this Imap) Home() (string, error) {
	return "/", nil
}

// ---------------------------------------------------------------- paths

var uidRegex = regexp.MustCompile(`\[(\d+)\]\.eml$`)

// split a path into the mailbox name and, for a file, the message uid
func (this Imap) parse(path string) (mailbox string, uid uint32, isFile bool, err error) {
	path = strings.Trim(path, "/")
	parts := strings.Split(path, "/")
	if path == "" {
		return "", 0, false, nil
	}
	last := parts[len(parts)-1]
	if strings.HasSuffix(last, ".eml") {
		m := uidRegex.FindStringSubmatch(last)
		if m == nil || len(parts) < 2 {
			return "", 0, true, ErrNotFound
		}
		n, err := strconv.ParseUint(m[1], 10, 32)
		if err != nil {
			return "", 0, true, ErrNotFound
		}
		return strings.Join(parts[:len(parts)-1], this.delim), uint32(n), true, nil
	}
	return strings.Join(parts, this.delim), 0, false, nil
}

var unsafeChars = regexp.MustCompile(`[/\\:*?"<>|\x00-\x1f]+`)

func filename(msg *imap.Message) string {
	subject := "(no subject)"
	date := msg.InternalDate
	if msg.Envelope != nil {
		if s := strings.TrimSpace(msg.Envelope.Subject); s != "" {
			subject = s
		}
		if !msg.Envelope.Date.IsZero() {
			date = msg.Envelope.Date
		}
	}
	subject = strings.Join(strings.Fields(unsafeChars.ReplaceAllString(subject, " ")), " ")
	if len(subject) > 80 {
		subject = strings.TrimSpace(subject[:80])
	}
	return fmt.Sprintf("%s %s [%d].eml", date.Format("2006-01-02"), subject, msg.Uid)
}

// ---------------------------------------------------------------- operations

func (this Imap) Ls(path string) ([]os.FileInfo, error) {
	this.mu.Lock()
	defer this.mu.Unlock()
	mailbox, _, isFile, err := this.parse(path)
	if err != nil || isFile {
		return nil, ErrNotValid
	}
	files := []os.FileInfo{}

	// child mailboxes
	pattern := "%"
	if mailbox != "" {
		pattern = mailbox + this.delim + "%"
	}
	ch := make(chan *imap.MailboxInfo, 10)
	done := make(chan error, 1)
	go func() { done <- this.client.List("", pattern, ch) }()
	for m := range ch {
		name := m.Name
		if mailbox != "" {
			name = strings.TrimPrefix(name, mailbox+this.delim)
		}
		if name == "" || strings.Contains(name, "/") {
			continue
		}
		files = append(files, File{FName: name, FType: "directory", FSize: -1})
	}
	if err := <-done; err != nil {
		return nil, ErrFilesystemError
	}
	if mailbox == "" {
		return files, nil
	}

	// messages: the most recent ones
	status, err := this.client.Select(mailbox, true)
	if err != nil {
		if len(files) > 0 {
			return files, nil // \Noselect container
		}
		return nil, ErrNotFound
	}
	if status.Messages == 0 {
		return files, nil
	}
	from := uint32(1)
	if status.Messages > maxListedMessages {
		from = status.Messages - maxListedMessages + 1
	}
	seq := new(imap.SeqSet)
	seq.AddRange(from, status.Messages)
	msgs := make(chan *imap.Message, 20)
	go func() {
		done <- this.client.Fetch(seq, []imap.FetchItem{imap.FetchUid, imap.FetchEnvelope, imap.FetchRFC822Size, imap.FetchInternalDate}, msgs)
	}()
	for msg := range msgs {
		files = append(files, File{FName: filename(msg), FType: "file", FSize: int64(msg.Size), FTime: msg.InternalDate.Unix()})
	}
	if err := <-done; err != nil {
		return nil, ErrFilesystemError
	}
	return files, nil
}

func (this Imap) fetch(mailbox string, uid uint32, items []imap.FetchItem) (*imap.Message, error) {
	if _, err := this.client.Select(mailbox, true); err != nil {
		return nil, ErrNotFound
	}
	seq := new(imap.SeqSet)
	seq.AddNum(uid)
	ch := make(chan *imap.Message, 1)
	if err := this.client.UidFetch(seq, items, ch); err != nil {
		return nil, ErrFilesystemError
	}
	msg := <-ch
	if msg == nil {
		return nil, ErrNotFound
	}
	for range ch {
	}
	return msg, nil
}

func (this Imap) Stat(path string) (os.FileInfo, error) {
	this.mu.Lock()
	defer this.mu.Unlock()
	mailbox, uid, isFile, err := this.parse(path)
	if err != nil {
		return nil, err
	}
	if !isFile {
		if mailbox == "" {
			return File{FName: "/", FType: "directory"}, nil
		}
		ch := make(chan *imap.MailboxInfo, 1)
		if err := this.client.List("", mailbox, ch); err != nil {
			return nil, ErrFilesystemError
		}
		found := false
		for range ch {
			found = true
		}
		if !found {
			return nil, ErrNotFound
		}
		parts := strings.Split(strings.Trim(path, "/"), "/")
		return File{FName: parts[len(parts)-1], FType: "directory", FSize: -1}, nil
	}
	msg, err := this.fetch(mailbox, uid, []imap.FetchItem{imap.FetchUid, imap.FetchEnvelope, imap.FetchRFC822Size, imap.FetchInternalDate})
	if err != nil {
		return nil, err
	}
	return File{FName: filename(msg), FType: "file", FSize: int64(msg.Size), FTime: msg.InternalDate.Unix()}, nil
}

func (this Imap) Cat(path string) (io.ReadCloser, error) {
	this.mu.Lock()
	defer this.mu.Unlock()
	mailbox, uid, isFile, err := this.parse(path)
	if err != nil {
		return nil, err
	} else if !isFile {
		return nil, ErrNotValid
	}
	section := &imap.BodySectionName{Peek: true} // don't mark as read
	msg, err := this.fetch(mailbox, uid, []imap.FetchItem{section.FetchItem()})
	if err != nil {
		return nil, err
	}
	body := msg.GetBody(section)
	if body == nil {
		return nil, ErrNotFound
	}
	return io.NopCloser(body), nil
}

func (this Imap) Mkdir(path string) error {
	this.mu.Lock()
	defer this.mu.Unlock()
	mailbox, _, isFile, err := this.parse(path)
	if err != nil || isFile || mailbox == "" {
		return ErrNotValid
	}
	if err := this.client.Create(mailbox); err != nil {
		return ErrFilesystemError
	}
	return nil
}

func (this Imap) Rm(path string) error {
	this.mu.Lock()
	defer this.mu.Unlock()
	mailbox, uid, isFile, err := this.parse(path)
	if err != nil {
		return err
	} else if mailbox == "" {
		return ErrNotValid
	}
	if !isFile {
		if err := this.client.Delete(mailbox); err != nil {
			return ErrFilesystemError
		}
		return nil
	}
	if _, err := this.client.Select(mailbox, false); err != nil {
		return ErrNotFound
	}
	seq := new(imap.SeqSet)
	seq.AddNum(uid)
	if err := this.client.UidStore(seq, imap.FormatFlagsOp(imap.AddFlags, true), []interface{}{imap.DeletedFlag}, nil); err != nil {
		return ErrFilesystemError
	}
	if err := this.client.Expunge(nil); err != nil {
		return ErrFilesystemError
	}
	return nil
}

func (this Imap) Mv(from, to string) error {
	this.mu.Lock()
	defer this.mu.Unlock()
	srcBox, uid, srcFile, err := this.parse(from)
	if err != nil {
		return err
	}
	if !srcFile {
		dstBox, _, _, err := this.parse(to)
		if err != nil || srcBox == "" || dstBox == "" {
			return ErrNotValid
		}
		if err := this.client.Rename(srcBox, dstBox); err != nil {
			return ErrFilesystemError
		}
		return nil
	}
	// an email can change folder but its name is derived from its content
	parts := strings.Split(strings.Trim(to, "/"), "/")
	dstBox := strings.Join(parts[:len(parts)-1], this.delim)
	if dstBox == "" || dstBox == srcBox {
		return NewError("Emails can be moved to another folder but can't be renamed", 400)
	}
	if _, err := this.client.Select(srcBox, false); err != nil {
		return ErrNotFound
	}
	seq := new(imap.SeqSet)
	seq.AddNum(uid)
	if err := this.client.UidMove(seq, dstBox); err == nil {
		return nil
	}
	// fallback for servers that advertise MOVE but don't implement it properly
	if err := this.client.UidCopy(seq, dstBox); err != nil {
		Log.Debug("plg_backend_imap::mv err=%s", err.Error())
		return ErrFilesystemError
	}
	if err := this.client.UidStore(seq, imap.FormatFlagsOp(imap.AddFlags, true), []interface{}{imap.DeletedFlag}, nil); err != nil {
		return ErrFilesystemError
	}
	if err := this.client.Expunge(nil); err != nil {
		return ErrFilesystemError
	}
	return nil
}

func (this Imap) Save(path string, file io.Reader) error {
	this.mu.Lock()
	defer this.mu.Unlock()
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) < 2 || !strings.HasSuffix(strings.ToLower(path), ".eml") {
		return NewError("Only .eml files can be saved inside a mailbox", 400)
	}
	buf := &bytes.Buffer{}
	if _, err := io.Copy(buf, io.LimitReader(file, 50<<20)); err != nil {
		return err
	}
	if err := this.client.Append(strings.Join(parts[:len(parts)-1], this.delim), nil, time.Now(), buf); err != nil {
		return ErrFilesystemError
	}
	return nil
}

func (this Imap) Touch(path string) error {
	return ErrNotSupported
}
