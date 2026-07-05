package cli

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/alecthomas/kong"
	"github.com/linuxmatters/jive-visualiser/internal/theme"
)

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

// StyledHelpPrinter returns a Kong help printer that renders usage, arguments,
// and flags with the Lipgloss fire theme.
func StyledHelpPrinter() kong.HelpPrinter {
	return kong.HelpPrinter(func(options kong.HelpOptions, ctx *kong.Context) error {
		var sb strings.Builder

		sb.WriteString(helpTitleStyle.Render("Jive Visualiser ✨"))
		sb.WriteString("\n")
		sb.WriteString(helpDescStyle.Render("Spin your podcast .wav into a groovy MP4 visualiser with spring-driven real-time audio frequencies."))
		sb.WriteString("\n")

		sb.WriteString(helpSectionStyle.Render("Usage:"))
		sb.WriteString("\n  ")
		fmt.Fprintf(&sb, "%s [<input> [<output>]] [flags]", ctx.Model.Name)
		sb.WriteString("\n")

		args := getArguments(ctx)
		if len(args) > 0 {
			sb.WriteString("\n")
			sb.WriteString(helpSectionStyle.Render("Arguments:"))
			sb.WriteString("\n")
			sb.WriteString(argumentTable(args))
			sb.WriteString("\n")
		}

		flags := getFlags(ctx)
		if len(flags) > 0 {
			sb.WriteString("\n")
			sb.WriteString(helpSectionStyle.Render("Flags:"))
			sb.WriteString("\n")
			sb.WriteString(flagTable(flags))
			sb.WriteString("\n")
		}

		sb.WriteString("\n")
		fmt.Fprint(ctx.Stdout, sb.String())
		return nil
	})
}

func argumentTable(args []argument) string {
	t := theme.BorderlessTable().StyleFunc(func(_, col int) lipgloss.Style {
		if col == 0 {
			return helpArgStyle.PaddingLeft(2).PaddingRight(2)
		}
		return lipgloss.NewStyle()
	})
	for _, arg := range args {
		t.Row(arg.name, arg.help)
	}
	return t.Render()
}

func flagTable(flags []flag) string {
	t := theme.BorderlessTable().StyleFunc(func(_, col int) lipgloss.Style {
		if col == 0 {
			return helpFlagStyle.PaddingLeft(2).PaddingRight(2)
		}
		return lipgloss.NewStyle()
	})
	for _, f := range flags {
		help := f.help
		if f.defaultVal != "" {
			suffix := helpDefaultStyle.Render("(default: " + f.defaultVal + ")")
			if help != "" {
				help += " " + suffix
			} else {
				help = suffix
			}
		}
		t.Row(f.flags, help)
	}
	return t.Render()
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

	for _, arg := range ctx.Model.Positional {
		name := arg.Summary()
		help := arg.Help
		args = append(args, argument{name: name, help: help})
	}

	return args
}

func getFlags(ctx *kong.Context) []flag {
	var flags []flag

	flags = append(flags, flag{
		flags: "-h, --help",
		help:  "Show context-sensitive help.",
	})

	for _, f := range ctx.Model.Flags {
		if f.Name == "help" {
			continue
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
