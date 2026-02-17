package cli

import "testing"

func TestNewChatPairingCommandIncludesExpectedSubcommands(t *testing.T) {
	cmd := newChatPairingCommand(nil)
	expected := []string{"start", "lookup", "approve", "deny", "pair-admin"}
	for _, name := range expected {
		if _, _, err := cmd.Find([]string{name}); err != nil {
			t.Fatalf("expected subcommand %q to exist: %v", name, err)
		}
	}
}
