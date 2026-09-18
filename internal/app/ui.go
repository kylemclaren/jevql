package app

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	"github.com/alecthomas/chroma/v2/quick"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"golang.org/x/term"

	"github.com/mclaren/jevpsql/internal/stats"
	"github.com/mclaren/jevpsql/internal/typesafe"
)

var (
	errStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Bold(true)
	warnStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("11"))
	dimStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	accentStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
	okStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	titleStyle  = lipgloss.NewStyle().Bold(true)
)

func isTerminal(f *os.File) bool { return term.IsTerminal(int(f.Fd())) }

// ui holds the styled writers for one session.
type ui struct {
	out, err io.Writer
	color    bool // colour on stdout
	colorErr bool // colour on stderr
	quiet    bool
}

func (u *ui) style(st lipgloss.Style, s string, color bool) string {
	if !color {
		return s
	}
	return st.Render(s)
}

func (u *ui) errorf(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	fmt.Fprintln(u.err, u.style(errStyle, "ERROR:", u.colorErr)+"  "+msg)
}

func (u *ui) warnf(format string, args ...any) {
	fmt.Fprintln(u.err, u.style(warnStyle, fmt.Sprintf(format, args...), u.colorErr))
}

func (u *ui) notef(format string, args ...any) {
	fmt.Fprintln(u.err, u.style(dimStyle, fmt.Sprintf(format, args...), u.colorErr))
}

func (u *ui) footer(st *stats.Stats) {
	fmt.Fprintln(u.err, u.style(dimStyle, st.Footer(), u.colorErr))
}

// sql renders highlighted SQL when colour is on.
func (u *ui) sql(s string, color bool) string {
	if !color {
		return s
	}
	var b strings.Builder
	if err := quick.Highlight(&b, s, "postgresql", "terminal256", "friendly"); err != nil {
		return s
	}
	return strings.TrimRight(b.String(), "\n")
}

// --- spinner -------------------------------------------------------------

type statusMsg string
type stopMsg struct{}

type spinModel struct {
	sp     spinner.Model
	status string
}

func (m spinModel) Init() tea.Cmd { return m.sp.Tick }

func (m spinModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch v := msg.(type) {
	case statusMsg:
		m.status = string(v)
		return m, nil
	case stopMsg:
		return m, tea.Quit
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.sp, cmd = m.sp.Update(v)
		return m, cmd
	}
	return m, nil
}

func (m spinModel) View() string {
	return m.sp.View() + " " + dimStyle.Render(m.status)
}

// progressUI shows a spinner on stderr while judging. It is a no-op when
// stderr is not a terminal.
type progressUI struct {
	p    *tea.Program
	done chan struct{}
	once sync.Once
}

func startProgress(w *os.File, initial string) *progressUI {
	if w == nil || !isTerminal(w) {
		return nil
	}
	sp := spinner.New(spinner.WithSpinner(spinner.MiniDot), spinner.WithStyle(accentStyle))
	m := spinModel{sp: sp, status: initial}
	p := tea.NewProgram(m, tea.WithOutput(w), tea.WithInput(nil), tea.WithoutSignalHandler())
	pu := &progressUI{p: p, done: make(chan struct{})}
	go func() {
		_, _ = p.Run()
		close(pu.done)
	}()
	return pu
}

func (pu *progressUI) update(p typesafe.Progress) {
	if pu == nil {
		return
	}
	pu.p.Send(statusMsg(fmt.Sprintf("judging  %d/%d batches  %d/%d rows  %s tokens  $%.4f",
		p.BatchesDone, p.BatchesTotal, p.ItemsDone, p.ItemsTotal, stats.Compact(p.Usage.InputTokens), p.Usage.USD())))
}

func (pu *progressUI) stop() {
	if pu == nil {
		return
	}
	pu.once.Do(func() {
		pu.p.Send(stopMsg{})
		<-pu.done
	})
}
