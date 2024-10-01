package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/signal"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/breqdev/fissh.breq.dev/internal/fishes"
	"github.com/breqdev/fissh.breq.dev/internal/timezone"
	"github.com/charmbracelet/bubbles/timer"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/log"
	"github.com/charmbracelet/ssh"
	"github.com/charmbracelet/wish"
	"github.com/charmbracelet/wish/activeterm"
	"github.com/charmbracelet/wish/bubbletea"
	"github.com/charmbracelet/wish/logging"
)

func main() {
	host := os.Getenv("HOST")
	if host == "" {
		host = "localhost"
	}
	port := os.Getenv("PORT")
	if port == "" {
		port = "23234"
	}

	s, err := wish.NewServer(
		wish.WithAddress(net.JoinHostPort(host, port)),
		wish.WithHostKeyPath(".ssh/id_ed25519"),
		wish.WithMiddleware(
			bubbletea.Middleware(teaHandler),
			activeterm.Middleware(), // Bubble Tea apps usually require a PTY.
			logging.Middleware(),
		),
	)
	if err != nil {
		log.Error("Could not start server", "error", err)
	}

	done := make(chan os.Signal, 1)
	signal.Notify(done, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)
	log.Info("Starting SSH server", "host", host, "port", port)
	go func() {
		if err = s.ListenAndServe(); err != nil && !errors.Is(err, ssh.ErrServerClosed) {
			log.Error("Could not start server", "error", err)
			done <- nil
		}
	}()

	<-done
	log.Info("Stopping SSH server")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer func() { cancel() }()
	if err := s.Shutdown(ctx); err != nil && !errors.Is(err, ssh.ErrServerClosed) {
		log.Error("Could not stop server", "error", err)
	}
}

func extractTimezoneFromEnv(env []string) (string, error) {
	const envName = "TZ="
	idx := slices.IndexFunc(env, func(s string) bool { return strings.HasPrefix(s, envName) })
	if idx == -1 {
		return "", nil
	}

	timezone := env[idx][len(envName):]

	_, err := time.LoadLocation(timezone)
	if err != nil {
		return "", err
	}
	return timezone, nil

}

// You can wire any Bubble Tea model up to the middleware with a function that
// handles the incoming ssh.Session. Here we just grab the terminal info and
// pass it to the new model. You can also return tea.ProgramOptions (such as
// tea.WithAltScreen) on a session by session basis.
func teaHandler(s ssh.Session) (tea.Model, []tea.ProgramOption) {
	// This should never fail, as we are using the activeterm middleware.
	pty, _, _ := s.Pty()

	// When running a Bubble Tea app over SSH, you shouldn't use the default
	// lipgloss.NewStyle function.
	// That function will use the color profile from the os.Stdin, which is the
	// server, not the client.
	// We provide a MakeRenderer function in the bubbletea middleware package,
	// so you can easily get the correct renderer for the current session, and
	// use it to create the styles.
	// The recommended way to use these styles is to then pass them down to
	// your Bubble Tea model.
	renderer := bubbletea.MakeRenderer(s)
	appStyle := renderer.NewStyle().AlignHorizontal(lipgloss.Center)

	ip := s.RemoteAddr().(*net.TCPAddr).IP

	envTimezone, err := extractTimezoneFromEnv(s.Environ())

	var foundTimezone string

	if envTimezone == "" || err != nil {
		foundTimezone = timezone.LookupTimezone(ip.String())
	} else {
		foundTimezone = envTimezone
	}

	loc, err := time.LoadLocation(foundTimezone)
	if err != nil {
		log.Fatal(err)
	}

	m := model{
		timer:          timer.NewWithInterval(999999999*time.Second, time.Millisecond),
		time:           time.Now(),
		ip:             ip,
		timezone:       loc,
		timezoneViaEnv: envTimezone != "",
		page:           HomePage,
		fish:           "",
		window:         tea.WindowSizeMsg{Width: pty.Window.Width, Height: pty.Window.Height},
		styles: appStyles{
			app:    appStyle,
			header: renderer.NewStyle().Bold(true).Foreground(lipgloss.Color("7")).Margin(2).Inherit(appStyle).AlignVertical(lipgloss.Top),
			txt:    renderer.NewStyle().Foreground(lipgloss.Color("2")).Inherit(appStyle),
			about:  renderer.NewStyle().Foreground(lipgloss.Color("2")).Inherit(appStyle).Align(lipgloss.Center),
			debug:  renderer.NewStyle().Foreground(lipgloss.Color("4")).Inherit(appStyle).Align(lipgloss.Center),
			fish:   renderer.NewStyle().Foreground(lipgloss.Color("12")).Inherit(appStyle),
		},
	}
	return m, []tea.ProgramOption{tea.WithAltScreen()}
}

type appStyles struct {
	app    lipgloss.Style
	header lipgloss.Style
	txt    lipgloss.Style
	about  lipgloss.Style
	debug  lipgloss.Style
	fish   lipgloss.Style
}

const (
	HomePage = iota
	AboutPage
)

type model struct {
	timer          timer.Model
	time           time.Time
	ip             net.IP
	timezone       *time.Location
	timezoneViaEnv bool
	page           int
	fish           string
	window         tea.WindowSizeMsg
	styles         appStyles
}

func (m model) Init() tea.Cmd {
	return m.timer.Init()
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "a":
			m.page = AboutPage
			return m, nil
		case "esc", "h":
			m.page = HomePage
			return m, nil
		case "r":
			if m.IsFishTime() {
				m.fish = fishes.GetFish(m.window.Width, m.window.Height)
			}
			return m, nil
		}
	case tea.WindowSizeMsg:
		m.window = msg
		return m, nil
	case timer.TickMsg:
		var cmd tea.Cmd
		m.timer, cmd = m.timer.Update(msg)
		m.time = time.Now()
		if m.IsFishTime() {
			if m.fish == "" {
				m.fish = fishes.GetFish(m.window.Width, m.window.Height)
			}
		}
		return m, cmd
	}
	return m, nil
}

func (m model) IsFishTime() bool {
	return m.time.In(m.timezone).Format("03:04") == "11:11" || os.Getenv("ALWAYSFISH") == "1"
}

const menuHeight = 5

func (m model) View() string {
	page := lipgloss.Place(m.window.Width, m.window.Height-menuHeight, lipgloss.Center, lipgloss.Center, m.content())
	menu := lipgloss.Place(m.window.Width, menuHeight, lipgloss.Center, lipgloss.Center, m.menu())
	return lipgloss.JoinVertical(lipgloss.Center, page, menu)
}

func (m model) menu() string {
	return lipgloss.JoinHorizontal(
		lipgloss.Top,
		m.styles.header.Render("(esc/h) home"),
		m.styles.header.Render("(a) about"),
		m.styles.header.Render("(r) refresh"),
		m.styles.header.Foreground(lipgloss.Color("124")).Render("(q) quit"),
	)
}

func (m model) content() string {
	switch m.page {
	case HomePage:
		return m.homepage()
	case AboutPage:
		return m.aboutpage()
	default:
		return m.styles.txt.Render("Page Not Found")
	}
}

func (m model) homepage() string {
	s := fmt.Sprintf("the time is %s", m.time.In(m.timezone).Format("03:04:05 pm"))
	if m.fish == "" {
		return fmt.Sprintf("%s\n\n%s", m.styles.txt.Render(s), m.styles.fish.Render("come back at 11:11"))
	}
	return fmt.Sprintf("%s\n\n%s\n\n%s", m.styles.txt.Render(s), m.styles.fish.Render(m.fish), m.styles.fish.Render("make a fish"))
}

const credits = `made with <3 by ` + "\x1B]8;;https://breq.dev\x1B\\@breqdev\x1B]8;;\x1B\\ and \x1B]8;;https://avasilver.dev\x1B\\@avasilver\x1B]8;;\x1B\\" + `
inspired by ` + "\x1B]8;;https://weepingwitch.github.io\x1B\\@weepingwitch\x1B]8;;\x1B\\" + `
concept by ` + "\x1B]8;;https://miakizz.quest\x1B\\@miakizz\x1B]8;;\x1B\\" + `
fishes from ` + "\x1B]8;;https://ascii.co.uk/art/fish\x1B\\ascii.co.uk\x1B]8;;\x1B\\"

func (m model) aboutpage() string {
	var timezoneSource string
	if m.timezoneViaEnv {
		timezoneSource = fmt.Sprintf("timezone read from env variable (%s)\n", m.timezone)
	} else {
		timezoneSource = fmt.Sprintf("timezone fetched from your ip (%s)\n", m.timezone)
	}
	debugText := timezoneSource + fmt.Sprintf("you are calling from: %s", m.ip.String())
	return fmt.Sprintf("%s\n\n%s", m.styles.about.Render(credits), m.styles.debug.Render(debugText))
}
