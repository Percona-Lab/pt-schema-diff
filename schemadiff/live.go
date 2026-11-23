package schemadiff

// Reading schemas from a running MySQL server. Only SHOW DATABASES, information_schema.TABLES and SHOW CREATE TABLE
// are used, so a read-only login is enough.

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
)

// SystemSchemas are left out of database lists.
var SystemSchemas = map[string]bool{"mysql": true, "sys": true, "information_schema": true, "performance_schema": true}

// Queryer is what reading a schema needs: a *sql.DB or a *sql.Conn.
type Queryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// ListDatabases returns the user databases of a server.
func ListDatabases(ctx context.Context, db Queryer) ([]string, error) {
	rows, err := db.QueryContext(ctx, "SHOW DATABASES")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		if !SystemSchemas[strings.ToLower(name)] {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out, rows.Err()
}

// FetchSchema returns {table: SHOW CREATE TABLE} and the approximate sizes of the base tables of a database.
func FetchSchema(ctx context.Context, db Queryer, database string) (map[string]string, Sizes, error) {
	rows, err := db.QueryContext(ctx, "SELECT TABLE_NAME, TABLE_ROWS, DATA_LENGTH + INDEX_LENGTH FROM information_schema.TABLES "+
		"WHERE TABLE_SCHEMA = ? AND TABLE_TYPE = 'BASE TABLE' ORDER BY TABLE_NAME", database)
	if err != nil {
		return nil, nil, err
	}
	type info struct {
		name        string
		rows, bytes sql.NullInt64
	}
	var list []info
	for rows.Next() {
		var i info
		if err := rows.Scan(&i.name, &i.rows, &i.bytes); err != nil {
			rows.Close()
			return nil, nil, err
		}
		list = append(list, i)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	tables, sizes := map[string]string{}, Sizes{}
	for _, i := range list {
		var name, ddl string
		err := db.QueryRowContext(ctx, "SHOW CREATE TABLE "+Quote(database)+"."+Quote(i.name)).Scan(&name, &ddl)
		if err == sql.ErrNoRows {
			continue // dropped meanwhile
		}
		if err != nil {
			return nil, nil, fmt.Errorf("SHOW CREATE TABLE %s.%s: %w", Quote(database), Quote(i.name), err)
		}
		tables[i.name] = ddl
		sizes[strings.ToLower(i.name)] = Size{Rows: i.rows.Int64, Bytes: i.bytes.Int64}
	}
	return tables, sizes, nil
}

// ServerVersion returns SELECT VERSION().
func ServerVersion(ctx context.Context, db Queryer) (string, error) {
	var v string
	err := db.QueryRowContext(ctx, "SELECT VERSION()").Scan(&v)
	return v, err
}
