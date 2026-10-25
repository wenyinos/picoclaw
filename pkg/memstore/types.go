// Package memstore implements PicoClaw's long-term memory store.
//
// It is intentionally independent from the session JSONL store: sessions hold
// raw conversation history, while this package holds deduplicated, classified
// long-term memories backed by SQLite.
package memstore

import (
	"path/filepath"
	"sync"
	"time"
)

// Realm separates what is true about the user from role-play material.
const (
	RealmReal      = "real"
	RealmFictional = "fictional"
	RealmWork      = "work"
	RealmUnknown   = "unknown"
)

// Layer is the memory's role in the prompt.
const (
	LayerPersona      = "persona"
	LayerProfile      = "profile"
	LayerRelationship = "relationship"
	LayerEpisodic     = "episodic"
	LayerProcedural   = "procedural"
	LayerBoundary     = "boundary"
)

// Status is the lifecycle of a memory entry.
const (
	StatusActive     = "active"
	StatusSuperseded = "superseded"
	StatusArchived   = "archived"
	StatusPending    = "pending_review"
)

// Config controls the memory subsystem. It is copied from config.MemoryConfig.
type Config struct {
	Enabled               bool
	DBPath                string
	BudgetTokens          int
	AutoExtract           bool
	ExtractModel          string
	PrivateChannels       []string
	InjectFictional       bool
	ReviewRequired        bool
	EpisodicRetentionDays int
}

// DefaultConfig mirrors config defaults for callers without a config file.
func DefaultConfig(workspace string) Config {
	return Config{
		Enabled:               true,
		DBPath:                "memory/memory.db",
		BudgetTokens:          1200,
		AutoExtract:           true,
		PrivateChannels:       []string{"pico"},
		InjectFictional:       false,
		ReviewRequired:        true,
		EpisodicRetentionDays: 365,
	}
}

// Entry is one long-term memory.
type Entry struct {
	ID              string         `json:"id"`
	Realm           string         `json:"realm"`
	Layer           string         `json:"layer"`
	Scope           string         `json:"scope"`
	Audience        string         `json:"audience"`
	Subject         string         `json:"subject,omitempty"`
	Predicate       string         `json:"predicate,omitempty"`
	Value           string         `json:"value,omitempty"`
	Text            string         `json:"text"`
	Normalized      string         `json:"normalized,omitempty"`
	ContentHash     string         `json:"content_hash,omitempty"`
	Simhash         int64          `json:"simhash,omitempty"`
	TemplateKey     string         `json:"template_key,omitempty"`
	ClusterID       string         `json:"cluster_id,omitempty"`
	Occurrences     int            `json:"occurrences"`
	Importance      float64        `json:"importance"`
	EmotionalWeight float64        `json:"emotional_weight"`
	Valence         float64        `json:"valence"`
	Confidence      float64        `json:"confidence"`
	Status          string         `json:"status"`
	EventAt         *int64         `json:"event_at,omitempty"`
	ExpiresAt       *int64         `json:"expires_at,omitempty"`
	CreatedAt       int64          `json:"created_at"`
	UpdatedAt       int64          `json:"updated_at"`
	LastUsedAt      *int64         `json:"last_used_at,omitempty"`
	UseCount        int            `json:"use_count"`
	Attributes      map[string]any `json:"attributes,omitempty"`
	Sources         []Source       `json:"sources,omitempty"`
}

// Source records where a memory came from.
type Source struct {
	EntryID    string `json:"entry_id"`
	SourceType string `json:"source_type"`
	SourceID   string `json:"source_id,omitempty"`
	SessionKey string `json:"session_key,omitempty"`
	CreatedAt  int64  `json:"created_at"`
}

// AddInput is the input to Store.Add.
type AddInput struct {
	Text            string
	Realm           string
	Layer           string
	Scope           string
	Audience        string
	Subject         string
	Predicate       string
	Value           string
	Importance      float64
	EmotionalWeight float64
	Valence         float64
	Confidence      float64
	ExpiresAt       *int64
	EventAt         *int64
	TemplateKey     string
	ForceStatus     string
	SourceType      string
	SourceID        string
	SessionKey      string
	Attributes      map[string]any
}

// AddResult reports how a write was resolved.
type AddResult struct {
	Action     string `json:"action"` // added|merged|duplicate|superseded
	Entry      *Entry `json:"entry,omitempty"`
	ExistingID string `json:"existing_id,omitempty"`
}

// SearchOptions filters entry listing and retrieval.
type SearchOptions struct {
	Query           string
	Realm           string
	Layer           string
	Status          string
	Audience        string
	Scope           string
	Channel         string
	PrivateChannels []string
	Limit           int
	Offset          int
	IncludeFic      bool
}

// Overview is the dashboard payload.
type Overview struct {
	Total          int   `json:"total"`
	Active         int   `json:"active"`
	Pending        int   `json:"pending"`
	Superseded     int   `json:"superseded"`
	Archived       int   `json:"archived"`
	InjectedTokens int   `json:"injected_tokens"`
	Revision       int64 `json:"revision"`
	DBSize         int64 `json:"db_size"`
}

// RelationshipState is a current relationship attribute.
type RelationshipState struct {
	Key        string  `json:"key"`
	Value      string  `json:"value"`
	ValidFrom  int64   `json:"valid_from"`
	Confidence float64 `json:"confidence"`
}

// Persona is a versioned character definition.
type Persona struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Version   int    `json:"version"`
	Content   string `json:"content"`
	Active    bool   `json:"active"`
	CreatedAt int64  `json:"created_at"`
}

// ImportReport describes a one-shot legacy import.
type ImportReport struct {
	TaskRows          int `json:"task_rows"`
	TaskUnique        int `json:"task_unique"`
	TaskImported      int `json:"task_imported"`
	TaskMerged        int `json:"task_merged"`
	TemplateFolded    int `json:"template_folded"`
	ChitchatSkipped   int `json:"chitchat_skipped"`
	FailuresArchived  int `json:"failures_archived"`
	SummariesImported int `json:"summaries_imported"`
	SummariesMerged   int `json:"summaries_merged"`
	LegacyNotes       int `json:"legacy_notes"`
}

var stores sync.Map // db path -> *Store

// OpenForWorkspace returns a cached store for workspace.
func OpenForWorkspace(workspace string, cfg Config) (*Store, error) {
	path := cfg.DBPath
	if path == "" {
		path = "memory/memory.db"
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(workspace, path)
	}
	if v, ok := stores.Load(path); ok {
		return v.(*Store), nil
	}
	s, err := Open(path)
	if err != nil {
		return nil, err
	}
	actual, _ := stores.LoadOrStore(path, s)
	return actual.(*Store), nil
}

func millis(t time.Time) int64 { return t.UnixMilli() }
