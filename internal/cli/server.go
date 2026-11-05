package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/runui/oci-mirror-proxy/internal/admin"
	"github.com/spf13/cobra"
)

func newServerInfoCommand(ctx context.Context, stdout, stderr io.Writer) *cobra.Command {
	cmd := leafCommand("info", "Display client, server and configuration information")
	fs := cmd.Flags()
	path := fs.String("config", "", "deprecated compatibility flag; ignored")
	socket := fs.String("admin-socket", "", "explicit admin endpoint; does not require reading configuration")
	asJSON := fs.Bool("json", false, "output client, server and non-secret configuration information as JSON")
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		endpoint := *socket
		if endpoint == "" {
			var err error
			endpoint, err = resolveAdminSocket("", *path)
			if err != nil {
				return err
			}
		}
		remote, err := admin.NewClient(endpoint).Info()
		if err != nil {
			return fmt.Errorf("query server information (the server must support /v1/info): %w", err)
		}
		if remote.SchemaVersion != 1 {
			return fmt.Errorf("unsupported server information schema %d", remote.SchemaVersion)
		}
		info := admin.Info{ServerInfo: remote, Client: admin.CurrentBuild(version)}
		if *asJSON {
			enc := json.NewEncoder(stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(info)
		}
		fmt.Fprintf(stdout, "Client: %s (%s/%s, %s)\n", info.Client.Version, info.Client.OS, info.Client.Arch, info.Client.GoVersion)
		fmt.Fprintf(stdout, "Server: %s (%s/%s, %s)\nAPI: %s\n", info.Server.Version, info.Server.OS, info.Server.Arch, info.Server.GoVersion, info.APIVersion)
		fmt.Fprintf(stdout, "Configuration:\n  Listen: %s\n  Proxy URL: %s\n", info.Config.Listen, info.Config.ProxyURL)
		for _, reg := range info.Config.Registries {
			fmt.Fprintf(stdout, "  Registry: %s -> %s\n", reg.Alias, reg.Origin)
		}
		return nil
	}
	return cmd
}
