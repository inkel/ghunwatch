package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"

	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/evertras/bubble-table/table"
	"github.com/google/go-github/github"
	"golang.org/x/oauth2"
)

func main() {
	ctx := context.TODO()

	if err := realMain(ctx); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

type sub struct {
	org, repo string
}

func realMain(ctx context.Context) error {
	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		return errors.New("must set GITHUB_TOKEN")
	}

	c := github.NewClient(oauth2.NewClient(ctx, oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: token},
	)))

	return tea.NewProgram(newModel(c)).Start()
}

func getSubs(ctx context.Context, c *github.Client) ([]sub, error) {
	var subs []sub

	opts := &github.ListOptions{
		PerPage: 100,
	}

	for {
		repos, res, err := c.Activity.ListWatched(ctx, "", opts)
		if err != nil {
			return nil, fmt.Errorf("fetching page %d of watched repos: %w", opts.Page, err)
		}

		if subs == nil {
			subs = make([]sub, 0, res.LastPage*opts.PerPage)
		}

		for _, r := range repos {
			subs = append(subs, sub{*r.Owner.Login, *r.Name})
		}

		if res.NextPage == 0 {
			break
		}
		opts.Page = res.NextPage
	}

	sort.Slice(subs, func(i, j int) bool {
		a, b := subs[i], subs[j]
		if a.org == b.org {
			return a.repo < b.repo
		}
		return a.org < b.org
	})

	return subs, nil
}

const (
	colSub  = "sub"
	colOrg  = "org"
	colRepo = "repo"
)

type state int

const (
	stateLoading state = iota
	stateError
	stateLoaded
	stateUnwatching
)

type model struct {
	table   table.Model
	spinner spinner.Model
	help    help.Model
	done    chan struct{}
	gh      *github.Client
	err     error
	state   state
}

func newModel(gh *github.Client) tea.Model {
	tbl := table.New([]table.Column{
		table.NewFlexColumn(colOrg, "Organization", 1),
		table.NewFlexColumn(colRepo, "Repository", 2),
	}).SelectableRows(true).
		WithBaseStyle(lipgloss.NewStyle().Align(lipgloss.Left))

	m := model{
		table:   tbl,
		spinner: spinner.New(),
		help:    help.New(),
		done:    make(chan struct{}),
		gh:      gh,
	}

	return m
}

func (m model) Init() tea.Cmd {
	return tea.Batch(tea.EnterAltScreen, m.spinner.Tick, m.loadSubs)
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd

	switch msg := msg.(type) {
	case error:
		m.err = msg
		m.state = stateError
		return m, nil

	case tea.KeyMsg:
		switch {
		case key.Matches(msg, km.Quit):
			return m, tea.Quit

		case key.Matches(msg, km.Exec):
			rows := m.table.SelectedRows()
			subs := make([]sub, len(rows))
			for i, r := range rows {
				subs[i] = r.Data[colSub].(sub)
			}
			m.state = stateUnwatching
			return m, m.unwatch(subs)
		}

	case tea.WindowSizeMsg:
		hh := lipgloss.Height(m.help.ShortHelpView(km.ShortHelp()))
		m.table = m.table.WithTargetWidth(msg.Width).WithPageSize(msg.Height - 6 - hh)

	case spinner.TickMsg:
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case subsLoadedMsg:
		if msg.err != nil {
			m.state = stateError
			return m, nil
		}

		rows := make([]table.Row, len(msg.subs))
		for i, s := range msg.subs {
			rows[i] = table.NewRow(table.RowData{
				colSub:  s,
				colOrg:  s.org,
				colRepo: s.repo,
			})
		}

		m.table = m.table.WithRows(rows).Focused(true)
		m.state = stateLoaded
	}

	m.table, cmd = m.table.Update(msg)
	return m, cmd
}

func (m model) View() string {
	switch m.state {
	case stateError:
		return fmt.Sprintf("Error: %v\n", m.err)

	case stateLoaded:
		return lipgloss.JoinVertical(lipgloss.Left,
			m.table.View(),
			m.help.ShortHelpView(km.ShortHelp()))

	case stateLoading:
		return fmt.Sprintf("Loading subscriptions %s\n", m.spinner.View())

	case stateUnwatching:
		return fmt.Sprintf("Unwatching marked subscriptions %s\n", m.spinner.View())

	default:
		return "Invalid state!"
	}
}

type subsLoadedMsg struct {
	subs []sub
	err  error
}

func (m model) loadSubs() tea.Msg {
	var msg subsLoadedMsg

	msg.subs, msg.err = getSubs(context.TODO(), m.gh)

	return msg
}

func (m model) unwatch(subs []sub) tea.Cmd {
	ctx := context.TODO()
	for _, s := range subs {
		_, err := m.gh.Activity.DeleteRepositorySubscription(ctx, s.org, s.repo)
		if err != nil {
			return func() tea.Msg {
				return fmt.Errorf("unwatching %s/%s: %w", s.org, s.repo, err)
			}
		}
	}

	return m.loadSubs
}

type keyMap struct {
	Quit, Mark, Exec key.Binding
}

func (km keyMap) ShortHelp() []key.Binding {
	return []key.Binding{km.Mark, km.Exec, km.Quit}
}

func (km keyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{km.ShortHelp()}
}

var km = keyMap{
	Mark: key.NewBinding(
		key.WithKeys(" "),
		key.WithHelp("space", "toggle mark")),
	Quit: key.NewBinding(
		key.WithKeys("q"),
		key.WithHelp("q", "quit")),
	Exec: key.NewBinding(
		key.WithKeys("x"),
		key.WithHelp("x", "unwatch")),
}
