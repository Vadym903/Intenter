package storage

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// ErrNotFound is returned when a requested row does not exist; the IPC layer
// maps it to the NOT_FOUND error code.
var ErrNotFound = errors.New("storage: not found")

// Store bundles the repositories the daemon writes through.
type Store struct {
	DB             *DB
	Projects       *ProjectRepo
	Approvals      *ApprovalRepo
	Conditions     *ConditionRepo
	ApprovalEvents *ApprovalEventRepo
	Audit          *AuditRepo
	Imports        *ImportRepo
	Meta           *MetaRepo
}

// NewStore builds the repository set for an open database.
func NewStore(db *DB) *Store {
	return &Store{
		DB:             db,
		Projects:       &ProjectRepo{db: db},
		Approvals:      &ApprovalRepo{db: db},
		Conditions:     &ConditionRepo{db: db},
		ApprovalEvents: &ApprovalEventRepo{db: db},
		Audit:          &AuditRepo{db: db},
		Imports:        &ImportRepo{db: db},
		Meta:           &MetaRepo{db: db},
	}
}

// Close releases the underlying database.
func (s *Store) Close() error {
	if s == nil {
		return nil
	}
	return s.DB.Close()
}

// encodeJSON marshals a value for a JSON column. Nil slices are stored as "[]"
// so readers never have to distinguish null from empty.
func encodeJSON(v any) (string, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("storage: encode column: %w", err)
	}
	if string(raw) == "null" {
		return "[]", nil
	}
	return string(raw), nil
}

// encodeJSONNullable marshals a value, returning SQL NULL for nil/empty input.
func encodeJSONNullable(v any) (sql.NullString, error) {
	if v == nil {
		return sql.NullString{}, nil
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return sql.NullString{}, fmt.Errorf("storage: encode column: %w", err)
	}
	if string(raw) == "null" {
		return sql.NullString{}, nil
	}
	return sql.NullString{String: string(raw), Valid: true}, nil
}

// decodeJSON unmarshals a JSON column, tolerating NULL and empty strings.
func decodeJSON(value sql.NullString, target any) error {
	if !value.Valid || value.String == "" || value.String == "null" {
		return nil
	}
	if err := json.Unmarshal([]byte(value.String), target); err != nil {
		return fmt.Errorf("storage: decode column: %w", err)
	}
	return nil
}

func nullString(value string) sql.NullString {
	if value == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: value, Valid: true}
}

func nullInt64(value *int64) sql.NullInt64 {
	if value == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: *value, Valid: true}
}

func int64Ptr(value sql.NullInt64) *int64 {
	if !value.Valid {
		return nil
	}
	out := value.Int64
	return &out
}

func nullTime(value *time.Time) sql.NullString {
	if value == nil || value.IsZero() {
		return sql.NullString{}
	}
	return sql.NullString{String: formatTime(*value), Valid: true}
}

func timePtr(value sql.NullString) (*time.Time, error) {
	if !value.Valid || value.String == "" {
		return nil, nil
	}
	parsed, err := parseTime(value.String)
	if err != nil {
		return nil, err
	}
	return &parsed, nil
}
