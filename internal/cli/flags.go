package cli

import (
	"flag"
	"fmt"
	"strings"
)

type Strings []string

func (s *Strings) String() string {
	return strings.Join(*s, ",")
}

func (s *Strings) Set(value string) error {
	for _, part := range strings.Split(value, ",") {
		item := strings.TrimSpace(part)
		if item != "" {
			*s = append(*s, item)
		}
	}

	return nil
}

func Parse(fs *flag.FlagSet, args []string, boolFlags map[string]bool) ([]string, error) {
	flagArgs, positionals, err := splitFlags(args, boolFlags)
	if err != nil {
		return nil, err
	}

	if err := fs.Parse(flagArgs); err != nil {
		return nil, err
	}

	return append(fs.Args(), positionals...), nil
}

func splitFlags(args []string, boolFlags map[string]bool) ([]string, []string, error) {
	flagArgs := make([]string, 0, len(args))
	positionals := make([]string, 0, len(args))

	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			positionals = append(positionals, args[i+1:]...)
			break
		}

		if !strings.HasPrefix(arg, "-") || arg == "-" {
			positionals = append(positionals, arg)
			continue
		}

		name := flagName(arg)
		flagArgs = append(flagArgs, arg)
		if strings.Contains(arg, "=") || boolFlags[name] || isHelpFlag(name) {
			continue
		}

		if i+1 >= len(args) {
			return nil, nil, fmt.Errorf("flag %s requires a value", arg)
		}
		i++
		flagArgs = append(flagArgs, args[i])
	}

	return flagArgs, positionals, nil
}

func flagName(arg string) string {
	name := strings.TrimLeft(arg, "-")
	if idx := strings.IndexByte(name, '='); idx >= 0 {
		name = name[:idx]
	}
	return name
}

func isHelpFlag(name string) bool {
	return name == "h" || name == "help"
}

func BoolFlags(names ...string) map[string]bool {
	flags := make(map[string]bool, len(names))
	for _, name := range names {
		flags[name] = true
	}
	return flags
}

func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	return 1
}
