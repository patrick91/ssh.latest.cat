package main

// An example Bubble Tea server. This will put an ssh session into alt screen
// and continually print up to date terminal information.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/timer"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/wish"
	bm "github.com/charmbracelet/wish/bubbletea"
	lm "github.com/charmbracelet/wish/logging"
	"github.com/gliderlabs/ssh"
	"github.com/muesli/termenv"

	"latestcat/utils/colors"
)

const host = "0.0.0.0"
const port = 22

var cat = "                               ,----.\n" +
	"                              ( WOW! )                         .-.\n" +
	"                               `----' _                         \\ \\\n" +
	"                                     (_)                         \\ \\\n" +
	"                                         O                       | |\n" +
	"                    |\\ /\\                  o                     | |\n" +
	"    __              |,\\(_\\_                  . /\\---/\\   _,---._ | |\n" +
	"   ( (              |\\,`   `-^.               /^   ^  \\,'       `. ;\n" +
	"    \\ \\             :    `-'   )             ( O   O   )           ;\n" +
	"     \\ \\             \\        ;               `.=o=__,'            \\\n" +
	"      \\ \\             `-.   ,'                  /         _,--.__   \\\n" +
	"       \\ \\ ____________,'  (                   /  _ )   ,'   `-. `-. \\\n" +
	"        ; '                ;                  / ,' /  ,'        \\ \\ \\ \\\n" +
	"        \\                 /___,-.            / /  / ,'          (,_)(,_)\n" +
	"         `,    ,_____|  ;'_____,'           (,;  (,,)\n" +
	"       ,-\" \\  :      | :\n" +
	"      ( .-\" \\ `.__   | |\n" +
	"       \\__)  `.__,'  |__)"

var term = termenv.ColorProfile()

type versionResultMsg struct {
	Version      string
	SoftwareName string
}
type notFoundMsg struct{}

func MakeGradient(text string) string {
	lines := strings.Split(text, "\n")

	maxLength := 0

	for _, line := range lines {
		if len(line) > maxLength {
			maxLength = len(strings.Split(line, ""))
		}
	}

	var ramp = colors.MakeRamp("#B14FFF", "#00FFA3", float64(maxLength))

	result := ""

	for i := 0; i < len(lines); i++ {
		chars := strings.Split(lines[i], "")

		for i, char := range chars {
			result += termenv.String(char).Foreground(term.Color(ramp[i])).String()
		}

		result += "\n"
	}

	return result

}
func main() {
	s, err := wish.NewServer(
		wish.WithAddress(fmt.Sprintf("%s:%d", host, port)),
		wish.WithHostKeyPath(".ssh/term_info_ed25519"),
		wish.WithMiddleware(
			bm.Middleware(teaHandler),
			lm.Middleware(),
		),
	)
	if err != nil {
		log.Fatalln(err)
	}

	done := make(chan os.Signal, 1)
	signal.Notify(done, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)
	log.Printf("Starting SSH server on %s:%d", host, port)
	go func() {
		if err = s.ListenAndServe(); err != nil {
			log.Fatalln(err)
		}
	}()

	<-done
	log.Println("Stopping SSH server")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer func() { cancel() }()
	if err := s.Shutdown(ctx); err != nil {
		log.Fatalln(err)
	}
}

func teaHandler(s ssh.Session) (tea.Model, []tea.ProgramOption) {
	pty, _, active := s.Pty()
	if !active {
		fmt.Println("no active terminal, skipping")
		return nil, nil
	}

	ti := textinput.New()
	ti.Placeholder = "Python"
	ti.Focus()
	ti.CharLimit = 156
	ti.Width = 20

	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("205"))

	m := model{
		term:          pty.Term,
		textInput:     ti,
		latestVersion: "",
		spinner:       sp,
		notFound:      false,
		timer:         timer.NewWithInterval(time.Second*5, time.Second),
	}
	return m, []tea.ProgramOption{tea.WithAltScreen()}
}

type model struct {
	term          string
	width         int
	height        int
	loading       bool
	notFound      bool
	textInput     textinput.Model
	latestVersion string
	softwareName  string
	spinner       spinner.Model
	timer         timer.Model
	quitting      bool
}

func (m model) Init() tea.Cmd {
	return textinput.Blink
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd

	switch msg := msg.(type) {
	case notFoundMsg:
		m.notFound = true
		m.loading = false

		return m, tea.Batch(
			cmd,
			m.timer.Init(),
			m.timer.Start(),
		)

	case versionResultMsg:
		m.latestVersion = msg.Version
		m.softwareName = msg.SoftwareName
		m.loading = false

		return m, tea.Batch(
			cmd,
			m.timer.Init(),
			m.timer.Start(),
		)
	case tea.KeyMsg:
		switch msg.String() {
		case "esc", "ctrl+c":
			return m, tea.Quit
		}
		switch msg.Type {
		case tea.KeyEnter:
			m.loading = true
			return m, fetchVersion(m.textInput.Value())
		}

		m.textInput, cmd = m.textInput.Update(msg)
		return m, cmd
	case spinner.TickMsg:
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case timer.TickMsg:
		var cmd tea.Cmd
		m.timer, cmd = m.timer.Update(msg)
		return m, cmd

	case timer.TimeoutMsg:
		m.quitting = true
		return m, tea.Quit

	default:
		return m, m.spinner.Tick
	}

}

func (m model) View() string {
	var softwareStyle = lipgloss.NewStyle().
		Bold(true).
		Italic(true)
	var versionStyle = lipgloss.NewStyle().
		Bold(true).
		Underline(true)
	var helloStyle = lipgloss.NewStyle().
		Bold(true).
		Underline(true)

	s := ""
	header := cat

	if m.loading {
		s += fmt.Sprintf("%s Fetching latest version for %s...\n\n", m.spinner.View(), m.textInput.Value())
	} else if m.notFound {
		header = strings.Replace(header, "WOW", "404", -1)
		header = strings.Replace(header, "O   O", "x   x", -1)
		s += fmt.Sprintf("😿 No software found for %s", softwareStyle.Render(m.textInput.Value()))
		s += "\n\n"
		s += fmt.Sprintf("Closing in %s 👋", m.timer.View())
	} else if m.latestVersion != "" {

		s += "Latest version for " + softwareStyle.Render(m.softwareName) + " is " + versionStyle.Render(m.latestVersion)
		s += "\n\n"
		s += fmt.Sprintf("Closing in %s 👋", m.timer.View())
	} else {
		s += helloStyle.Render("Hello there! Welcome to latest.cat 🐈‍!")
		s += "\n\n"
		s += "What software are you looking for?\n\n"
		s += fmt.Sprintf("%s", m.textInput.View())
	}

	s = "\n" + MakeGradient(header) + "\n\n" + s

	var style = lipgloss.NewStyle().
		PaddingTop(0).
		PaddingRight(2).
		PaddingBottom(2).
		PaddingLeft(2)

	return style.Render(s)
}

var FIND_VERSION_QUERY = `
query FindVersion($slug: String!) {
	findVersion(slug: $slug) {
		latestVersion
		software {
			slug
			name
		}
	}
}
`

type Variables struct {
	Slug string `json:"slug"`
}

type Operation struct {
	Query     string    `json:"query"`
	Variables Variables `json:"variables"`
}

type FindVersionResponse struct {
	Data *Data `json:"data,omitempty"`
}

type Data struct {
	FindVersion *FindVersion `json:"findVersion,omitempty"`
}

type FindVersion struct {
	LatestVersion string   `json:"latestVersion"`
	Software      Software `json:"software"`
}

type Software struct {
	Slug string `json:"slug"`
	Name string `json:"name"`
}

func fetchVersion(slug string) tea.Cmd {
	return func() tea.Msg {
		values := Operation{
			Query: FIND_VERSION_QUERY,
			Variables: Variables{
				Slug: slug,
			},
		}

		json_data, err := json.Marshal(values)

		if err != nil {
			log.Fatal(err)
		}

		response, err := http.Post(
			"https://latest-cat.fly.dev/graphql",
			"application/json",
			bytes.NewBuffer(json_data),
		)

		if err != nil {
			log.Fatal(err)
		}

		var res FindVersionResponse

		json.NewDecoder(response.Body).Decode(&res)

		if res.Data.FindVersion == nil {
			return notFoundMsg{}
		}

		return versionResultMsg{Version: res.Data.FindVersion.LatestVersion, SoftwareName: res.Data.FindVersion.Software.Name}
	}
}
