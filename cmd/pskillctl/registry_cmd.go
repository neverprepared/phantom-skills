package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	pkserver "github.com/neverprepared/phantom-skills/internal/server"
)

// registryCmd groups auth/scope registry operations (the analog of pbrainctl's
// `server vault`). A scope is a directory profiles/<scope>/ holding auth.toml
// (bearer_token) and an optional config.toml.
func registryCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "registry",
		Short: "Manage auth scopes and bearer tokens",
	}
	c.AddCommand(registryListCmd(), registryAddCmd(), registryTokenCmd())
	return c
}

func registryListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List scopes and token descriptions",
		RunE: func(cmd *cobra.Command, _ []string) error {
			dir := pkserver.DefaultConfigDir()
			cfg, err := pkserver.LoadServerConfig(dir)
			if err != nil {
				return err
			}
			reg := pkserver.NewRegistry()
			if _, err := reg.Load(dir, cfg.Defaults); err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			scopes := reg.Scopes()
			if len(scopes) == 0 {
				fmt.Fprintln(out, "(no scopes configured)")
				return nil
			}
			for _, b := range scopes {
				fmt.Fprintf(out, "%-24s %s\n", b.Key.String(), b.Auth.Description)
			}
			return nil
		},
	}
}

func registryAddCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "add <scope>",
		Short: "Scaffold profiles/<scope>/auth.toml with a fresh bearer token",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			scope := args[0]
			desc, _ := cmd.Flags().GetString("description")
			dir := pkserver.DefaultConfigDir()
			authPath := filepath.Join(dir, "profiles", scope, "auth.toml")
			if _, err := os.Stat(authPath); err == nil {
				return fmt.Errorf("scope %q already exists at %s (use `registry token %s --rotate` to change its token)", scope, authPath, scope)
			}
			if err := os.MkdirAll(filepath.Dir(authPath), 0o755); err != nil {
				return fmt.Errorf("create scope dir: %w", err)
			}
			token, err := genToken()
			if err != nil {
				return err
			}
			if err := writeAuthToml(authPath, token, desc); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "created %s\n  bearer_token: %s\n", authPath, token)
			return nil
		},
	}
	c.Flags().String("description", "", "operator-facing description for the scope")
	return c
}

func registryTokenCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "token <scope>",
		Short: "Print or rotate the bearer token for a scope",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			scope := args[0]
			rotate, _ := cmd.Flags().GetBool("rotate")
			dir := pkserver.DefaultConfigDir()
			_, auth, err := pkserver.LoadScopeFiles(dir, scope)
			if err != nil {
				return err
			}
			if !rotate {
				fmt.Fprintln(cmd.OutOrStdout(), auth.BearerToken)
				return nil
			}
			token, err := genToken()
			if err != nil {
				return err
			}
			authPath := filepath.Join(dir, "profiles", scope, "auth.toml")
			if err := writeAuthToml(authPath, token, auth.Description); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "rotated %s\n  bearer_token: %s\n", authPath, token)
			return nil
		},
	}
	c.Flags().Bool("rotate", false, "generate and write a new token")
	return c
}

// genToken returns a 32-byte random bearer token, hex-encoded with an "sk-"
// prefix so it's recognizable in logs and config.
func genToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate token: %w", err)
	}
	return "sk-" + hex.EncodeToString(b), nil
}

// writeAuthToml writes an auth.toml atomically (temp + rename), 0600 since it
// holds a secret.
func writeAuthToml(path, token, description string) error {
	body := fmt.Sprintf("bearer_token = %q\ndescription  = %q\n", token, description)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(body), 0o600); err != nil {
		return fmt.Errorf("write %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("rename %s: %w", path, err)
	}
	return nil
}
