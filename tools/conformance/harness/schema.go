package harness

import (
	"database/sql"
	"fmt"
	"sort"
	"strings"
)

// Catalog describes the public schema in comparable lines: one per column
// (with its position, type, nullability and default), index, constraint and
// sequence. Two databases with equal catalogs have the same structure.
type Catalog struct {
	Lines []string
}

// Catalog reads the structure of the database's public schema.
func (d DB) Catalog() (Catalog, error) {
	db, err := d.Open()
	if err != nil {
		return Catalog{}, err
	}
	defer db.Close()
	var c Catalog
	queries := []struct{ kind, sql string }{
		{"column", `SELECT table_name, lpad(ordinal_position::text, 3, '0'), column_name, data_type,
			COALESCE(character_maximum_length, 0), COALESCE(numeric_precision, 0), is_nullable, COALESCE(column_default, '')
			FROM information_schema.columns WHERE table_schema = 'public'`},
		{"index", `SELECT tablename, indexname, indexdef FROM pg_indexes WHERE schemaname = 'public'`},
		{"constraint", `SELECT conrelid::regclass::text, conname, contype::text, pg_get_constraintdef(oid)
			FROM pg_constraint WHERE connamespace = 'public'::regnamespace`},
		{"sequence", `SELECT sequence_name, data_type, start_value, increment FROM information_schema.sequences WHERE sequence_schema = 'public'`},
	}
	for _, q := range queries {
		lines, err := rowsAsLines(db, q.sql)
		if err != nil {
			return c, fmt.Errorf("catalog %s: %w", q.kind, err)
		}
		for _, l := range lines {
			c.Lines = append(c.Lines, q.kind+" "+l)
		}
	}
	sort.Strings(c.Lines)
	return c, nil
}

func rowsAsLines(db *sql.DB, query string) ([]string, error) {
	rows, err := db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	var out []string
	for rows.Next() {
		vals := make([]sql.NullString, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		parts := make([]string, len(vals))
		for i, v := range vals {
			parts[i] = v.String
		}
		out = append(out, strings.Join(parts, " | "))
	}
	return out, rows.Err()
}

// Without returns the catalog minus every line about the given tables (and
// their indexes, constraints and sequences).
func (c Catalog) Without(tables ...string) Catalog {
	var out Catalog
next:
	for _, l := range c.Lines {
		for _, t := range tables {
			if strings.Contains(l, " "+t+" ") || strings.Contains(l, " "+t+"_") {
				continue next
			}
		}
		out.Lines = append(out.Lines, l)
	}
	return out
}

// WithoutColumns returns the catalog minus the given columns, each named
// "table.column".
func (c Catalog) WithoutColumns(columns ...string) Catalog {
	drop := map[[2]string]bool{}
	for _, tc := range columns {
		table, column, _ := strings.Cut(tc, ".")
		drop[[2]string{"column " + table, column}] = true
	}
	var out Catalog
	for _, l := range c.Lines {
		if parts := strings.Split(l, " | "); len(parts) > 2 && drop[[2]string{parts[0], parts[2]}] {
			continue
		}
		out.Lines = append(out.Lines, l)
	}
	return out
}

// Diff lists the lines only in a ("-") and only in b ("+").
func (a Catalog) Diff(b Catalog) []string {
	count := map[string]int{}
	for _, l := range a.Lines {
		count[l]++
	}
	for _, l := range b.Lines {
		count[l]--
	}
	var out []string
	for l, n := range count {
		switch {
		case n > 0:
			out = append(out, "- "+l)
		case n < 0:
			out = append(out, "+ "+l)
		}
	}
	sort.Strings(out)
	return out
}
