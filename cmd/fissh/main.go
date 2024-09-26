package main

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/charmbracelet/bubbles/timer"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/log"
	"github.com/charmbracelet/ssh"
	"github.com/charmbracelet/wish"
	"github.com/charmbracelet/wish/activeterm"
	"github.com/charmbracelet/wish/bubbletea"
	"github.com/charmbracelet/wish/logging"
	"github.com/ipinfo/go/v2/ipinfo"
)

const (
	host = "localhost"
	port = "23234"
)

func get_fish(max_width int, max_height int) string {
	// fish are stored in the fishes/*.txt files

	// get the list of fish files
	files, err := os.ReadDir("fishes")
	if err != nil {
		fmt.Println(err)
	}

	// shuffle the list of fish files
	rand.Seed(time.Now().UnixNano())
	rand.Shuffle(len(files), func(i, j int) { files[i], files[j] = files[j], files[i] })

	// iterate through them
	for _, file := range files {
		// read in the fish
		fish, err := os.ReadFile("fishes/" + file.Name())
		if err != nil {
			fmt.Println(err)
		}

		// check if the fish fits in the terminal
		num_lines := 0
		max_length := 0
		leading_spaces := -1

		for _, line := range strings.Split(string(fish), "\n") {
			num_lines += 1

			if len(line) > max_length {
				max_length = len(line)
			}

			line_leading_spaces := 0
			for _, char := range line {
				if char == ' ' {
					line_leading_spaces += 1
				} else {
					break
				}
			}

			if len(line) > line_leading_spaces {
				if leading_spaces == -1 {
					leading_spaces = line_leading_spaces
				} else {
					if line_leading_spaces < leading_spaces {
						leading_spaces = line_leading_spaces
					}
				}
			}
		}

		if num_lines < max_height && max_length < max_width {
			new_fish := ""
			for _, line := range strings.Split(string(fish), "\n") {
				if len(line) > leading_spaces {
					new_fish += line[leading_spaces:] + "\n"
				} else {
					new_fish += "\n"
				}
			}
			return new_fish
		}
	}

	// TODO this is bad
	return ""
}

func lookup_timezone(ip_address string) string {
	token := os.Getenv("IPINFO_TOKEN")

	client := ipinfo.NewClient(nil, nil, token)

	info, err := client.GetIPInfo(net.ParseIP(ip_address))
	if err != nil {
		log.Fatal(err)
	}

	return info.Timezone
}

func main() {
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
	// appStyle := renderer.NewStyle().Background(lipgloss.Color("240"))
	appStyle := renderer.NewStyle()
	txtStyle := renderer.NewStyle().Foreground(lipgloss.Color("10")).Inherit(appStyle)
	fishStyle := renderer.NewStyle().Foreground(lipgloss.Color("8")).Inherit(appStyle)
	quitStyle := renderer.NewStyle().Foreground(lipgloss.Color("8")).Inherit(appStyle)

	timezone := lookup_timezone(s.RemoteAddr().String())
	loc, err := time.LoadLocation(timezone)
	if err != nil {
		log.Fatal(err)
	}

	m := model{
		timer:     timer.NewWithInterval(999999999*time.Second, time.Millisecond),
		time:      time.Now(),
		timezone:  loc,
		fish:      get_fish(pty.Window.Width, pty.Window.Height),
		width:     pty.Window.Width,
		height:    pty.Window.Height,
		appStyle:  appStyle,
		txtStyle:  txtStyle,
		fishStyle: fishStyle,
		quitStyle: quitStyle,
	}
	return m, []tea.ProgramOption{tea.WithAltScreen()}
}

type model struct {
	timer     timer.Model
	time      time.Time
	timezone  *time.Location
	fish      string
	width     int
	height    int
	appStyle  lipgloss.Style
	txtStyle  lipgloss.Style
	fishStyle lipgloss.Style
	quitStyle lipgloss.Style
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
		}
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil
	case timer.TickMsg:
		var cmd tea.Cmd
		m.timer, cmd = m.timer.Update(msg)
		m.time = time.Now()
		return m, cmd
	}
	return m, nil
}

func (m model) View() string {
	s := fmt.Sprintf("The time is %s", m.time.In(m.timezone).Format("15:04:05"))
	if m.time.In(m.timezone).Format("03:04") == "11:11" || os.Getenv("ALWAYSFISH") == "1" {
		s = fmt.Sprintf("%s\n\n%s", m.txtStyle.Render(s), m.fishStyle.Render(m.fish))
	} else {
		s = fmt.Sprintf("%s\n\n%s", m.txtStyle.Render(s), m.fishStyle.Render("Come back at 11:11"))
	}
	s = fmt.Sprintf("%s\n\n%s", s, m.quitStyle.Render("Press 'q' to quit"))

	// center the text
	s = lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, s)

	return m.appStyle.Width(m.width).Height(m.height).Render(s)
}
