package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/dimetron/pi-go/internal/auth"
)

func newLogoutCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "logout [provider]",
		Short: "Remove stored credentials for a provider",
		Long: `Remove stored credentials for a provider.

Only providers that keep state beyond ~/.pi-go/.env are supported today:

  xai     Removes the stored SuperGrok / X Premium+ token, including the
          refresh token. The XAI_API_KEY entry in ~/.pi-go/.env is left
          alone, since it may hold a separate developer API key.`,
		Args: cobra.MaximumNArgs(1),
		RunE: runLogout,
	}
}

func runLogout(_ *cobra.Command, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("a provider is required — supported: xai")
	}
	switch args[0] {
	case "xai":
		if err := auth.ClearXAITokens(); err != nil {
			return err
		}
		fmt.Println("Removed the stored xAI subscription token.")
		fmt.Println("A developer API key in ~/.pi-go/.env (XAI_API_KEY) was left in place.")
		return nil
	default:
		return fmt.Errorf("unknown provider %q — supported: xai", args[0])
	}
}
