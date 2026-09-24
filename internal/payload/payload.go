// Package payload decodes the status JSON Claude Code sends on stdin.
//
// Leaf values use the Num, Str, and Bool types instead of pointers. With a
// plain *float64, encoding/json allocates the pointer before it notices a type
// mismatch, so `"used_percentage": "90"` would decode as a set value of 0 and
// render as "100% session". These types stay unset unless the JSON type
// matches, and they never return an error, so a surprise in one field never
// blanks the whole status line.
package payload

import (
	"encoding/json"
	"errors"
)

// Num is a JSON number that is Set only when the value really was a number.
type Num struct {
	V   float64
	Set bool
}

// UnmarshalJSON implements json.Unmarshaler.
func (n *Num) UnmarshalJSON(data []byte) error {
	if len(data) > 0 && (data[0] == '-' || (data[0] >= '0' && data[0] <= '9')) {
		n.Set = json.Unmarshal(data, &n.V) == nil
	}
	return nil
}

// Str is a JSON string that is Set only when the value really was a string.
type Str struct {
	V   string
	Set bool
}

// UnmarshalJSON implements json.Unmarshaler.
func (s *Str) UnmarshalJSON(data []byte) error {
	if len(data) > 0 && data[0] == '"' {
		s.Set = json.Unmarshal(data, &s.V) == nil
	}
	return nil
}

// Bool is a JSON boolean that is Set only when the value really was a boolean.
type Bool struct {
	V   bool
	Set bool
}

// UnmarshalJSON implements json.Unmarshaler.
func (b *Bool) UnmarshalJSON(data []byte) error {
	switch string(data) {
	case "true":
		b.V, b.Set = true, true
	case "false":
		b.V, b.Set = false, true
	}
	return nil
}

// Payload is the subset of Claude Code's status JSON that paceline reads.
// Object fields are pointers so a missing object is nil.
type Payload struct {
	Model *struct {
		DisplayName Str `json:"display_name"`
	} `json:"model"`
	Effort *struct {
		Level Str `json:"level"`
	} `json:"effort"`
	FastMode  Bool `json:"fast_mode"`
	Cwd       Str  `json:"cwd"`
	Workspace *struct {
		CurrentDir Str `json:"current_dir"`
		ProjectDir Str `json:"project_dir"`
	} `json:"workspace"`
	RateLimits *struct {
		FiveHour *Limit `json:"five_hour"`
		SevenDay *Limit `json:"seven_day"`
	} `json:"rate_limits"`
	ContextWindow *struct {
		UsedPercentage Num `json:"used_percentage"`
	} `json:"context_window"`
	PromptCache *struct {
		ExpiresAt           Num `json:"expires_at"`
		RecacheTokensIfCold Num `json:"recache_tokens_if_cold"`
	} `json:"prompt_cache"`
	Cost *struct {
		TotalDurationMs Num `json:"total_duration_ms"`
	} `json:"cost"`
}

// Limit is one rate-limit window.
type Limit struct {
	UsedPercentage Num `json:"used_percentage"`
	ResetsAt       Num `json:"resets_at"`
}

// Decode parses data. It fails only on JSON that does not parse at all or
// whose top level is not an object; an object where a nested object was
// expected is skipped, and encoding/json keeps decoding the other fields.
func Decode(data []byte) (*Payload, error) {
	var p Payload
	err := json.Unmarshal(data, &p)
	var typeErr *json.UnmarshalTypeError
	if errors.As(err, &typeErr) && typeErr.Field != "" {
		err = nil
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}
