package cmd

import (
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/nikolareljin/agentvault/internal/agent"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// Gateway env variable names: what the gateway reads from its environment.
const (
	gatewayTokenEnv     = "OLLAMA_TOKEN"
	gatewayBasicAuthEnv = "BASIC_AUTH"
)

// Where add reads secrets that are not given as flags, so they stay out of
// shell history.
const (
	gatewayTokenInputEnv    = "AGENTVAULT_GATEWAY_TOKEN"
	gatewayPasswordInputEnv = "AGENTVAULT_GATEWAY_PASSWORD"
)

var gatewayCmd = &cobra.Command{
	Use:   "gateway",
	Short: "Manage credentials for reverse-proxy gateways in front of model servers",
	Long: `A gateway is a reverse proxy that checks a bearer token (OLLAMA_TOKEN) and
optional basic auth (BASIC_AUTH=user:pass) before it passes a request on to a
model server. The vault keeps its credentials apart from agents: gateways are
not listed by 'agentvault list', not routed to, and generate no config.

The gateway and its clients read the values from their environment:
  agentvault gateway env NAME --format dotenv > .env
  eval "$(agentvault gateway env NAME)"

Examples:
  agentvault gateway add lab --url https://llm.example.com --generate
  AGENTVAULT_GATEWAY_TOKEN=T agentvault gateway add lab --url https://llm.example.com --basic-auth-user me
  agentvault gateway list
  agentvault gateway rotate lab
  agentvault gateway env lab --previous
  agentvault gateway remove lab`,
}

var gatewayAddCmd = &cobra.Command{
	Use:   "add <name>",
	Short: "Add a gateway with a token and/or basic auth",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		gwURL, _ := cmd.Flags().GetString("url")
		token, _ := cmd.Flags().GetString("token")
		generate, _ := cmd.Flags().GetBool("generate")
		user, _ := cmd.Flags().GetString("basic-auth-user")
		pass, _ := cmd.Flags().GetString("basic-auth-password")
		if generate && token != "" {
			return fmt.Errorf("give --token or --generate, not both")
		}
		if token == "" && !generate {
			token = os.Getenv(gatewayTokenInputEnv)
		}
		if user != "" && pass == "" {
			if pass = os.Getenv(gatewayPasswordInputEnv); pass == "" && term.IsTerminal(stdinFD()) {
				var err error
				if pass, err = readPassword("Basic auth password: "); err != nil {
					return err
				}
			}
		}
		if generate {
			var err error
			if token, err = agent.NewGatewayToken(); err != nil {
				return err
			}
		}
		now := time.Now()
		g := agent.Gateway{
			Name:              strings.TrimSpace(args[0]),
			URL:               strings.TrimSpace(gwURL),
			Token:             token,
			BasicAuthUser:     user,
			BasicAuthPassword: pass,
			CreatedAt:         now,
			UpdatedAt:         now,
		}
		if err := g.Validate(); err != nil {
			return err
		}
		if g.Token == "" && g.BasicAuthUser == "" {
			return fmt.Errorf("nothing to store: give --token, --generate or --basic-auth-user/--basic-auth-password")
		}
		v, err := openVault()
		if err != nil {
			return err
		}
		if err := v.AddGateway(g); err != nil {
			return err
		}
		fmt.Printf("Gateway %s added.\n", printable(g.Name))
		if generate {
			fmt.Printf("Print the token with: agentvault gateway env %s\n", printable(g.Name))
		}
		return nil
	},
}

var gatewayListCmd = &cobra.Command{
	Use:   "list",
	Short: "List gateways (secrets are never printed)",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		v, err := openVault()
		if err != nil {
			return err
		}
		gs := v.Gateways()
		if len(gs) == 0 {
			fmt.Println("No gateways stored.")
			return nil
		}
		w := tabwriter.NewWriter(c1Writer(os.Stdout), 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "NAME\tURL\tTOKEN\tBASIC AUTH\tPREVIOUS TOKEN\tROTATED")
		for _, g := range gs {
			basic := "-"
			if g.BasicAuthUser != "" {
				basic = printable(g.BasicAuthUser)
				if g.BasicAuthPassword == "" {
					basic += " (no password)"
				}
			}
			rotated := "never"
			if !g.RotatedAt.IsZero() {
				rotated = g.RotatedAt.Local().Format("2006-01-02 15:04")
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", printable(g.Name), printable(maskURLSecrets(g.URL)),
				yesNo(g.Token != ""), basic, yesNo(g.PreviousToken != ""), rotated)
		}
		return w.Flush()
	},
}

var gatewayRemoveCmd = &cobra.Command{
	Use:   "remove <name>",
	Short: "Remove a gateway",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		v, err := openVault()
		if err != nil {
			return err
		}
		if _, ok := v.GetGateway(args[0]); !ok {
			return fmt.Errorf("no gateway named %s", printable(args[0]))
		}
		if err := v.RemoveGateway(args[0]); err != nil {
			return err
		}
		fmt.Printf("Gateway %s removed.\n", printable(args[0]))
		return nil
	},
}

var gatewayRotateCmd = &cobra.Command{
	Use:   "rotate <name>",
	Short: "Replace the token with a new random one; the old one becomes the previous token",
	Long: `Generate a new token (32 random bytes, base64url). The current token is kept
as the previous token, so clients can move over while the gateway accepts
both; the token before that is dropped. The new token is not printed.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		v, err := openVault()
		if err != nil {
			return err
		}
		g, ok := v.GetGateway(args[0])
		if !ok {
			return fmt.Errorf("no gateway named %s", printable(args[0]))
		}
		token, err := agent.NewGatewayToken()
		if err != nil {
			return err
		}
		now := time.Now()
		if g.Token != "" {
			g.PreviousToken = g.Token
		}
		g.Token = token
		g.RotatedAt = now
		g.UpdatedAt = now
		if err := v.UpdateGateway(g); err != nil {
			return err
		}
		name := printable(g.Name)
		fmt.Printf("Gateway %s: new token stored.\n", name)
		fmt.Println("Update the gateway and every client that sends the token; until then they use the old one.")
		fmt.Printf("  new value:      agentvault gateway env %s\n", name)
		if g.PreviousToken != "" {
			fmt.Printf("  previous value: agentvault gateway env %s --previous\n", name)
		}
		return nil
	},
}

var gatewayEnvCmd = &cobra.Command{
	Use:   "env <name>",
	Short: "Print OLLAMA_TOKEN and BASIC_AUTH for a gateway, for a shell or a .env file",
	Long: `Print the gateway's credentials as environment assignments: OLLAMA_TOKEN
when a token is set and BASIC_AUTH=user:pass when basic auth is set.

  --format sh      export OLLAMA_TOKEN='...'   (default; for eval)
  --format dotenv  OLLAMA_TOKEN='...'          (for a .env file)

The output holds secrets: redirect it to a file only you can read.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		format, _ := cmd.Flags().GetString("format")
		previous, _ := cmd.Flags().GetBool("previous")
		line := shellExportLine
		switch format {
		case "sh":
		case "dotenv":
			line = dotenvLine
		default:
			return fmt.Errorf("unknown format %q; use sh or dotenv", format)
		}
		v, err := openVault()
		if err != nil {
			return err
		}
		g, ok := v.GetGateway(args[0])
		if !ok {
			return fmt.Errorf("no gateway named %s", printable(args[0]))
		}
		token := g.Token
		if previous {
			if g.PreviousToken == "" {
				return fmt.Errorf("gateway %s has no previous token", printable(g.Name))
			}
			token = g.PreviousToken
		}
		var out strings.Builder
		for _, kv := range [][2]string{{gatewayTokenEnv, token}, {gatewayBasicAuthEnv, g.BasicAuth()}} {
			if kv[1] == "" {
				continue
			}
			l := line(kv[0], kv[1])
			// The helpers write a comment for a value they cannot quote. A
			// .env without the credential must not look like success.
			if strings.HasPrefix(l, "#") {
				return fmt.Errorf("%s cannot be written as %s; try --format sh", kv[0], format)
			}
			out.WriteString(l)
		}
		if out.Len() == 0 {
			return fmt.Errorf("gateway %s has no token or basic auth set", printable(g.Name))
		}
		fmt.Print(out.String())
		return nil
	},
}

func init() {
	rootCmd.AddCommand(gatewayCmd)
	gatewayCmd.AddCommand(gatewayAddCmd, gatewayListCmd, gatewayRemoveCmd, gatewayRotateCmd, gatewayEnvCmd)

	gatewayAddCmd.Flags().String("url", "", "gateway URL (http:// or https://)")
	gatewayAddCmd.Flags().String("token", "", "bearer token the gateway checks (OLLAMA_TOKEN); default: $AGENTVAULT_GATEWAY_TOKEN, which keeps it out of shell history")
	gatewayAddCmd.Flags().Bool("generate", false, "generate a random token instead of --token")
	gatewayAddCmd.Flags().String("basic-auth-user", "", "basic auth user (BASIC_AUTH)")
	gatewayAddCmd.Flags().String("basic-auth-password", "", "basic auth password (BASIC_AUTH); default: $AGENTVAULT_GATEWAY_PASSWORD, else asked on a terminal")
	_ = gatewayAddCmd.MarkFlagRequired("url")
	gatewayEnvCmd.Flags().String("format", "sh", "output format: sh or dotenv")
	gatewayEnvCmd.Flags().Bool("previous", false, "print the previous token as OLLAMA_TOKEN")
}
