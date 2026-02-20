// Package gengo provides the public API for GengoWatcher.
// This package contains shared types and interfaces for submodule consumers.
package gengo

import (
	"encoding/json"
	"time"
)

// EventType represents the type of job event.
type EventType string

const (
	// EventJobFound is emitted when a new job is discovered.
	EventJobFound EventType = "job_found"
	// EventJobExpired is emitted when a job is no longer available.
	EventJobExpired EventType = "job_expired"
	// EventError is emitted when a monitor encounters an error.
	EventError EventType = "error"
)

// Source represents where a job was discovered.
type Source string

const (
	SourceWebSocket Source = "websocket"
	SourceRSS       Source = "rss"
	SourceEmail     Source = "email"
	SourceWebsite   Source = "website"
)

// Job represents a translation job from Gengo.
type Job struct {
	// ID is the unique identifier for the job.
	ID string `json:"id"`
	// Title is the job title/description.
	Title string `json:"title"`
	// Source indicates where the job was discovered.
	Source Source `json:"source"`
	// Reward is the payment amount for the job.
	Reward float64 `json:"reward"`
	// Currency is the reward currency (e.g., "USD").
	Currency string `json:"currency"`
	// Language is the language pair (e.g., "en → ja").
	Language string `json:"language"`
	// UnitCount is the number of units (words/characters) in the job.
	UnitCount int `json:"unit_count"`
	// Deadline is when the job must be completed by.
	Deadline time.Time `json:"deadline"`
	// FoundAt is when the job was discovered.
	FoundAt time.Time `json:"found_at"`
	// URL is the link to view/accept the job.
	URL string `json:"url,omitempty"`
	// Payload contains the raw source data for debugging.
	Payload json.RawMessage `json:"payload,omitempty"`
}

// JobEvent represents an event emitted by a monitor.
type JobEvent struct {
	// Type indicates what kind of event this is.
	Type EventType `json:"type"`
	// Job contains the job data (nil for error events).
	Job *Job `json:"job,omitempty"`
	// Source indicates which monitor emitted this event.
	Source Source `json:"source"`
	// Error contains the error message (for error events).
	Error string `json:"error,omitempty"`
	// Time is when the event occurred.
	Time time.Time `json:"time"`
}

// NewJobEvent creates a new JobEvent with the current timestamp.
func NewJobEvent(eventType EventType, source Source, job *Job) JobEvent {
	return JobEvent{
		Type:   eventType,
		Source: source,
		Job:    job,
		Time:   time.Now(),
	}
}

// NewErrorEvent creates a new error JobEvent.
func NewErrorEvent(source Source, err string) JobEvent {
	return JobEvent{
		Type:   EventError,
		Source: source,
		Error:  err,
		Time:   time.Now(),
	}
}

// Stats tracks job discovery statistics.
type Stats struct {
	// TotalFound is the all-time count of jobs discovered.
	TotalFound int `json:"total_found"`
	// SessionFound is the count of jobs found in the current session.
	SessionFound int `json:"session_found"`
	// BySource breaks down counts by discovery source.
	BySource map[Source]int `json:"by_source"`
	// LastUpdated is when stats were last modified.
	LastUpdated time.Time `json:"last_updated"`
}

// NewStats creates a new Stats instance with initialized maps.
func NewStats() *Stats {
	return &Stats{
		BySource: make(map[Source]int),
	}
}

// Increment increments the count for a given source.
func (s *Stats) Increment(source Source) {
	s.TotalFound++
	s.SessionFound++
	s.BySource[source]++
	s.LastUpdated = time.Now()
}

