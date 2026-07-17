package main

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"

	"github.com/spf13/cobra"
)

// newGenSecretsCmd generates the random secrets a production instance needs,
// printed as ready-to-paste CAIRN_* env lines. It makes no network/DB calls —
// just crypto/rand — so it's safe to run anywhere (locally, in a bootstrap
// job). Pipe to a file or your secrets manager; NEVER commit the output.
//
//	cairn gen-secrets > cairn.secrets.env
//
// The two AES/session keys are 32 random bytes base64-encoded (the master key
// decodes back to 32 bytes and is used raw by auth.SecretBox; the session
// secret signs cookies).
func newGenSecretsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "gen-secrets",
		Short: "Generate the random secrets a production instance needs (CAIRN_* env lines)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			sessionSecret, err := randStd(32)
			if err != nil {
				return err
			}
			masterKey, err := randStd(32)
			if err != nil {
				return err
			}

			out := cmd.OutOrStdout()
			fmt.Fprintln(out, "# Cairn production secrets — generated fresh; store in your secrets")
			fmt.Fprintln(out, "# manager and inject as env. NEVER commit these. Rotate the master key")
			fmt.Fprintln(out, "# later with: cairn rotate-key --new-key=<new>")
			fmt.Fprintln(out, "")
			fmt.Fprintf(out, "CAIRN_AUTH_SESSION_SECRET=%s\n", sessionSecret)
			fmt.Fprintf(out, "CAIRN_AUTH_MASTER_ENCRYPTION_KEY=%s\n", masterKey)
			return nil
		},
	}
}

// randStd returns n cryptographically-random bytes, standard-base64-encoded.
func randStd(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("read random: %w", err)
	}
	return base64.StdEncoding.EncodeToString(b), nil
}
