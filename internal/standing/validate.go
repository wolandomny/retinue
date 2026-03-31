package standing

import (
	"fmt"
	"regexp"
)

// kebabCaseRe matches valid kebab-case identifiers: lowercase
// alphanumeric characters separated by single hyphens.
var kebabCaseRe = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// Validate checks a slice of agents for common configuration errors:
//   - No duplicate IDs
//   - All IDs are non-empty
//   - All prompts are non-empty
//   - IDs are valid kebab-case (lowercase, hyphens, alphanumeric)
func Validate(agents []Agent) error {
	seen := make(map[string]bool, len(agents))

	for i, a := range agents {
		if a.ID == "" {
			return fmt.Errorf("agent[%d]: id must not be empty", i)
		}

		if !kebabCaseRe.MatchString(a.ID) {
			return fmt.Errorf("agent[%d]: id %q is not valid kebab-case (lowercase alphanumeric and hyphens)", i, a.ID)
		}

		if seen[a.ID] {
			return fmt.Errorf("agent[%d]: duplicate id %q", i, a.ID)
		}
		seen[a.ID] = true

		if a.Prompt == "" {
			return fmt.Errorf("agent[%d] (%s): prompt must not be empty", i, a.ID)
		}
	}

	return nil
}
