package cli

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"
)

var upstreamDescriptions = map[string]string{"list": "List registry upstreams", "add": "Add a registry upstream", "enable": "Enable an upstream", "disable": "Disable an upstream", "remove": "Remove an upstream"}
var remoteDescriptions = map[string]string{"list": "List remote mirror catalogs", "update": "Update remote mirrors", "remove": "Remove remote mirrors"}
var dockerdDescriptions = map[string]string{"plan": "Preview Docker configuration", "apply": "Apply Docker configuration", "revert": "Revert Docker configuration"}

func leafCommand(name, short string) *cobra.Command {
	cmd := &cobra.Command{Use: name, Short: short, Args: func(_ *cobra.Command, args []string) error {
		if len(args) != 0 {
			return fmt.Errorf("%w: unexpected arguments: %v", errUsage, args)
		}
		return nil
	}}
	// Match Go flag's stop-at-first-positional behavior rather than accepting interspersed options.
	cmd.Flags().SetInterspersed(false)
	return cmd
}

func commandGroup(name, short string) *cobra.Command {
	cmd := leafCommand(name, short)
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		cmd.SetOut(cmd.ErrOrStderr())
		_ = cmd.Usage()
		return fmt.Errorf("%w: %s requires a subcommand", errUsage, name)
	}
	return cmd
}

func newRootCommand(ctx context.Context, stdout, stderr io.Writer) *cobra.Command {
	// Commands and flag values belong to one execution, never a package-level singleton.
	root := commandGroup("oci-mirror-proxy", "A local registry mirror proxy for dockerd")
	root.SilenceErrors, root.SilenceUsage = true, true
	root.CompletionOptions.DisableDefaultCmd = true
	root.SetContext(ctx)
	root.SetOut(stdout)
	root.SetErr(stderr)
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error { return fmt.Errorf("%w: %v", errUsage, err) })
	root.Example = "  oci-mirror-proxy dockerd apply --help\n  oci-mirror-proxy help upstream add"
	root.AddCommand(newInitCommand(ctx, stdout, stderr), newServeCommand(ctx, stdout, stderr), newStatusCommand(stdout, stderr), newReloadCommand(stdout, stderr), newDoctorCommand(ctx, stdout, stderr))
	upstream := commandGroup("upstream", "Manage registry upstreams")
	for _, action := range []string{"list", "add", "enable", "disable", "remove"} {
		upstream.AddCommand(newUpstreamAction(action))
	}
	remote := commandGroup("remote", "Manage remote mirror catalogs")
	// Catalog inspection is independent of local service configuration and the
	// admin API. Keep it a separate command, without a parent startup hook.
	remote.AddCommand(newRemoteShowCommand(ctx, stdout, stderr))
	for _, action := range []string{"list", "update", "remove"} {
		remote.AddCommand(newRemoteAction(ctx, action, stdout, stderr))
	}
	dockerd := commandGroup("dockerd", "Configure Docker to use the proxy")
	for _, action := range []string{"plan", "apply", "revert"} {
		dockerd.AddCommand(newDockerdAction(ctx, action, stdout, stderr))
	}
	server := commandGroup("server", "Inspect the running proxy")
	server.AddCommand(newServerInfoCommand(ctx, stdout, stderr))
	v := leafCommand("version", "Print the version")
	// Historically version ignored trailing arguments; only explicit help changes that behavior.
	v.DisableFlagParsing = true
	v.Args = nil
	v.RunE = func(cmd *cobra.Command, args []string) error {
		if len(args) > 0 && (args[0] == "-h" || args[0] == "--help") {
			return cmd.Help()
		}
		_, err := fmt.Fprintln(stdout, version)
		return err
	}
	root.AddCommand(upstream, remote, dockerd, server, v)
	return root
}

// Resolve help only at command positions, never by scanning values for --help.
func prepareCommandArgs(root *cobra.Command, args []string) ([]string, error) {
	cmd := root
	var path []string
	for len(args) > 0 {
		if cmd.HasSubCommands() && strings.HasPrefix(args[0], "-") {
			if args[0] == "-h" || args[0] == "--help" {
				return append(path, "--help"), nil
			}
			// Groups historically required the action before its flags. Reject
			// options here so Cobra cannot discover an action past an invalid token.
			return nil, fmt.Errorf("%w: unknown command %q for %s", errUsage, args[0], cmd.CommandPath())
		}
		if args[0] == "help" && cmd.HasSubCommands() {
			for _, name := range args[1:] {
				next := childCommand(cmd, name)
				if next == nil {
					return nil, fmt.Errorf("%w: unknown command %q for %s", errUsage, name, cmd.CommandPath())
				}
				cmd = next
				path = append(path, name)
			}
			return append(path, "--help"), nil
		}
		if !cmd.HasSubCommands() || strings.HasPrefix(args[0], "-") {
			break
		}
		next := childCommand(cmd, args[0])
		if next == nil {
			return nil, fmt.Errorf("%w: unknown command %q for %s", errUsage, args[0], cmd.CommandPath())
		}
		cmd = next
		path = append(path, args[0])
		args = args[1:]
	}
	if cmd.Name() == "version" {
		return append(path, args...), nil
	}
	return append(path, legacyFlags(cmd, args)...), nil
}

func childCommand(cmd *cobra.Command, name string) *cobra.Command {
	for _, child := range cmd.Commands() {
		if child.Name() == name {
			return child
		}
	}
	return nil
}

// Keep Go flag's single-dash long options and the remote region alias. Consume
// string values unchanged and stop at -- or a positional argument.
func legacyFlags(cmd *cobra.Command, args []string) []string {
	args = append([]string(nil), args...)
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" || arg == "-" || !strings.HasPrefix(arg, "-") {
			break
		}
		name, value, assigned := strings.Cut(strings.TrimPrefix(strings.TrimPrefix(arg, "-"), "-"), "=")
		if name == "h" || name == "help" {
			// Go flag stopped immediately on help, even with an assigned value.
			// Preserve earlier parse errors, but never parse subsequent tokens.
			args[i] = "--help"
			return args[:i+1]
		}
		if name == "region" && cmd.Parent() != nil && cmd.Parent().Name() == "remote" && cmd.Flags().Lookup("regions") != nil {
			name = "regions"
		}
		f := cmd.Flags().Lookup(name)
		if f == nil {
			// The old parser had no bundled shorthands: -hv is an unknown
			// long option, not a request for help followed by another flag.
			if !strings.HasPrefix(arg, "--") {
				args[i] = "-" + arg
			}
			continue
		}
		args[i] = "--" + name
		if assigned {
			args[i] += "=" + value
		} else if f.NoOptDefVal == "" {
			i++
		}
	}
	return args
}
