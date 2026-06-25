package app

import (
	"fmt"
	"os"
	"strings"
)

type rgbColor struct {
	r int
	g int
	b int
}

var ghostableWordmark = []string{
	" ▗▄▄▖▗▖ ▗▖ ▗▄▖  ▗▄▄▖▗▄▄▄▖▗▄▖ ▗▄▄▖ ▗▖   ▗▄▄▄▖",
	"▐▌   ▐▌ ▐▌▐▌ ▐▌▐▌     █ ▐▌ ▐▌▐▌ ▐▌▐▌   ▐▌   ",
	"▐▌▝▜▌▐▛▀▜▌▐▌ ▐▌ ▝▀▚▖  █ ▐▛▀▜▌▐▛▀▚▖▐▌   ▐▛▀▀▘",
	"▝▚▄▞▘▐▌ ▐▌▝▚▄▞▘▗▄▄▞▘  █ ▐▌ ▐▌▐▙▄▞▘▐▙▄▄▖▐▙▄▄▖",
	"                                            ",
	"                                            ",
	"                                        ",
}

var ghostableGradient = []rgbColor{
	{r: 70, g: 185, b: 168},
	{r: 57, g: 152, b: 138},
	{r: 45, g: 118, b: 108},
	{r: 32, g: 85, b: 77},
}

func renderGhostableBanner() string {
	lines := make([]string, 0, len(ghostableWordmark))
	for index, line := range ghostableWordmark {
		visibleLine := strings.TrimRight(line, " \t")
		if visibleLine == "" {
			lines = append(lines, "")
			continue
		}

		color := ghostableGradient[min(index, len(ghostableGradient)-1)]
		if os.Getenv("NO_COLOR") != "" {
			lines = append(lines, visibleLine)
			continue
		}
		lines = append(lines, fmt.Sprintf("\x1b[38;2;%d;%d;%dm%s\x1b[0m", color.r, color.g, color.b, visibleLine))
	}

	return strings.Join(lines, "\n")
}
