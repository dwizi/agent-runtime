package imap

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"log/slog"
	"net/mail"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/emersion/go-imap"
	"github.com/emersion/go-imap/client"

	"github.com/carlos/spinner/internal/orchestrator"
	"github.com/carlos/spinner/internal/store"
)

type Store interface {
	EnsureContextForExternalChannel(ctx context.Context, connector, externalID, displayName string) (store.ContextRecord, error)
	CreateTask(ctx context.Context, input store.CreateTaskInput) error
}

type Engine interface {
	Enqueue(task orchestrator.Task) (orchestrator.Task, error)
}

type Message struct {
	UID       uint32
	MessageID string
	From      string
	Subject   string
	Date      time.Time
	Body      string
}

type Connector struct {
	host          string
	port          int
	username      string
	password      string
	mailbox       string
	pollSeconds   int
	workspaceRoot string
	tlsSkipVerify bool
	store         Store
	engine        Engine
	logger        *slog.Logger
	fetchUnread   func(ctx context.Context) ([]Message, error)
	markSeen      func(ctx context.Context, uids []uint32) error
}

func New(host string, port int, username, password, mailbox string, pollSeconds int, workspaceRoot string, tlsSkipVerify bool, store Store, engine Engine, logger *slog.Logger) *Connector {
	if port < 1 {
		port = 993
	}
	if strings.TrimSpace(mailbox) == "" {
		mailbox = "INBOX"
	}
	if pollSeconds < 1 {
		pollSeconds = 60
	}
	c := &Connector{
		host:          strings.TrimSpace(host),
		port:          port,
		username:      strings.TrimSpace(username),
		password:      password,
		mailbox:       strings.TrimSpace(mailbox),
		pollSeconds:   pollSeconds,
		workspaceRoot: strings.TrimSpace(workspaceRoot),
		tlsSkipVerify: tlsSkipVerify,
		store:         store,
		engine:        engine,
		logger:        logger,
	}
	c.fetchUnread = c.fetchUnreadFromIMAP
	c.markSeen = c.markSeenInIMAP
	return c
}

func (c *Connector) Name() string {
	return "imap"
}

func (c *Connector) Start(ctx context.Context) error {
	if c.host == "" || c.username == "" || c.password == "" {
		c.logger.Info("connector disabled, imap credentials missing")
		<-ctx.Done()
		return nil
	}
	if c.store == nil {
		c.logger.Info("connector disabled, store missing")
		<-ctx.Done()
		return nil
	}
	if c.fetchUnread == nil || c.markSeen == nil {
		c.logger.Info("connector disabled, imap handlers missing")
		<-ctx.Done()
		return nil
	}
	c.logger.Info("connector started", "mailbox", c.mailbox, "host", c.host, "poll_seconds", c.pollSeconds)

	for {
		if ctx.Err() != nil {
			c.logger.Info("connector stopped")
			return nil
		}
		if err := c.pollOnce(ctx); err != nil && ctx.Err() == nil {
			c.logger.Error("imap poll failed", "error", err)
		}
		select {
		case <-ctx.Done():
			c.logger.Info("connector stopped")
			return nil
		case <-time.After(time.Duration(c.pollSeconds) * time.Second):
		}
	}
}

func (c *Connector) pollOnce(ctx context.Context) error {
	incoming, err := c.fetchUnread(ctx)
	if err != nil {
		return err
	}
	if len(incoming) == 0 {
		return nil
	}
	contextRecord, err := c.store.EnsureContextForExternalChannel(ctx, "imap", c.externalID(), c.mailbox)
	if err != nil {
		return err
	}

	processedUIDs := make([]uint32, 0, len(incoming))
	for _, item := range incoming {
		targetPath, relativePath, writeErr := c.writeMessageMarkdown(contextRecord.WorkspaceID, item)
		if writeErr != nil {
			c.logger.Error("imap message write failed", "error", writeErr, "uid", item.UID)
			continue
		}
		c.queueMessageTask(ctx, contextRecord, item, relativePath)
		c.logger.Info("imap message ingested", "uid", item.UID, "path", targetPath)
		if item.UID > 0 {
			processedUIDs = append(processedUIDs, item.UID)
		}
	}

	if len(processedUIDs) > 0 {
		if err := c.markSeen(ctx, processedUIDs); err != nil {
			c.logger.Error("imap mark seen failed", "error", err)
		}
	}
	return nil
}

func (c *Connector) queueMessageTask(ctx context.Context, contextRecord store.ContextRecord, msg Message, relativePath string) {
	if c.engine == nil {
		return
	}
	subject := strings.TrimSpace(msg.Subject)
	if subject == "" {
		subject = "No subject"
	}
	title := "Review email: " + subject
	if len(title) > 72 {
		title = title[:72]
	}
	prompt := fmt.Sprintf("A new email was ingested from `%s` with subject `%s`. Review file `%s` and decide next actions.", fallbackString(msg.From, "unknown sender"), subject, relativePath)
	task, err := c.engine.Enqueue(orchestrator.Task{
		WorkspaceID: contextRecord.WorkspaceID,
		ContextID:   contextRecord.ID,
		Kind:        orchestrator.TaskKindGeneral,
		Title:       title,
		Prompt:      prompt,
	})
	if err != nil {
		c.logger.Error("enqueue imap task failed", "error", err, "subject", subject)
		return
	}
	if err := c.store.CreateTask(ctx, store.CreateTaskInput{
		ID:          task.ID,
		WorkspaceID: task.WorkspaceID,
		ContextID:   task.ContextID,
		Kind:        string(task.Kind),
		Title:       task.Title,
		Prompt:      task.Prompt,
		Status:      "queued",
	}); err != nil {
		c.logger.Error("persist imap task failed", "error", err, "task_id", task.ID)
	}
}

func (c *Connector) writeMessageMarkdown(workspaceID string, msg Message) (absolutePath string, relativePath string, err error) {
	basePath := filepath.Join(c.workspaceRoot, workspaceID)
	targetDir := filepath.Join(basePath, "inbox", "imap", sanitizeSegment(c.mailbox))
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return "", "", err
	}

	filename := fmt.Sprintf("%d-%s.md", msg.UID, sanitizeFilename(fallbackString(msg.Subject, "message")))
	if msg.UID == 0 {
		filename = fmt.Sprintf("%d-%s.md", time.Now().UTC().Unix(), sanitizeFilename(fallbackString(msg.Subject, "message")))
	}
	targetPath := filepath.Join(targetDir, filename)
	content := buildMarkdownMessage(msg)
	if err := os.WriteFile(targetPath, []byte(content), 0o644); err != nil {
		return "", "", err
	}
	relPath, relErr := filepath.Rel(basePath, targetPath)
	if relErr != nil {
		relPath = filepath.ToSlash(filename)
	}
	return targetPath, filepath.ToSlash(relPath), nil
}

func buildMarkdownMessage(msg Message) string {
	parts := []string{
		"# Email",
		"",
		"- uid: `" + strconv.FormatUint(uint64(msg.UID), 10) + "`",
		"- message_id: `" + strings.TrimSpace(msg.MessageID) + "`",
		"- from: `" + fallbackString(msg.From, "unknown") + "`",
		"- subject: `" + fallbackString(msg.Subject, "(no subject)") + "`",
	}
	if !msg.Date.IsZero() {
		parts = append(parts, "- date: `"+msg.Date.UTC().Format(time.RFC3339)+"`")
	}
	parts = append(parts, "", "## Body", "", strings.TrimSpace(msg.Body))
	return strings.Join(parts, "\n") + "\n"
}

func (c *Connector) fetchUnreadFromIMAP(ctx context.Context) ([]Message, error) {
	clientInstance, err := c.openClient(ctx)
	if err != nil {
		return nil, err
	}
	defer clientInstance.Logout()
	return c.fetchUnreadWithClient(clientInstance)
}

func (c *Connector) markSeenInIMAP(ctx context.Context, uids []uint32) error {
	if len(uids) == 0 {
		return nil
	}
	clientInstance, err := c.openClient(ctx)
	if err != nil {
		return err
	}
	defer clientInstance.Logout()

	if _, err := clientInstance.Select(c.mailbox, false); err != nil {
		return fmt.Errorf("imap select mailbox: %w", err)
	}
	set := new(imap.SeqSet)
	set.AddNum(uids...)
	item := imap.FormatFlagsOp(imap.AddFlags, true)
	if err := clientInstance.UidStore(set, item, []interface{}{imap.SeenFlag}, nil); err != nil {
		return fmt.Errorf("imap mark seen: %w", err)
	}
	return nil
}

func (c *Connector) openClient(ctx context.Context) (*client.Client, error) {
	address := netAddress(c.host, c.port)
	tlsConfig := &tls.Config{
		ServerName:         c.host,
		InsecureSkipVerify: c.tlsSkipVerify,
		MinVersion:         tls.VersionTLS12,
	}
	clientInstance, err := client.DialTLS(address, tlsConfig)
	if err != nil {
		return nil, fmt.Errorf("imap dial: %w", err)
	}
	select {
	case <-ctx.Done():
		clientInstance.Logout()
		return nil, ctx.Err()
	default:
	}
	if err := clientInstance.Login(c.username, c.password); err != nil {
		clientInstance.Logout()
		return nil, fmt.Errorf("imap login: %w", err)
	}
	return clientInstance, nil
}

func (c *Connector) fetchUnreadWithClient(clientInstance *client.Client) ([]Message, error) {
	_, err := clientInstance.Select(c.mailbox, false)
	if err != nil {
		return nil, fmt.Errorf("imap select mailbox: %w", err)
	}
	criteria := imap.NewSearchCriteria()
	criteria.WithoutFlags = []string{imap.SeenFlag}
	uids, err := clientInstance.UidSearch(criteria)
	if err != nil {
		return nil, fmt.Errorf("imap search unread: %w", err)
	}
	if len(uids) == 0 {
		return nil, nil
	}

	set := new(imap.SeqSet)
	set.AddNum(uids...)
	section := &imap.BodySectionName{}
	items := []imap.FetchItem{
		imap.FetchUid,
		imap.FetchEnvelope,
		imap.FetchRFC822Size,
		section.FetchItem(),
	}
	messages := make(chan *imap.Message, len(uids))
	done := make(chan error, 1)
	go func() {
		done <- clientInstance.UidFetch(set, items, messages)
	}()

	results := make([]Message, 0, len(uids))
	for fetched := range messages {
		bodyReader := fetched.GetBody(section)
		if bodyReader == nil {
			continue
		}
		bodyBytes, readErr := ioReadAllLimited(bodyReader, 2<<20)
		if readErr != nil {
			continue
		}
		parsedBody := decodeMessageBody(bodyBytes)
		item := Message{
			UID:  fetched.Uid,
			Body: parsedBody,
		}
		if fetched.Envelope != nil {
			item.Subject = strings.TrimSpace(fetched.Envelope.Subject)
			item.Date = fetched.Envelope.Date
			item.MessageID = strings.TrimSpace(fetched.Envelope.MessageId)
			item.From = formatAddresses(fetched.Envelope.From)
		}
		results = append(results, item)
	}
	if err := <-done; err != nil {
		return nil, fmt.Errorf("imap fetch unread: %w", err)
	}
	return results, nil
}

func decodeMessageBody(raw []byte) string {
	if len(raw) == 0 {
		return ""
	}
	parsed, err := mail.ReadMessage(strings.NewReader(string(raw)))
	if err != nil {
		return strings.TrimSpace(string(raw))
	}
	body, err := ioReadAllLimited(parsed.Body, 2<<20)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(body))
}

func formatAddresses(items []*imap.Address) string {
	if len(items) == 0 {
		return ""
	}
	parts := make([]string, 0, len(items))
	for _, item := range items {
		if item == nil {
			continue
		}
		address := strings.TrimSpace(item.MailboxName + "@" + item.HostName)
		name := strings.TrimSpace(item.PersonalName)
		if name != "" {
			parts = append(parts, name+" <"+address+">")
			continue
		}
		parts = append(parts, address)
	}
	return strings.Join(parts, ", ")
}

func (c *Connector) externalID() string {
	return strings.TrimSpace(c.username + ":" + c.mailbox)
}

func netAddress(host string, port int) string {
	return strings.TrimSpace(host) + ":" + strconv.Itoa(port)
}

var filenameSanitizer = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

func sanitizeFilename(input string) string {
	value := strings.TrimSpace(input)
	value = filenameSanitizer.ReplaceAllString(value, "-")
	value = strings.Trim(value, "-.")
	if value == "" {
		return "message"
	}
	return value
}

func sanitizeSegment(input string) string {
	value := sanitizeFilename(input)
	return strings.ReplaceAll(value, ".", "_")
}

func fallbackString(value, fallback string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return fallback
	}
	return trimmed
}

func ioReadAllLimited(reader io.Reader, maxBytes int64) ([]byte, error) {
	limited := &io.LimitedReader{R: reader, N: maxBytes + 1}
	data, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxBytes {
		return nil, fmt.Errorf("content exceeds max size")
	}
	return data, nil
}
