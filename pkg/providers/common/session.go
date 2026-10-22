// PicoClaw - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 PicoClaw contributors

package common

import "strings"

const (
	// SessionKeyOption carries the per-conversation session key through the
	// LLM call options.
	SessionKeyOption = "session_key"

	// SessionKeyPlaceholder is expanded in custom header values.
	SessionKeyPlaceholder = "{{session_key}}"

	// DefaultSessionKey keeps header templates stable for calls that happen
	// outside an agent turn, such as model tests and background jobs.
	DefaultSessionKey = "picoclaw-default"
)

// ResolveSessionKey returns the per-request session key, falling back to a
// stable default when the caller has no session context.
func ResolveSessionKey(options map[string]any) string {
	if key, ok := options[SessionKeyOption].(string); ok && strings.TrimSpace(key) != "" {
		return key
	}
	return DefaultSessionKey
}
