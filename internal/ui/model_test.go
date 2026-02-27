package ui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/tdawe1/gengowatcher-go/pkg/gengo"
)

func TestModel_UpdateJobFoundPrependsAndUpdatesStats(t *testing.T) {
	m := NewModel()
	ev := gengo.NewJobEvent(gengo.EventJobFound, gengo.SourceWebSocket, &gengo.Job{ID: "job-1", Title: "JP -> EN", Reward: 12.5})

	updated, _ := m.Update(jobFoundMsg{Event: ev})
	next := updated.(Model)

	if len(next.jobs) != 1 || next.jobs[0].ID != "job-1" {
		t.Fatalf("expected prepended job, got %#v", next.jobs)
	}
	if next.stats.TotalFound != 1 || next.stats.BySource[gengo.SourceWebSocket] != 1 {
		t.Fatalf("unexpected stats: %#v", next.stats)
	}
}

func TestModel_TabNavigation(t *testing.T) {
	tests := []struct {
		name string
		key  string
		want tab
	}{
		{name: "jobs tab", key: "1", want: tabJobs},
		{name: "stats tab", key: "2", want: tabStats},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := NewModel()
			updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(tt.key)})
			next := updated.(Model)
			if next.currentTab != tt.want {
				t.Fatalf("expected tab %v, got %v", tt.want, next.currentTab)
			}
		})
	}
}

func TestModel_QuitKeys(t *testing.T) {
	m := NewModel()

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	next := updated.(Model)
	if !next.quitting {
		t.Fatal("expected quitting=true after q")
	}

	m = NewModel()
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	next = updated.(Model)
	if !next.quitting {
		t.Fatal("expected quitting=true after ctrl+c")
	}
}

func TestModel_JobListCapsAt200(t *testing.T) {
	m := NewModel()

	for i := range 205 {
		id := fmt.Sprintf("job-%03d", i)
		ev := gengo.NewJobEvent(gengo.EventJobFound, gengo.SourceRSS, &gengo.Job{ID: id})
		updated, _ := m.Update(jobFoundMsg{Event: ev})
		m = updated.(Model)
	}

	if got := len(m.jobs); got != 200 {
		t.Fatalf("expected 200 jobs, got %d", got)
	}
	if m.jobs[0].ID != "job-204" {
		t.Fatalf("expected newest job at index 0, got %q", m.jobs[0].ID)
	}
	if m.jobs[199].ID != "job-005" {
		t.Fatalf("expected oldest retained job at index 199, got %q", m.jobs[199].ID)
	}
}

func TestModel_ViewIsNotEmpty(t *testing.T) {
	m := NewModel()
	view := m.View()
	if strings.TrimSpace(view) == "" {
		t.Fatal("expected non-empty view")
	}
}

func TestModel_ViewRendersJobsAndStatsTabs(t *testing.T) {
	m := NewModel()
	ev := gengo.NewJobEvent(gengo.EventJobFound, gengo.SourceWebSocket, &gengo.Job{ID: "job-1", Title: "JP -> EN", Reward: 12.5})

	updated, _ := m.Update(jobFoundMsg{Event: ev})
	m = updated.(Model)

	jobsView := m.View()
	if !strings.Contains(jobsView, "Jobs") || !strings.Contains(jobsView, "job-1") {
		t.Fatalf("expected jobs view to include tab and job row, got %q", jobsView)
	}

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("2")})
	m = updated.(Model)

	statsView := m.View()
	if !strings.Contains(statsView, "Stats") || !strings.Contains(statsView, "Total Found: 1") {
		t.Fatalf("expected stats view to include tab and totals, got %q", statsView)
	}
}
