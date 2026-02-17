package cli

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/dwizi/agent-runtime/internal/adminclient"
	"github.com/dwizi/agent-runtime/internal/config"
)

func newChatPairingCommand(logger *slog.Logger) *cobra.Command {
	_ = logger
	var (
		connector  string
		externalID string
		fromUserID string
		display    string
		timeoutSec int
	)

	cmd := &cobra.Command{
		Use:     "pairing",
		Aliases: []string{"pair"},
		Short:   "Manage channel identity pairings via admin API",
	}

	cmd.PersistentFlags().StringVar(&connector, "connector", "codex", "connector for pairing operations")
	cmd.PersistentFlags().StringVar(&externalID, "external-id", "codex-cli", "external channel/session id")
	cmd.PersistentFlags().StringVar(&fromUserID, "from-user-id", "", "origin user id (defaults to external-id)")
	cmd.PersistentFlags().StringVar(&display, "display-name", "Codex CLI", "display name for pairing records")
	cmd.PersistentFlags().IntVar(&timeoutSec, "timeout-sec", 120, "request timeout in seconds")

	cmd.AddCommand(newPairingStartCommand(&connector, &externalID, &fromUserID, &display, &timeoutSec))
	cmd.AddCommand(newPairingLookupCommand(&timeoutSec))
	cmd.AddCommand(newPairingApproveCommand(&timeoutSec))
	cmd.AddCommand(newPairingDenyCommand(&timeoutSec))
	cmd.AddCommand(newPairingAdminBootstrapCommand(&connector, &externalID, &fromUserID, &display, &timeoutSec))

	return cmd
}

func newPairingStartCommand(connector, externalID, fromUserID, display *string, timeoutSec *int) *cobra.Command {
	var expiresInSec int
	cmd := &cobra.Command{
		Use:   "start",
		Short: "Create a one-time pairing token for the current channel identity",
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newAdminClientFromEnv(*timeoutSec)
			if err != nil {
				return err
			}
			resolvedConnector, _, resolvedFromUserID, resolvedDisplay := resolveChatIdentity(*connector, *externalID, *fromUserID, *display)
			ctx, cancel := context.WithTimeout(context.Background(), boundedTimeout(*timeoutSec))
			defer cancel()

			response, err := client.StartPairing(ctx, resolvedConnector, resolvedFromUserID, resolvedDisplay, expiresInSec)
			if err != nil {
				return err
			}

			cmd.Printf("Pairing started: %s\n", response.ID)
			cmd.Printf("Token: %s\n", response.Token)
			cmd.Printf("Token hint: %s\n", response.TokenHint)
			cmd.Printf("Connector: %s\n", response.Connector)
			cmd.Printf("Connector user id: %s\n", response.ConnectorUserID)
			cmd.Printf("Display name: %s\n", response.DisplayName)
			cmd.Printf("Status: %s\n", response.Status)
			cmd.Printf("Expires at: %s\n", time.Unix(response.ExpiresAtUnix, 0).UTC().Format(time.RFC3339))
			return nil
		},
	}
	cmd.Flags().IntVar(&expiresInSec, "expires-in-sec", 600, "token TTL in seconds")
	return cmd
}

func newPairingLookupCommand(timeoutSec *int) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "lookup <token>",
		Short: "Inspect pairing status by token",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newAdminClientFromEnv(*timeoutSec)
			if err != nil {
				return err
			}
			token := strings.TrimSpace(args[0])
			ctx, cancel := context.WithTimeout(context.Background(), boundedTimeout(*timeoutSec))
			defer cancel()

			pairing, err := client.LookupPairing(ctx, token)
			if err != nil {
				return err
			}

			cmd.Printf("Pairing: %s\n", pairing.ID)
			cmd.Printf("Token hint: %s\n", pairing.TokenHint)
			cmd.Printf("Connector: %s\n", pairing.Connector)
			cmd.Printf("Connector user id: %s\n", pairing.ConnectorUserID)
			cmd.Printf("Display name: %s\n", pairing.DisplayName)
			cmd.Printf("Status: %s\n", pairing.Status)
			cmd.Printf("Expires at: %s\n", time.Unix(pairing.ExpiresAtUnix, 0).UTC().Format(time.RFC3339))
			if strings.TrimSpace(pairing.ApprovedUserID) != "" {
				cmd.Printf("Approved user id: %s\n", pairing.ApprovedUserID)
			}
			if strings.TrimSpace(pairing.ApproverUserID) != "" {
				cmd.Printf("Approver user id: %s\n", pairing.ApproverUserID)
			}
			if strings.TrimSpace(pairing.DeniedReason) != "" {
				cmd.Printf("Denied reason: %s\n", pairing.DeniedReason)
			}
			return nil
		},
	}
	return cmd
}

func newPairingApproveCommand(timeoutSec *int) *cobra.Command {
	var (
		approverUserID string
		role           string
		targetUserID   string
	)
	cmd := &cobra.Command{
		Use:   "approve <token>",
		Short: "Approve a pending pairing token",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newAdminClientFromEnv(*timeoutSec)
			if err != nil {
				return err
			}
			if strings.TrimSpace(approverUserID) == "" {
				return fmt.Errorf("--approver-user-id is required")
			}
			token := strings.TrimSpace(args[0])
			ctx, cancel := context.WithTimeout(context.Background(), boundedTimeout(*timeoutSec))
			defer cancel()

			response, err := client.ApprovePairing(ctx, token, approverUserID, role, targetUserID)
			if err != nil {
				return err
			}

			cmd.Printf("Pairing approved: %s\n", response.ID)
			cmd.Printf("Status: %s\n", response.Status)
			cmd.Printf("Approved user id: %s\n", response.ApprovedUserID)
			cmd.Printf("Approver user id: %s\n", response.ApproverUserID)
			cmd.Printf("Identity id: %s\n", response.IdentityID)
			cmd.Printf("Connector: %s\n", response.Connector)
			cmd.Printf("Connector user id: %s\n", response.ConnectorUserID)
			return nil
		},
	}
	cmd.Flags().StringVar(&approverUserID, "approver-user-id", "", "admin user id performing approval")
	cmd.Flags().StringVar(&role, "role", "admin", "role to assign when creating a new user")
	cmd.Flags().StringVar(&targetUserID, "target-user-id", "", "optional existing user id to link")
	return cmd
}

func newPairingDenyCommand(timeoutSec *int) *cobra.Command {
	var (
		approverUserID string
		reason         string
	)
	cmd := &cobra.Command{
		Use:   "deny <token>",
		Short: "Deny a pending pairing token",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newAdminClientFromEnv(*timeoutSec)
			if err != nil {
				return err
			}
			if strings.TrimSpace(approverUserID) == "" {
				return fmt.Errorf("--approver-user-id is required")
			}
			if strings.TrimSpace(reason) == "" {
				return fmt.Errorf("--reason is required")
			}
			token := strings.TrimSpace(args[0])
			ctx, cancel := context.WithTimeout(context.Background(), boundedTimeout(*timeoutSec))
			defer cancel()

			response, err := client.DenyPairing(ctx, token, approverUserID, reason)
			if err != nil {
				return err
			}

			cmd.Printf("Pairing denied: %s\n", response.ID)
			cmd.Printf("Status: %s\n", response.Status)
			cmd.Printf("Approver user id: %s\n", response.ApproverUserID)
			cmd.Printf("Reason: %s\n", response.DeniedReason)
			return nil
		},
	}
	cmd.Flags().StringVar(&approverUserID, "approver-user-id", "", "admin user id performing denial")
	cmd.Flags().StringVar(&reason, "reason", "", "denial reason")
	return cmd
}

func newPairingAdminBootstrapCommand(connector, externalID, fromUserID, display *string, timeoutSec *int) *cobra.Command {
	var (
		approverUserID string
		role           string
		targetUserID   string
		expiresInSec   int
	)
	cmd := &cobra.Command{
		Use:   "pair-admin",
		Short: "Bootstrap admin pairing for the active CLI channel identity",
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newAdminClientFromEnv(*timeoutSec)
			if err != nil {
				return err
			}

			resolvedConnector, resolvedExternalID, resolvedFromUserID, resolvedDisplay := resolveChatIdentity(*connector, *externalID, *fromUserID, *display)
			resolvedApprover := strings.TrimSpace(approverUserID)
			if resolvedApprover == "" {
				resolvedApprover = resolvedFromUserID
			}

			ctx, cancel := context.WithTimeout(context.Background(), boundedTimeout(*timeoutSec))
			defer cancel()

			startResponse, err := client.StartPairing(ctx, resolvedConnector, resolvedFromUserID, resolvedDisplay, expiresInSec)
			if err != nil {
				return err
			}

			approveResponse, err := client.ApprovePairing(ctx, startResponse.Token, resolvedApprover, role, targetUserID)
			if err != nil {
				return err
			}

			cmd.Printf("Admin pairing ready for %s/%s\n", resolvedConnector, resolvedExternalID)
			cmd.Printf("Connector user id: %s\n", resolvedFromUserID)
			cmd.Printf("Display name: %s\n", resolvedDisplay)
			cmd.Printf("Assigned role: %s\n", strings.TrimSpace(role))
			cmd.Printf("Approved user id: %s\n", approveResponse.ApprovedUserID)
			cmd.Printf("Approver user id: %s\n", approveResponse.ApproverUserID)
			cmd.Printf("Identity id: %s\n", approveResponse.IdentityID)
			cmd.Printf("Pairing token hint: %s\n", startResponse.TokenHint)
			return nil
		},
	}
	cmd.Flags().StringVar(&approverUserID, "approver-user-id", "", "admin user id performing approval (defaults to --from-user-id)")
	cmd.Flags().StringVar(&role, "role", "admin", "role to assign when creating a new user")
	cmd.Flags().StringVar(&targetUserID, "target-user-id", "", "optional existing user id to link")
	cmd.Flags().IntVar(&expiresInSec, "expires-in-sec", 600, "token TTL in seconds before approval")
	return cmd
}

func newAdminClientFromEnv(timeoutSec int) (*adminclient.Client, error) {
	cfg := config.FromEnv()
	client, err := adminclient.New(cfg)
	if err != nil {
		return nil, err
	}
	return client.WithTimeout(boundedTimeout(timeoutSec)), nil
}
