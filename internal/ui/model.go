package ui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/tdawe1/gengowatcher-go/pkg/gengo"
)

const maxJobs = 200

type tab int

const (
	tabJobs tab = iota
	tabStats
)

type jobFoundMsg struct {
	Event gengo.JobEvent
}

type Model struct {
	jobs       []*gengo.Job
	stats      *gengo.Stats
	currentTab tab
	quitting   bool
	done       <-chan struct{}
	events     <-chan gengo.JobEvent
}

func (m Model) Init() tea.Cmd {
	if m.events != nil {
		return waitForJobEventWithDone(m.done, m.events)
	}

	return nil
}

func (m Model) View() string {
	var b strings.Builder
	b.WriteString("GengoWatcher\n")

	switch m.currentTab {
	case tabStats:
		b.WriteString("Tab: Stats\n")
		totalFound := 0
		sessionFound := 0
		bySource := map[gengo.Source]int{}
		if m.stats != nil {
			totalFound = m.stats.TotalFound
			sessionFound = m.stats.SessionFound
			bySource = m.stats.BySource
		}

		b.WriteString(fmt.Sprintf("Total Found: %d\n", totalFound))
		b.WriteString(fmt.Sprintf("Session Found: %d\n", sessionFound))
		b.WriteString(fmt.Sprintf("WebSocket: %d\n", bySource[gengo.SourceWebSocket]))
		b.WriteString(fmt.Sprintf("RSS: %d\n", bySource[gengo.SourceRSS]))
	default:
		b.WriteString("Tab: Jobs\n")
		if len(m.jobs) == 0 {
			b.WriteString("No jobs yet.\n")
		} else {
			limit := len(m.jobs)
			if limit > 10 {
				limit = 10
			}

			for i := range limit {
				job := m.jobs[i]
				if job == nil {
					continue
				}
				b.WriteString(fmt.Sprintf("- %s | %s | %.2f\n", job.ID, job.Title, job.Reward))
			}
		}
	}

	b.WriteString("Keys: 1 Jobs, 2 Stats, q Quit\n")

	if m.quitting {
		b.WriteString("Quitting...\n")
	}

	return b.String()
}

func NewModel() Model {
	return Model{
		stats:      gengo.NewStats(),
		currentTab: tabJobs,
	}
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case jobFoundMsg:
		if msg.Event.Job == nil {
			if m.events != nil {
				return m, waitForJobEventWithDone(m.done, m.events)
			}
			return m, nil
		}

		m.jobs = append([]*gengo.Job{msg.Event.Job}, m.jobs...)
		if len(m.jobs) > maxJobs {
			m.jobs = m.jobs[:maxJobs]
		}
		m.stats.Increment(msg.Event.Source)
		if m.events != nil {
			return m, waitForJobEventWithDone(m.done, m.events)
		}
		return m, nil
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			m.quitting = true
			return m, tea.Quit
		case "1":
			m.currentTab = tabJobs
		case "2":
			m.currentTab = tabStats
		}
	}

	return m, nil
}
