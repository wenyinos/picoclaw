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

// Time precision describes how exact an event timestamp is.
const (
	PrecisionExact       = "exact"
	PrecisionDate        = "date"
	PrecisionApproximate = "approximate"
	PrecisionRange       = "range"
	PrecisionNarrative   = "narrative"
	PrecisionPlanned     = "planned"
	PrecisionUnknown     = "unknown"
)

// Source types grade how a memory was obtained.
const (
	SourceExplicit     = "explicit"
	SourceInferred     = "inferred"
	SourceConfirmed    = "confirmed"
	SourceManual       = "manual"
	SourceTaskRecord   = "task_record"
	SourceImported     = "imported"
	SourceReclassified = "reclassified"
)

// Relation types describe edges between memories.
const (
	RelSupersedes   = "supersedes"
	RelContradicts  = "contradicts"
	RelRelatedTo    = "related_to"
	RelCausedBy     = "caused_by"
	RelInfluencedBy = "influenced_by"
	RelPartOf       = "part_of"
	RelFulfilledBy  = "fulfilled_by"
	RelRevisitedBy  = "revisited_by"
	RelNextScene    = "next_scene"
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
	Facet           string         `json:"facet,omitempty"`
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

	EventTimePrecision string `json:"event_time_precision,omitempty"`
	EventTimezone      string `json:"event_timezone,omitempty"`
	EventTimeSource    string `json:"event_time_source,omitempty"`
	ValidFrom          *int64 `json:"valid_from,omitempty"`
	ValidTo            *int64 `json:"valid_to,omitempty"`
	PinnedCore         bool   `json:"pinned_core,omitempty"`
}

// Source records where a memory came from. It is an evidence record, not just
// a pointer: evidence_text keeps the sentence the memory was extracted from.
type Source struct {
	ID              int64   `json:"id,omitempty"`
	EntryID         string  `json:"entry_id"`
	ConversationID  string  `json:"conversation_id,omitempty"`
	SessionKey      string  `json:"session_key,omitempty"`
	MessageID       *int64  `json:"message_id,omitempty"`
	SourceID        string  `json:"source_id,omitempty"`
	SourceType      string  `json:"source_type"`
	EvidenceText    string  `json:"evidence_text,omitempty"`
	ExtractionModel string  `json:"extraction_model,omitempty"`
	PromptVersion   string  `json:"prompt_version,omitempty"`
	Confidence      float64 `json:"confidence"`
	UserConfirmed   bool    `json:"user_confirmed"`
	CreatedAt       int64   `json:"created_at"`
}

// Relation is a typed edge between two memories.
type Relation struct {
	ID           int64   `json:"id,omitempty"`
	SourceID     string  `json:"source_id"`
	TargetID     string  `json:"target_id"`
	RelationType string  `json:"relation_type"`
	Confidence   float64 `json:"confidence"`
	SourceType   string  `json:"source_type"`
	CreatedAt    int64   `json:"created_at"`
}

// Cluster groups memories into a scene capsule.
type Cluster struct {
	ID        string `json:"id"`
	Realm     string `json:"realm"`
	Title     string `json:"title"`
	Topic     string `json:"topic"`
	Status    string `json:"status"`
	CreatedAt int64  `json:"created_at"`
	UpdatedAt int64  `json:"updated_at"`
}

// CapsuleItem is one memory rendered inside a scene capsule.
type CapsuleItem struct {
	EntryID string `json:"entry_id"`
	Ordinal int    `json:"ordinal"`
	Layer   string `json:"layer"`
	Realm   string `json:"realm"`
	Text    string `json:"text"`
	Tokens  int    `json:"tokens"`
}

// CapsuleResult is the rendered scene capsule.
type CapsuleResult struct {
	ID          string        `json:"id"`
	Title       string        `json:"title"`
	Items       []CapsuleItem `json:"items"`
	TotalTokens int           `json:"total_tokens"`
	Truncated   bool          `json:"truncated"`
}

// OperationLog is one governance action. It never stores memory text.
type OperationLog struct {
	ID                 int64  `json:"id,omitempty"`
	Operation          string `json:"operation"`
	TargetEntryID      string `json:"target_entry_id,omitempty"`
	ReplacementEntryID string `json:"replacement_entry_id,omitempty"`
	ContentHash        string `json:"content_hash,omitempty"`
	Reason             string `json:"reason,omitempty"`
	AffectedCount      int    `json:"affected_count"`
	CreatedAt          int64  `json:"created_at"`
}

// InspectResult is the evidence chain for one memory.
type InspectResult struct {
	Entry      Entry          `json:"entry"`
	Sources    []Source       `json:"sources"`
	Supersedes []Relation     `json:"supersedes"`
	Relations  []Relation     `json:"relations"`
	Operations []OperationLog `json:"operations"`
	Clusters   []string       `json:"clusters"`
}

// ForgetPreview reports what a hard delete would remove.
type ForgetPreview struct {
	EntryID     string   `json:"entry_id"`
	ContentHash string   `json:"content_hash"`
	Sources     int      `json:"sources"`
	Relations   int      `json:"relations"`
	ClusterIDs  []string `json:"cluster_ids"`
	Summaries   int      `json:"summaries"`
	AlreadyGone bool     `json:"already_gone"`
}

// FixedUsage accounts for the fixed prompt block and the boundary block.
type FixedUsage struct {
	BaseTokens     int `json:"base_tokens"`
	BoundaryTokens int `json:"boundary_tokens"`
	TotalTokens    int `json:"total_tokens"`
	BoundaryBudget int `json:"boundary_budget"`
	BoundaryCut    int `json:"boundary_cut"`
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
	Facet           string
	Value           string
	Importance      float64
	EmotionalWeight float64
	Valence         float64
	Confidence      float64
	ExpiresAt       *int64
	EventAt         *int64
	TemplateKey     string
	ForceStatus     string
	SupersedesID    string
	SourceType      string
	SourceID        string
	SessionKey      string
	Attributes      map[string]any

	EventTimePrecision    string
	EventTimezone         string
	EventTimeSource       string
	ValidFrom             *int64
	ValidTo               *int64
	PinnedCore            bool
	EvidenceText          string
	ModelID               string
	PromptVersion         string
	ConversationID        string
	MessageID             *int64
	SourceConfirmed       bool
	BypassForgetTombstone bool
	// Force allows Correct to replace a pinned_core entry. Automatic
	// extraction never sets it.
	Force bool
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
	Predicate       string
	Facet           string
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
	ExpiresAt  *int64  `json:"expires_at,omitempty"`
}

// RelationshipHistory is one archived version of a relationship attribute.
type RelationshipHistory struct {
	Key        string  `json:"key"`
	Value      string  `json:"value"`
	ValidFrom  int64   `json:"valid_from"`
	ValidTo    int64   `json:"valid_to"`
	Confidence float64 `json:"confidence"`
	UpdatedAt  int64   `json:"updated_at"`
}

// Persona is a versioned character definition.
type Persona struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Version   int    `json:"version"`
	Content   string `json:"content"`
	Active    bool   `json:"active"`
	Realm     string `json:"realm"`
	Audience  string `json:"audience"`
	Scope     string `json:"scope"`
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
