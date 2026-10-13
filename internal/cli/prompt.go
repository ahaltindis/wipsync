package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"golang.org/x/term"
)

type prompter struct {
	ctx context.Context // cancelled on Ctrl+C, which ends a pending question
	in  io.Reader
	out io.Writer
	r   *bufio.Reader
}

func newPrompter(ctx context.Context, in io.Reader, out io.Writer) *prompter {
	return &prompter{ctx: ctx, in: in, out: out}
}

type reply struct {
	line string
	err  error
}

// wait returns the reply, or errInterrupted if the context ends first. The
// read is then left behind; the command is about to exit.
func (p *prompter) wait(read func() (string, error)) (string, error) {
	ch := make(chan reply, 1)
	go func() {
		line, err := read()
		ch <- reply{line, err}
	}()
	select {
	case r := <-ch:
		return r.line, r.err
	case <-p.ctx.Done():
		return "", errInterrupted
	}
}

// terminal returns the input as a terminal, if it is one.
func (p *prompter) terminal() (*os.File, bool) {
	f, ok := p.in.(*os.File)
	return f, ok && term.IsTerminal(int(f.Fd()))
}

// interactive reports whether there is someone to answer a question. Input
// that is not a file was set by the caller, so it counts.
func (p *prompter) interactive() bool {
	if _, isFile := p.in.(*os.File); !isFile {
		return true
	}
	_, ok := p.terminal()
	return ok
}

func (p *prompter) ask(question string) (string, error) {
	if p.r == nil {
		p.r = bufio.NewReader(p.in)
	}
	fmt.Fprint(p.out, question)
	line, err := p.wait(func() (string, error) { return p.r.ReadString('\n') })
	if err == errInterrupted {
		fmt.Fprintln(p.out)
		return "", err
	}
	if err != nil && line == "" {
		return "", fmt.Errorf("no answer: %w", err)
	}
	return strings.TrimSpace(line), nil
}

// askRequired asks until the answer is not empty.
func (p *prompter) askRequired(question string) (string, error) {
	for {
		answer, err := p.ask(question)
		if err != nil || answer != "" {
			return answer, err
		}
	}
}

// choose shows numbered options and returns the index of the chosen one.
// Enter picks the first.
func (p *prompter) choose(title, label string, options []string) (int, error) {
	fmt.Fprintln(p.out, title)
	for i, o := range options {
		fmt.Fprintf(p.out, "  %d) %s\n", i+1, o)
	}
	for {
		answer, err := p.ask(label + " [1]: ")
		if err != nil {
			return 0, err
		}
		if answer == "" {
			return 0, nil
		}
		if n, err := strconv.Atoi(answer); err == nil && n >= 1 && n <= len(options) {
			return n - 1, nil
		}
		fmt.Fprintf(p.out, "Enter a number from 1 to %d.\n", len(options))
	}
}

// confirm asks a yes/no question. Enter gives def.
func (p *prompter) confirm(question string, def bool) (bool, error) {
	hint := " [y/N]: "
	if def {
		hint = " [Y/n]: "
	}
	for {
		answer, err := p.ask(question + hint)
		if err != nil {
			return false, err
		}
		switch strings.ToLower(answer) {
		case "":
			return def, nil
		case "y", "yes":
			return true, nil
		case "n", "no":
			return false, nil
		}
	}
}

// secret asks for a line without showing it on a terminal.
func (p *prompter) secret(question string) (string, error) {
	f, ok := p.terminal()
	if !ok {
		return p.ask(question)
	}
	fd := int(f.Fd())
	// ReadPassword turns echo off. If the wait is interrupted it never gets
	// to turn it back on, so that is done here.
	state, err := term.GetState(fd)
	if err != nil {
		return "", err
	}
	fmt.Fprint(p.out, question)
	line, err := p.wait(func() (string, error) {
		b, err := term.ReadPassword(fd)
		return string(b), err
	})
	if err == errInterrupted {
		term.Restore(fd, state)
	}
	fmt.Fprintln(p.out)
	return line, err
}

// newPassword asks for a password twice, until both answers match.
func (p *prompter) newPassword() (string, error) {
	for {
		first, err := p.secret("Password: ")
		if err != nil {
			return "", err
		}
		if first == "" {
			fmt.Fprintln(p.out, "The password cannot be empty.")
			continue
		}
		second, err := p.secret("Repeat password: ")
		if err != nil {
			return "", err
		}
		if first == second {
			return first, nil
		}
		fmt.Fprintln(p.out, "The two passwords differ.")
	}
}

var (
	errNoTerminal  = errors.New("this command needs a terminal")
	errInterrupted = errors.New("interrupted")
)
