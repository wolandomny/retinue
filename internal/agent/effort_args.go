package agent

// applyEffort appends the appropriate effort-related arguments to args.
//
// An empty effort leaves args unchanged. The special "ultracode" tier
// maps to a settings override rather than the --effort flag. Any other
// value is passed through as --effort <value>.
func applyEffort(args []string, effort string) []string {
	switch effort {
	case "":
		return args
	case "ultracode":
		return append(args, "--settings", `{"ultracode": true}`)
	default:
		return append(args, "--effort", effort)
	}
}
