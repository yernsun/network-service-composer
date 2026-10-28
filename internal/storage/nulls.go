package storage

import (
	"database/sql"
	"time"
)

type sqlNullString struct {
	value string
	valid bool
}

func (n *sqlNullString) Scan(src any) error {
	var value sql.NullString
	if err := value.Scan(src); err != nil {
		return err
	}
	n.value, n.valid = value.String, value.Valid
	return nil
}

type sqlNullInt struct {
	value int64
	valid bool
}

func (n *sqlNullInt) Scan(src any) error {
	var value sql.NullInt64
	if err := value.Scan(src); err != nil {
		return err
	}
	n.value, n.valid = value.Int64, value.Valid
	return nil
}

type sqlNullBool struct {
	value bool
	valid bool
}

func (n *sqlNullBool) Scan(src any) error {
	var value sql.NullBool
	if err := value.Scan(src); err != nil {
		return err
	}
	n.value, n.valid = value.Bool, value.Valid
	return nil
}

type sqlNullTime struct {
	value time.Time
	valid bool
}

func (n *sqlNullTime) Scan(src any) error {
	var value sql.NullTime
	if err := value.Scan(src); err != nil {
		return err
	}
	n.value, n.valid = value.Time, value.Valid
	return nil
}
