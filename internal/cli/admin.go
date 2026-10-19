package cli

import (
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/runui/oci-mirror-proxy/internal/admin"
	"github.com/runui/oci-mirror-proxy/internal/config"
	"github.com/runui/oci-mirror-proxy/internal/ociapi"
)

// cmdStatus prints the health of every upstream of a running proxy.
func cmdStatus(args []string, stdout, stderr io.Writer) error {
	fs := newFlagSet("status", stderr)
	configPath := fs.String("config", "", "configuration file (default: user or system configuration)")
	socketFlag := fs.String("admin-socket", "", "admin socket path (default: configuration or per-user location)")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	socket, err := resolveAdminSocket(*socketFlag, *configPath)
	if err != nil {
		return err
	}
	st, err := admin.NewClient(socket).Status()
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "listen: %s\n\n", st.Listen)
	tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "REGISTRY\tUPSTREAM\tSTATE\tBREAKER\tREQUESTS\tFAILURES\tLAST LATENCY")
	for _, reg := range st.Registries {
		for _, up := range reg.Upstreams {
			state := "enabled"
			if up.Disabled {
				state = "disabled"
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%d\t%d\t%s\n",
				reg.Alias, up.ID, state, up.Breaker, up.Requests, up.Failures, up.LastLatency)
		}
	}
	return tw.Flush()
}

// cmdReload asks a running proxy to re-read its configuration file.
func cmdReload(args []string, stderr io.Writer) error {
	fs := newFlagSet("reload", stderr)
	configPath := fs.String("config", "", "configuration file (default: user or system configuration)")
	socketFlag := fs.String("admin-socket", "", "admin socket path (default: configuration or per-user location)")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	socket, err := resolveAdminSocket(*socketFlag, *configPath)
	if err != nil {
		return err
	}
	return admin.NewClient(socket).Reload()
}

// cmdUpstream manages the upstreams of a registry in a running proxy.
func cmdUpstream(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("%w: want one of list, add, enable, disable, remove", errUsage)
	}
	action, rest := args[0], args[1:]
	fs := newFlagSet("upstream "+action, stderr)
	configPath := fs.String("config", "", "configuration file (default: user or system configuration)")
	socketFlag := fs.String("admin-socket", "", "admin socket path (default: configuration or per-user location)")
	registry := fs.String("registry", ociapi.DefaultAlias, "registry alias")

	switch action {
	case "list":
		if err := parseFlags(fs, rest); err != nil {
			return err
		}
		client, err := adminClient(*socketFlag, *configPath)
		if err != nil {
			return err
		}
		ups, err := client.ListUpstreams(*registry)
		if err != nil {
			return err
		}
		return printUpstreams(stdout, ups)

	case "add":
		id := fs.String("id", "", "upstream identifier")
		url := fs.String("url", "", "base URL of the Registry v2 endpoint")
		authType := fs.String("auth", config.AuthNone, "none, basic or token")
		username := fs.String("username", "", "username for basic or token auth")
		passwordRef := fs.String("password-ref", "", "secret reference, e.g. env:MIRROR_PASSWORD or file:/run/secrets/pw")
		connect := fs.String("connect", "", "TCP + TLS connection timeout, e.g. 10s (default 10s)")
		firstByte := fs.String("first-byte", "", "response header timeout, e.g. 5s (default 5s)")
		overall := fs.String("overall", "", "timeout for metadata requests, e.g. 60s (default 60s)")
		caFile := fs.String("ca-file", "", "extra CA certificate file for this upstream")
		insecure := fs.Bool("insecure-skip-verify", false, "disable TLS verification for this upstream (not recommended)")
		disabled := fs.Bool("disabled", false, "add the upstream but keep it out of rotation")
		if err := parseFlags(fs, rest); err != nil {
			return err
		}
		if *id == "" || *url == "" {
			return fmt.Errorf("%w: --id and --url are required", errUsage)
		}
		client, err := adminClient(*socketFlag, *configPath)
		if err != nil {
			return err
		}
		spec := admin.UpstreamSpec{
			ID:        *id,
			URL:       *url,
			Disabled:  *disabled,
			Connect:   *connect,
			FirstByte: *firstByte,
			Overall:   *overall,
			Auth: admin.AuthSpec{
				Type:     *authType,
				Username: *username,
				Password: *passwordRef,
			},
			TLS: admin.TLSSpec{CAFile: *caFile, InsecureSkipVerify: *insecure},
		}
		if err := client.AddUpstream(*registry, spec); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "added upstream %s to %s\n", *id, *registry)
		return nil

	case "enable", "disable", "remove":
		id := fs.String("id", "", "upstream identifier")
		if err := parseFlags(fs, rest); err != nil {
			return err
		}
		if *id == "" {
			return fmt.Errorf("%w: --id is required", errUsage)
		}
		client, err := adminClient(*socketFlag, *configPath)
		if err != nil {
			return err
		}
		switch action {
		case "remove":
			err = client.RemoveUpstream(*registry, *id)
		default:
			err = client.SetUpstreamDisabled(*registry, *id, action == "disable")
		}
		if err != nil {
			return err
		}
		fmt.Fprintf(stdout, "%s upstream %s in %s\n", pastTense(action), *id, *registry)
		return nil

	default:
		return fmt.Errorf("%w: unknown upstream action %q", errUsage, action)
	}
}

func adminClient(socketFlag, configPath string) (*admin.Client, error) {
	socket, err := resolveAdminSocket(socketFlag, configPath)
	if err != nil {
		return nil, err
	}
	return admin.NewClient(socket), nil
}

func pastTense(action string) string {
	switch action {
	case "enable":
		return "enabled"
	case "disable":
		return "disabled"
	default:
		return "removed"
	}
}

func printUpstreams(w io.Writer, ups []admin.UpstreamSpec) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tURL\tAUTH\tSTATE\tFIRST BYTE\tOVERALL")
	for _, u := range ups {
		state := "enabled"
		if u.Disabled {
			state = "disabled"
		}
		auth := u.Auth.Type
		if auth == "" {
			auth = config.AuthNone
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", u.ID, u.URL, auth, state, orDefault(u.FirstByte, "5s"), orDefault(u.Overall, "60s"))
	}
	return tw.Flush()
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
