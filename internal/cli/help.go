package cli

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/alecthomas/kong"
	"github.com/linuxmatters/jivefire/internal/theme"
)

// Custom help styles - fire theme
var (
	helpTitleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(theme.FireYellow).
			MarginBottom(1)

	helpDescStyle = lipgloss.NewStyle().
			Foreground(theme.FireOrange).
			Italic(true).
			MarginBottom(1)

	helpSectionStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(theme.FireOrange).
				MarginTop(1)

	helpFlagStyle = lipgloss.NewStyle().
			Foreground(theme.FireYellow).
			Bold(true)

	helpArgStyle = lipgloss.NewStyle().
			Foreground(theme.FireRed).
			Bold(true)

	helpDefaultStyle = lipgloss.NewStyle().
				Foreground(theme.WarmGray).
				Italic(true)
)

// StyledHelpPrinter creates a custom help printer with Lipgloss styling
func StyledHelpPrinter(options kong.HelpOptions) kong.HelpPrinter {
	return kong.HelpPrinter(func(options kong.HelpOptions, ctx *kong.Context) error {
		var sb strings.Builder

		// Title and description
		sb.WriteString(helpTitleStyle.Render("Jivefire 🔥"))
		sb.WriteString("\n")
		sb.WriteString(helpDescStyle.Render("Spin your podcast .wav into a groovy MP4 visualiser with Cava-inspired real-time audio frequencies."))
		sb.WriteString("\n")

		// Usage
		sb.WriteString(helpSectionStyle.Render("Usage:"))
		sb.WriteString("\n  ")
		fmt.Fprintf(&sb, "%s [<input> [<output>]] [flags]", ctx.Model.Name)
		sb.WriteString("\n")

		// Arguments section
		args := getArguments(ctx)
		if len(args) > 0 {
			sb.WriteString("\n")
			sb.WriteString(helpSectionStyle.Render("Arguments:"))
			sb.WriteString("\n")
			for _, arg := range args {
				sb.WriteString("  ")
				sb.WriteString(helpArgStyle.Render(arg.name))
				if arg.help != "" {
					sb.WriteString("  ")
					sb.WriteString(arg.help)
				}
				sb.WriteString("\n")
			}
		}

		// Flags section
		flags := getFlags(ctx)
		if len(flags) > 0 {
			sb.WriteString("\n")
			sb.WriteString(helpSectionStyle.Render("Flags:"))
			sb.WriteString("\n")
			for _, flag := range flags {
				sb.WriteString("  ")
				sb.WriteString(helpFlagStyle.Render(flag.flags))
				if flag.help != "" {
					sb.WriteString("  ")
					sb.WriteString(flag.help)
				}
				if flag.defaultVal != "" {
					sb.WriteString(" ")
					sb.WriteString(helpDefaultStyle.Render("(default: " + flag.defaultVal + ")"))
				}
				sb.WriteString("\n")
			}
		}

		sb.WriteString("\n")
		fmt.Fprint(ctx.Stdout, sb.String())
		return nil
	})
}

type argument struct {
	name string
	help string
}

type flag struct {
	flags      string
	help       string
	defaultVal string
}

func getArguments(ctx *kong.Context) []argument {
	var args []argument

	// Parse arguments from the model
	for _, arg := range ctx.Model.Positional {
		name := arg.Summary()
		help := arg.Help
		args = append(args, argument{name: name, help: help})
	}

	return args
}

func getFlags(ctx *kong.Context) []flag {
	var flags []flag

	// Always include help flag
	flags = append(flags, flag{
		flags: "-h, --help",
		help:  "Show context-sensitive help.",
	})

	// Parse flags from the model
	for _, f := range ctx.Model.Flags {
		if f.Name == "help" {
			continue // Already added
		}

		var flagStr string
		if f.Short != 0 {
			flagStr = fmt.Sprintf("-%c, --%s", f.Short, f.Name)
		} else {
			flagStr = fmt.Sprintf("--%s", f.Name)
		}

		if !f.IsBool() && f.PlaceHolder != "" {
			flagStr += "=" + strings.ToUpper(f.PlaceHolder)
		}

		// Only show default if it's a meaningful value (not empty, not type placeholder)
		defaultVal := ""
		if f.HasDefault && !f.IsBool() {
			val := f.Default
			if val != "" && val != "STRING" && val != "BOOL" {
				defaultVal = val
			}
		}

		flags = append(flags, flag{
			flags:      flagStr,
			help:       f.Help,
			defaultVal: defaultVal,
		})
	}

	return flags
}
