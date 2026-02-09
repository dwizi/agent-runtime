package tui

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/carlos/spinner/internal/adminclient"
	"github.com/carlos/spinner/internal/config"
	"github.com/carlos/spinner/internal/envsync"
)

type model struct {
	cfg         config.Config
	logger      *slog.Logger
	client      *adminclient.Client
	quitting    bool
	loading     bool
	tokenInput  string
	statusText  string
	errorText   string
	activePair  *adminclient.Pairing
	approvedMsg *adminclient.ApprovePairingResponse
	startupInfo string
}

func Run(cfg config.Config, logger *slog.Logger) error {
	updatedCfg, startupInfo := syncEnvAtStartup(cfg, logger)

	client, err := adminclient.New(updatedCfg)
	if err != nil {
		return err
	}
	program := tea.NewProgram(model{
		cfg:         updatedCfg,
		logger:      logger,
		client:      client,
		startupInfo: startupInfo,
	})
	_, err = program.Run()
	return err
}

func (m model) Init() tea.Cmd {
	return nil
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch typed := msg.(type) {
	case lookupPairingDoneMsg:
		m.loading = false
		if typed.err != nil {
			m.errorText = typed.err.Error()
			m.statusText = ""
			m.activePair = nil
			m.approvedMsg = nil
			return m, nil
		}
		m.errorText = ""
		m.statusText = "token loaded"
		m.activePair = &typed.pairing
		m.approvedMsg = nil
		return m, nil
	case approvePairingDoneMsg:
		m.loading = false
		if typed.err != nil {
			m.errorText = typed.err.Error()
			m.statusText = ""
			return m, nil
		}
		m.errorText = ""
		m.statusText = "pairing approved"
		m.approvedMsg = &typed.response
		return m, nil
	case denyPairingDoneMsg:
		m.loading = false
		if typed.err != nil {
			m.errorText = typed.err.Error()
			m.statusText = ""
			return m, nil
		}
		m.errorText = ""
		m.statusText = "pairing denied"
		m.activePair = &typed.pairing
		m.approvedMsg = nil
		return m, nil
	}

	switch typed := msg.(type) {
	case tea.KeyMsg:
		switch typed.String() {
		case "q", "ctrl+c":
			m.quitting = true
			return m, tea.Quit
		}

		if m.loading {
			return m, nil
		}

		if m.activePair == nil {
			switch typed.String() {
			case "enter":
				token := strings.ToUpper(strings.TrimSpace(m.tokenInput))
				if token == "" {
					return m, nil
				}
				m.loading = true
				m.statusText = "loading token..."
				m.errorText = ""
				return m, m.lookupPairingCmd(token)
			case "backspace":
				if len(m.tokenInput) > 0 {
					m.tokenInput = m.tokenInput[:len(m.tokenInput)-1]
				}
				return m, nil
			}
			if typed.Type == tea.KeyRunes {
				for _, char := range typed.Runes {
					if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || char == '-' {
						m.tokenInput += strings.ToUpper(string(char))
					}
				}
			}
			return m, nil
		}

		switch typed.String() {
		case "n":
			m.activePair = nil
			m.approvedMsg = nil
			m.errorText = ""
			m.statusText = ""
			m.tokenInput = ""
			return m, nil
		case "a":
			m.loading = true
			m.statusText = "approving pairing..."
			m.errorText = ""
			return m, m.approvePairingCmd(strings.ToUpper(strings.TrimSpace(m.tokenInput)))
		case "d":
			m.loading = true
			m.statusText = "denying pairing..."
			m.errorText = ""
			return m, m.denyPairingCmd(strings.ToUpper(strings.TrimSpace(m.tokenInput)))
		}
	}

	return m, nil
}

func (m model) View() string {
	if m.quitting {
		return "spinner tui closed\n"
	}

	title := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("86")).Render("spinner admin tui")
	highlight := lipgloss.NewStyle().Foreground(lipgloss.Color("120"))
	errorStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
	warnStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("214"))

	bodyLines := []string{
		"",
		"Security-first orchestration control plane",
		fmt.Sprintf("Environment: %s", m.cfg.Environment),
		fmt.Sprintf("Admin API: %s", m.cfg.AdminAPIURL),
		fmt.Sprintf("Approver: %s (%s)", m.cfg.TUIApproverUserID, m.cfg.TUIApprovalRole),
		"",
	}
	if strings.TrimSpace(m.startupInfo) != "" {
		bodyLines = append(bodyLines, warnStyle.Render(m.startupInfo), "")
	}

	if m.activePair == nil {
		bodyLines = append(bodyLines,
			"Paste one-time token from Telegram/Discord DM and press Enter:",
			highlight.Render(m.tokenInput),
			"",
			"Controls: Enter=lookup, Backspace=edit, q=quit",
		)
	} else {
		bodyLines = append(bodyLines,
			"Pending pairing request:",
			fmt.Sprintf("- Connector: %s", m.activePair.Connector),
			fmt.Sprintf("- Connector User ID: %s", m.activePair.ConnectorUserID),
			fmt.Sprintf("- Display Name: %s", m.activePair.DisplayName),
			fmt.Sprintf("- Status: %s", m.activePair.Status),
			fmt.Sprintf("- Expires At: %s", time.Unix(m.activePair.ExpiresAtUnix, 0).UTC().Format(time.RFC3339)),
			"",
			"Controls: a=approve, d=deny, n=new token, q=quit",
		)
	}

	if m.approvedMsg != nil {
		bodyLines = append(bodyLines,
			"",
			highlight.Render("Approval completed"),
			fmt.Sprintf("- User ID: %s", m.approvedMsg.ApprovedUserID),
			fmt.Sprintf("- Identity ID: %s", m.approvedMsg.IdentityID),
		)
	}
	if strings.TrimSpace(m.statusText) != "" {
		bodyLines = append(bodyLines, "", warnStyle.Render(m.statusText))
	}
	if strings.TrimSpace(m.errorText) != "" {
		bodyLines = append(bodyLines, "", errorStyle.Render(m.errorText))
	}

	return title + "\n" + strings.Join(bodyLines, "\n") + "\n"
}

type lookupPairingDoneMsg struct {
	pairing adminclient.Pairing
	err     error
}

type approvePairingDoneMsg struct {
	response adminclient.ApprovePairingResponse
	err      error
}

type denyPairingDoneMsg struct {
	pairing adminclient.Pairing
	err     error
}

func (m model) lookupPairingCmd(token string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		pairing, err := m.client.LookupPairing(ctx, token)
		return lookupPairingDoneMsg{pairing: pairing, err: err}
	}
}

func (m model) approvePairingCmd(token string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		response, err := m.client.ApprovePairing(ctx, token, m.cfg.TUIApproverUserID, m.cfg.TUIApprovalRole, "")
		return approvePairingDoneMsg{response: response, err: err}
	}
}

func (m model) denyPairingCmd(token string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		pairing, err := m.client.DenyPairing(ctx, token, m.cfg.TUIApproverUserID, "denied by admin")
		return denyPairingDoneMsg{pairing: pairing, err: err}
	}
}

func syncEnvAtStartup(cfg config.Config, logger *slog.Logger) (config.Config, string) {
	result, err := envsync.SyncLocalPKIEnv()
	if err != nil {
		logger.Warn("tui env sync failed", "error", err)
		return cfg, "startup env sync failed"
	}

	infoParts := []string{}
	if result.Skipped {
		if strings.TrimSpace(result.Reason) != "" {
			infoParts = append(infoParts, result.Reason)
		}
	} else if len(result.UpdatedKeys) > 0 {
		infoParts = append(infoParts, "synced local mTLS paths to .env")
		logger.Info("tui env sync updated keys", "keys", strings.Join(result.UpdatedKeys, ","))
		if strings.TrimSpace(result.BackupPath) != "" {
			logger.Info("tui env sync created backup", "path", result.BackupPath)
		}
	}

	if cfg.AdminTLSCAFile == "" {
		path := filepathOrEmpty(result.PKIDir, "clients-ca.crt")
		if path != "" {
			cfg.AdminTLSCAFile = path
			_ = os.Setenv("SPINNER_ADMIN_TLS_CA_FILE", path)
		}
	}
	if cfg.AdminTLSCertFile == "" {
		path := filepathOrEmpty(result.PKIDir, "admin-client.crt")
		if path != "" {
			cfg.AdminTLSCertFile = path
			_ = os.Setenv("SPINNER_ADMIN_TLS_CERT_FILE", path)
		}
	}
	if cfg.AdminTLSKeyFile == "" {
		path := filepathOrEmpty(result.PKIDir, "admin-client.key")
		if path != "" {
			cfg.AdminTLSKeyFile = path
			_ = os.Setenv("SPINNER_ADMIN_TLS_KEY_FILE", path)
		}
	}

	cfg, recoveredInfo := recoverInvalidTLSConfig(cfg, result.PKIDir, logger)
	if strings.TrimSpace(recoveredInfo) != "" {
		infoParts = append(infoParts, recoveredInfo)
	}
	return cfg, strings.Join(infoParts, "; ")
}

func filepathOrEmpty(dir, file string) string {
	if strings.TrimSpace(dir) == "" || strings.TrimSpace(file) == "" {
		return ""
	}
	path := filepath.Join(dir, file)
	if _, err := os.Stat(path); err != nil {
		return ""
	}
	return path
}

func recoverInvalidTLSConfig(cfg config.Config, pkiDir string, logger *slog.Logger) (config.Config, string) {
	info := ""

	if strings.TrimSpace(cfg.AdminTLSCAFile) != "" && !validCACert(cfg.AdminTLSCAFile) {
		logger.Warn("invalid admin ca file configured, clearing for tui session", "path", cfg.AdminTLSCAFile)
		cfg.AdminTLSCAFile = ""
		_ = os.Setenv("SPINNER_ADMIN_TLS_CA_FILE", "")
		info = "ignored invalid CA path in environment"
	}

	certPath := strings.TrimSpace(cfg.AdminTLSCertFile)
	keyPath := strings.TrimSpace(cfg.AdminTLSKeyFile)
	if certPath == "" && keyPath == "" {
		return cfg, info
	}

	if certPath == "" || keyPath == "" {
		logger.Warn("incomplete admin client cert configuration, clearing for tui session")
		cfg.AdminTLSCertFile = ""
		cfg.AdminTLSKeyFile = ""
		_ = os.Setenv("SPINNER_ADMIN_TLS_CERT_FILE", "")
		_ = os.Setenv("SPINNER_ADMIN_TLS_KEY_FILE", "")
		if info == "" {
			info = "ignored incomplete client cert config"
		}
		return cfg, info
	}

	if _, err := tls.LoadX509KeyPair(certPath, keyPath); err == nil {
		return cfg, info
	}

	fallbackCert := filepathOrEmpty(pkiDir, "admin-client.crt")
	fallbackKey := filepathOrEmpty(pkiDir, "admin-client.key")
	if fallbackCert != "" && fallbackKey != "" {
		if _, err := tls.LoadX509KeyPair(fallbackCert, fallbackKey); err == nil {
			cfg.AdminTLSCertFile = fallbackCert
			cfg.AdminTLSKeyFile = fallbackKey
			_ = os.Setenv("SPINNER_ADMIN_TLS_CERT_FILE", fallbackCert)
			_ = os.Setenv("SPINNER_ADMIN_TLS_KEY_FILE", fallbackKey)
			if info == "" {
				info = "recovered client cert path from local caddy pki"
			}
			return cfg, info
		}
	}

	logger.Warn("invalid admin client cert configuration, continuing without client cert for tui")
	cfg.AdminTLSCertFile = ""
	cfg.AdminTLSKeyFile = ""
	_ = os.Setenv("SPINNER_ADMIN_TLS_CERT_FILE", "")
	_ = os.Setenv("SPINNER_ADMIN_TLS_KEY_FILE", "")
	if info == "" {
		info = "ignored invalid client cert config"
	}
	return cfg, info
}

func validCACert(path string) bool {
	content, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	pool := x509.NewCertPool()
	return pool.AppendCertsFromPEM(content)
}
