package main

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v4"
	log "github.com/sirupsen/logrus"
)

// getVarcharLengths queries the database for VARCHAR type columns to determine their actual maximum
// lengths. The result map has keys that are table identifiers, and each one of those has a value
// that is a map of column identifiers to the maximum length of the column. Since Redshift always
// converts all letters to lowercase for table or column identifiers, the returned identifiers are
// also all lowercase.
func getVarcharLengths(ctx context.Context, conn *pgx.Conn) (map[string]map[string]int, error) {
	out := make(map[string]map[string]int)

	q := `
	SELECT
		c.table_schema,
		c.table_name,
		c.column_name,
		c.character_maximum_length
	FROM information_schema.columns c
	WHERE
		c.table_schema != 'pg_catalog' AND
		c.table_schema != 'information_schema' AND
		c.table_schema != 'pg_internal' AND
		c.data_type = 'character varying';
	`

	n := 0
	rows, err := conn.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("querying string column lengths: %w", err)
	}
	for rows.Next() {
		var schema, tableName, columnName string
		var maxLength int
		if err := rows.Scan(&schema, &tableName, &columnName, &maxLength); err != nil {
			return nil, fmt.Errorf("scanning row: %w", err)
		}

		tableIdentifer := rsDialect.Identifier(schema, tableName)
		columnIdentifier := rsDialect.Identifier(columnName)

		log.WithFields(log.Fields{
			"table":  tableIdentifer,
			"column": columnIdentifier,
			"length": maxLength,
		}).Debug("queried VARCHAR column length")

		if out[tableIdentifer] == nil {
			out[tableIdentifer] = make(map[string]int)
		}
		out[tableIdentifer][columnIdentifier] = maxLength
		n++
	}

	log.WithFields(log.Fields{
		"tables":  len(out),
		"columns": n,
	}).Info("queried VARCHAR column lengths")

	return out, nil
}