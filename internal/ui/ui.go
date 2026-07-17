// Package ui reproduces the @clack/prompts look: intro/outro banners, a
// gutter-prefixed log, a spinner, and interactive prompts (via huh).
package ui

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/huh"
	"golang.org/x/term"
)

// ---------------------------------------------------------------- colors

var colorEnabled = term.IsTerminal(int(os.Stdout.Fd())) && os.Getenv("NO_COLOR") == ""

func paint(code, s string) string {
	if !colorEnabled {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}

func Bold(s string) string        { return paint("1", s) }
func Dim(s string) string         { return paint("2", s) }
func Red(s string) string         { return paint("31", s) }
func Green(s string) string       { return paint("32", s) }
func Yellow(s string) string      { return paint("33", s) }
func Cyan(s string) string        { return paint("36", s) }
func Gray(s string) string        { return paint("90", s) }
func BgCyanBlack(s string) string { return paint("46;30", s) }

// ------------------------------------------------------------------- log

const bar = "│"

func Intro(title string) {
	fmt.Println()
	fmt.Println(Gray("┌") + "  " + BgCyanBlack(title))
	fmt.Println(Gray(bar))
}

func Outro(msg string) {
	fmt.Println(Gray(bar))
	if msg != "" {
		fmt.Println(Gray("└") + "  " + msg)
	} else {
		fmt.Println(Gray("└"))
	}
	fmt.Println()
}

func logLines(symbol, msg string) {
	lines := strings.Split(msg, "\n")
	fmt.Println(Gray(bar))
	fmt.Println(symbol + "  " + lines[0])
	for _, l := range lines[1:] {
		fmt.Println(Gray(bar) + "  " + l)
	}
}

func Message(msg string) { logLines(Gray(bar), msg) }
func Info(msg string)    { logLines(Cyan("●"), msg) }
func Success(msg string) { logLines(Green("◆"), msg) }
func Step(msg string)    { logLines(Green("◇"), msg) }
func Warn(msg string)    { logLines(Yellow("▲"), msg) }
func Error(msg string)   { logLines(Red("■"), msg) }

func Note(msg, title string) {
	fmt.Println(Gray(bar))
	fmt.Println(Green("◇") + "  " + Bold(title))
	for _, l := range strings.Split(msg, "\n") {
		fmt.Println(Gray(bar) + "  " + l)
	}
}

func Cancel(msg string) {
	fmt.Println(Gray(bar))
	fmt.Println(Red("■") + "  " + msg)
	fmt.Println()
}

// --------------------------------------------------------------- spinner

type Spinner struct {
	msg  string
	stop chan struct{}
	done chan struct{}
}

var frames = []string{"◒", "◐", "◓", "◑"}

// Start begins an animated spinner line (plain print when not a TTY).
func (s *Spinner) Start(msg string) {
	s.msg = msg
	if !term.IsTerminal(int(os.Stdout.Fd())) {
		fmt.Println(Gray(bar))
		fmt.Println("◒  " + msg + "…")
		return
	}
	s.stop = make(chan struct{})
	s.done = make(chan struct{})
	fmt.Println(Gray(bar))
	go func() {
		defer close(s.done)
		i := 0
		for {
			select {
			case <-s.stop:
				return
			default:
				fmt.Printf("\r%s  %s…", Cyan(frames[i%len(frames)]), s.msg)
				i++
				time.Sleep(120 * time.Millisecond)
			}
		}
	}()
}

// Stop ends the spinner, replacing the line with a final message.
func (s *Spinner) Stop(msg string) {
	if s.stop != nil {
		close(s.stop)
		<-s.done
		s.stop = nil
		fmt.Printf("\r\x1b[2K")
	}
	fmt.Println(Green("◇") + "  " + msg)
}

// --------------------------------------------------------------- prompts

// ErrCancelled is returned when the user aborts a prompt (Esc / Ctrl-C).
var ErrCancelled = errors.New("cancelled")

func runField(f huh.Field) error {
	err := huh.NewForm(huh.NewGroup(f)).Run()
	if err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			return ErrCancelled
		}
		return err
	}
	return nil
}

type SelectOption struct {
	Value string
	Label string
	Hint  string
}

// Select shows an arrow-key list and returns the chosen value.
func Select(message string, options []SelectOption) (string, error) {
	opts := make([]huh.Option[string], len(options))
	for i, o := range options {
		label := o.Label
		if o.Hint != "" {
			label += " " + Dim("("+o.Hint+")")
		}
		opts[i] = huh.NewOption(label, o.Value)
	}
	var value string
	f := huh.NewSelect[string]().Title(message).Options(opts...).Value(&value).Height(12)
	if err := runField(f); err != nil {
		return "", err
	}
	return value, nil
}

// Confirm asks a yes/no question.
func Confirm(message string, initial bool) (bool, error) {
	value := initial
	f := huh.NewConfirm().Title(message).Affirmative("Yes").Negative("No").Value(&value)
	if err := runField(f); err != nil {
		return false, err
	}
	return value, nil
}

// Text asks for a line of input; validate may be nil.
func Text(message, placeholder, defaultValue string, validate func(string) error) (string, error) {
	value := defaultValue
	f := huh.NewInput().Title(message).Placeholder(placeholder).Value(&value)
	if validate != nil {
		f = f.Validate(validate)
	}
	if err := runField(f); err != nil {
		return "", err
	}
	return value, nil
}
