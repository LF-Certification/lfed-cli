package cmdutil

import (
	"strings"

	"github.com/MakeNowJust/heredoc/v2"
)

func Long(s string) string {
	return strings.TrimRight(heredoc.Doc(s), "\n")
}

func Examples(s string) string {
	lines := strings.Split(Long(s), "\n")
	for i, l := range lines {
		if l != "" {
			lines[i] = "  " + l
		}
	}
	return strings.Join(lines, "\n")
}
