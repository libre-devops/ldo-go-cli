package render

import (
	"encoding/json"
	"fmt"
	"slices"
)

// QueryResult is rows keyed by column name, with the columns in the order the query
// produced them: one shape for Advanced Hunting, Resource Graph and Log Analytics.
// Truncated is true when more rows existed than were fetched; Warnings is anything the
// service said about a partial result.
type QueryResult struct {
	Columns   []string
	Rows      []map[string]any
	Truncated bool
	Warnings  []string
}

// FromRecords is a result from a list of objects, its columns in first-seen order.
func FromRecords(records []map[string]any, truncated bool) QueryResult {
	var columns []string
	seen := map[string]bool{}
	for _, record := range records {
		for _, column := range orderedKeys(record) {
			if !seen[column] {
				seen[column] = true
				columns = append(columns, column)
			}
		}
	}
	return QueryResult{Columns: columns, Rows: records, Truncated: truncated}
}

// orderedKeys is a record's keys; Go maps have no order, so they are sorted.
func orderedKeys(record map[string]any) []string {
	keys := make([]string, 0, len(record))
	for key := range record {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

// FromColumns is a result from column names plus rows of values in that column order.
func FromColumns(columns []string, values [][]any, truncated bool) QueryResult {
	rows := make([]map[string]any, len(values))
	for index, row := range values {
		rows[index] = map[string]any{}
		for at, column := range columns {
			if at < len(row) {
				rows[index][column] = row[at]
			}
		}
	}
	return QueryResult{Columns: columns, Rows: rows, Truncated: truncated}
}

// Query writes a query result: its rows as JSON, else as a table of its columns.
func (c *Console) Query(result QueryResult, output Output) error {
	var err error
	if output == JSON {
		c.jsonIsNotArranged()
		rows := result.Rows
		if rows == nil {
			rows = []map[string]any{}
		}
		err = c.PrintJSON(rows)
	} else {
		rows := make([][]Cell, len(result.Rows))
		for index, row := range result.Rows {
			rows[index] = make([]Cell, len(result.Columns))
			for at, column := range result.Columns {
				rows[index][at] = Plain(queryCell(row[column]))
			}
		}
		err = c.Emit(output, result.Columns, rows, nil)
	}
	for _, warning := range result.Warnings {
		c.Warn("%s", warning)
	}
	if result.Truncated {
		c.Warn("stopped after %d rows; raise --limit to fetch more", len(result.Rows))
	}
	return err
}

func queryCell(value any) string {
	switch v := value.(type) {
	case nil:
		return ""
	case string:
		return v
	case bool, float64, int, int64:
		return fmt.Sprint(v)
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprint(value)
	}
	return string(encoded)
}
