package cli

import "strings"

type flagSpec struct {
	json   bool
	device bool
	notify bool
}

type options struct {
	json   bool
	device string
	notify bool
}

// parseOptions avoids the flag package, which reads the -10 in
// "poweraudio volume -10" as an unknown flag.
func parseOptions(cmd string, args []string, spec flagSpec) ([]string, options, error) {
	var (
		positional []string
		opts       options
	)

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--":
			// Everything after "--" is a query, even "--something".
			positional = append(positional, args[i+1:]...)
			return positional, opts, nil

		case spec.json && arg == "--json":
			opts.json = true

		case spec.notify && arg == "--notify":
			opts.notify = true

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

func expectNoArgs(cmd string, positional []string) error {
	if len(positional) > 0 {
		return usagef("%s takes no arguments, got %q", cmd, positional[0])
	}
	return nil
}

func expectOneArg(cmd, want string, positional []string) (string, error) {
	switch {
	case len(positional) == 0:
		return "", usagef("%s needs %s", cmd, want)
	case len(positional) > 1:
		return "", usagef("%s takes one argument, got %d (quote it if it contains spaces)", cmd, len(positional))
	}
	return positional[0], nil
}
