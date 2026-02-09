package smtp

import (
	"context"
	"fmt"
	"net/mail"
	gosmtp "net/smtp"
	"strconv"
	"strings"
	"time"

	"github.com/carlos/spinner/internal/actions/executor"
	"github.com/carlos/spinner/internal/store"
)

type Config struct {
	Host     string
	Port     int
	Username string
	Password string
	From     string
}

type sendMailFunc func(addr string, auth gosmtp.Auth, from string, to []string, msg []byte) error

type Plugin struct {
	cfg      Config
	sendMail sendMailFunc
}

func New(cfg Config) *Plugin {
	if cfg.Port < 1 {
		cfg.Port = 587
	}
	return &Plugin{
		cfg:      cfg,
		sendMail: gosmtp.SendMail,
	}
}

func (p *Plugin) PluginKey() string {
	return "smtp_email"
}

func (p *Plugin) ActionTypes() []string {
	return []string{"send_email", "smtp_email", "email"}
}

func (p *Plugin) Execute(ctx context.Context, approval store.ActionApproval) (executor.Result, error) {
	select {
	case <-ctx.Done():
		return executor.Result{}, ctx.Err()
	default:
	}
	if p == nil {
		return executor.Result{}, fmt.Errorf("smtp plugin not configured")
	}
	if p.sendMail == nil {
		p.sendMail = gosmtp.SendMail
	}
	host := strings.TrimSpace(p.cfg.Host)
	if host == "" {
		return executor.Result{}, fmt.Errorf("smtp host is not configured")
	}

	toRecipients, err := mergeRecipients(approval.ActionTarget, approval.Payload["to"])
	if err != nil {
		return executor.Result{}, err
	}
	ccRecipients, err := parseRecipients(approval.Payload["cc"])
	if err != nil {
		return executor.Result{}, err
	}
	bccRecipients, err := parseRecipients(approval.Payload["bcc"])
	if err != nil {
		return executor.Result{}, err
	}
	allRecipients := dedupeRecipients(append(append(toRecipients, ccRecipients...), bccRecipients...))
	if len(allRecipients) == 0 {
		return executor.Result{}, fmt.Errorf("smtp action requires recipient in target or payload.to")
	}
	if len(toRecipients) == 0 {
		toRecipients = append(toRecipients, allRecipients...)
	}

	fromHeader := getString(approval.Payload, "from")
	if fromHeader == "" {
		fromHeader = strings.TrimSpace(p.cfg.From)
	}
	if fromHeader == "" {
		return executor.Result{}, fmt.Errorf("smtp sender is not configured")
	}
	fromAddr, fromDisplay, err := parseSingleAddress(fromHeader)
	if err != nil {
		return executor.Result{}, fmt.Errorf("invalid sender: %w", err)
	}

	subject := getString(approval.Payload, "subject")
	if subject == "" {
		subject = strings.TrimSpace(approval.ActionSummary)
	}
	if subject == "" {
		subject = "Spinner notification"
	}
	textBody := getString(approval.Payload, "body")
	if textBody == "" {
		textBody = getString(approval.Payload, "text")
	}
	if textBody == "" {
		textBody = strings.TrimSpace(approval.ActionSummary)
	}
	htmlBody := getString(approval.Payload, "html")
	if strings.TrimSpace(textBody) == "" && strings.TrimSpace(htmlBody) == "" {
		return executor.Result{}, fmt.Errorf("smtp action requires body/text/html content")
	}

	headers := []string{
		"From: " + sanitizeHeader(fromDisplay),
		"To: " + sanitizeHeader(strings.Join(toRecipients, ", ")),
		"Subject: " + sanitizeHeader(subject),
		"Date: " + time.Now().UTC().Format(time.RFC1123Z),
		"MIME-Version: 1.0",
	}
	if len(ccRecipients) > 0 {
		headers = append(headers, "Cc: "+sanitizeHeader(strings.Join(ccRecipients, ", ")))
	}

	body := textBody
	if strings.TrimSpace(htmlBody) != "" {
		headers = append(headers, "Content-Type: text/html; charset=UTF-8")
		body = htmlBody
	} else {
		headers = append(headers, "Content-Type: text/plain; charset=UTF-8")
	}
	message := strings.Join(headers, "\r\n") + "\r\n\r\n" + normalizeBody(body)

	var auth gosmtp.Auth
	if strings.TrimSpace(p.cfg.Username) != "" {
		if strings.TrimSpace(p.cfg.Password) == "" {
			return executor.Result{}, fmt.Errorf("smtp password is required when username is set")
		}
		auth = gosmtp.PlainAuth("", p.cfg.Username, p.cfg.Password, host)
	}
	serverAddress := host + ":" + strconv.Itoa(p.cfg.Port)
	if err := p.sendMail(serverAddress, auth, fromAddr, allRecipients, []byte(message)); err != nil {
		return executor.Result{}, err
	}

	return executor.Result{
		Plugin:  p.PluginKey(),
		Message: fmt.Sprintf("email sent to %d recipient(s)", len(allRecipients)),
	}, nil
}

func mergeRecipients(target string, payloadTo any) ([]string, error) {
	targetRecipients, err := parseRecipients(target)
	if err != nil {
		return nil, err
	}
	payloadRecipients, err := parseRecipients(payloadTo)
	if err != nil {
		return nil, err
	}
	return dedupeRecipients(append(targetRecipients, payloadRecipients...)), nil
}

func parseRecipients(value any) ([]string, error) {
	switch casted := value.(type) {
	case nil:
		return nil, nil
	case string:
		if strings.TrimSpace(casted) == "" {
			return nil, nil
		}
		parts := strings.Split(casted, ",")
		return parseRecipientSlice(parts)
	case []string:
		generic := make([]string, 0, len(casted))
		for _, item := range casted {
			generic = append(generic, item)
		}
		return parseRecipientSlice(generic)
	case []any:
		generic := make([]string, 0, len(casted))
		for _, raw := range casted {
			generic = append(generic, fmt.Sprintf("%v", raw))
		}
		return parseRecipientSlice(generic)
	default:
		return parseRecipients(fmt.Sprintf("%v", value))
	}
}

func parseRecipientSlice(values []string) ([]string, error) {
	recipients := make([]string, 0, len(values))
	for _, raw := range values {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			continue
		}
		address, display, err := parseSingleAddress(trimmed)
		if err != nil {
			return nil, fmt.Errorf("invalid recipient %q: %w", trimmed, err)
		}
		if display != "" {
			recipients = append(recipients, display)
			continue
		}
		recipients = append(recipients, address)
	}
	return recipients, nil
}

func parseSingleAddress(value string) (address string, display string, err error) {
	parsed, err := mail.ParseAddress(strings.TrimSpace(value))
	if err != nil {
		return "", "", err
	}
	if parsed.Name != "" {
		return parsed.Address, parsed.String(), nil
	}
	return parsed.Address, parsed.Address, nil
}

func dedupeRecipients(values []string) []string {
	seen := map[string]struct{}{}
	results := make([]string, 0, len(values))
	for _, value := range values {
		addr, _, err := parseSingleAddress(value)
		if err != nil {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(addr))
		if key == "" {
			continue
		}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		results = append(results, addr)
	}
	return results
}

func getString(payload map[string]any, key string) string {
	if payload == nil {
		return ""
	}
	value, ok := payload[key]
	if !ok || value == nil {
		return ""
	}
	switch casted := value.(type) {
	case string:
		return strings.TrimSpace(casted)
	default:
		return strings.TrimSpace(fmt.Sprintf("%v", value))
	}
}

func sanitizeHeader(value string) string {
	replacer := strings.NewReplacer("\r", " ", "\n", " ")
	return strings.TrimSpace(replacer.Replace(value))
}

func normalizeBody(value string) string {
	text := strings.ReplaceAll(value, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	return strings.ReplaceAll(text, "\n", "\r\n")
}
