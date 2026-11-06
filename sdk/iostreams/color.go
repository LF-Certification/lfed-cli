package iostreams

const (
	ansiBold   = "\x1b[1m"
	ansiRed    = "\x1b[31m"
	ansiGreen  = "\x1b[32m"
	ansiYellow = "\x1b[33m"
	ansiGray   = "\x1b[90m"
	ansiCyan   = "\x1b[36m"
	ansiReset  = "\x1b[0m"
)

type ColorScheme struct {
	enabled bool
}

func (c *ColorScheme) wrap(code, s string) string {
	if !c.enabled {
		return s
	}
	return code + s + ansiReset
}

func (c *ColorScheme) Bold(s string) string   { return c.wrap(ansiBold, s) }
func (c *ColorScheme) Red(s string) string    { return c.wrap(ansiRed, s) }
func (c *ColorScheme) Green(s string) string  { return c.wrap(ansiGreen, s) }
func (c *ColorScheme) Yellow(s string) string { return c.wrap(ansiYellow, s) }
func (c *ColorScheme) Gray(s string) string   { return c.wrap(ansiGray, s) }
func (c *ColorScheme) Cyan(s string) string   { return c.wrap(ansiCyan, s) }

func (c *ColorScheme) SuccessIcon() string { return c.Green("✓") }
func (c *ColorScheme) FailureIcon() string { return c.Red("X") }
func (c *ColorScheme) WarningIcon() string { return c.Yellow("!") }
