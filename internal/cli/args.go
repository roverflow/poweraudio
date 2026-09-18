package cli

import "strings"

// flagSpec is the set of flags one command accepts. Commands declare it so an
// unknown flag is an error rather than a device query nobody matches.
type flagSpec struct {
	json   bool
	device bool
}

// options is what a command line asked for beyond its positional arguments.
type options struct {
	json   bool
	device string
}

// parseOptions splits args into the flags a command accepts and everything
// else. The flag package is not used here because it reads the "-10" of
// "poweraudio volume -10" as an unknown flag rather than a relative level,
// and lowering the volume from a media key is most of the point of this
// command set.
func parseOptions(cmd string, args []string, spec flagSpec) ([]string, options, error) {
	var (
		positional []string
		opts       options
	)

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--":
			// Everything after a bare double dash is a query, which is how a
			// device named "--something" can still be selected.
			positional = append(positional, args[i+1:]...)
			return positional, opts, nil

		case spec.json && arg == "--json":
			opts.json = true

		case spec.device && (arg == "--device" || arg == "-d"):
			if i+1 >= len(args) {
				return nil, opts, usagef("%s: --device needs a device query", cmd)
			}
			i++
			opts.device = args[i]

		case spec.device && strings.HasPrefix(arg, "--device="):
			opts.device = strings.TrimPrefix(arg, "--device=")
			if opts.device == "" {
				return nil, opts, usagef("%s: --device needs a device query", cmd)
			}

		case strings.HasPrefix(arg, "--"):
			return nil, opts, usagef("%s: unknown flag %q", cmd, arg)

		default:
			positional = append(positional, arg)
		}
	}

	return positional, opts, nil
}

// expectNoArgs rejects the leftovers of a command that takes none, so a
// mistyped flag does not silently do nothing.
func expectNoArgs(cmd string, positional []string) error {
	if len(positional) > 0 {
		return usagef("%s takes no arguments, got %q", cmd, positional[0])
	}
	return nil
}

// expectOneArg returns the single positional argument a command needs. want
// describes it for the error message.
func expectOneArg(cmd, want string, positional []string) (string, error) {
	switch {
	case len(positional) == 0:
		return "", usagef("%s needs %s", cmd, want)
	case len(positional) > 1:
		return "", usagef("%s takes one argument, got %d (quote it if it contains spaces)", cmd, len(positional))
	}
	return positional[0], nil
}
