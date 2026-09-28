package loganalytics

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/libre-devops/ldo-go-cli/internal/core/errs"
	"github.com/libre-devops/ldo-go-cli/internal/core/fields"
	"github.com/libre-devops/ldo-go-cli/internal/core/render"
)

// Which tables a workspace is receiving: when each last got data, and how much.
//
// This reads the workspace's Usage table, which holds one record per table (its
// DataType) for each hour something was ingested. That makes it cheap to ask, since it
// never reads the tables themselves, and accurate to the hour, not the event. It also
// means a table that received nothing in the whole window is not listed at all, so the
// window defaults to 30 days; and Usage itself arrives a little behind (up to an hour or
// so), so being quiet for less than a couple of hours is not a sign of anything. Sizes
// are as Usage counts them, in gigabytes of 1000 MB; billable excludes the free tables.

// DefaultWindow is how far back ingestion looks; DefaultQuietAfter when a table is quiet.
const (
	DefaultWindow     = 30 * 24 * time.Hour
	DefaultQuietAfter = 24 * time.Hour
	// MaxWindow is two years: Usage keeps what the workspace keeps, and that is the most.
	MaxWindow = 730 * 24 * time.Hour
)

const ingestionKQL = `Usage
| where TimeGenerated > ago(%dh)
| summarize LastData = max(EndTime), Megabytes = sum(Quantity),
    BillableMegabytes = sumif(Quantity, IsBillable == true),
    Solutions = strcat_array(make_set(Solution, 10), ", ")
    by DataType
| order by Megabytes desc`

// Table is one table's ingestion over the window: when it last received data (to the
// hour), how much, how much of that is billable, and the solutions that send it.
type Table struct {
	Table             string
	LastData          time.Time
	Gigabytes         float64
	BillableGigabytes float64
	Solutions         []string
}

// QuietFor is how long since the table last received data, and false when not known.
func (t Table) QuietFor(now time.Time) (time.Duration, bool) {
	if t.LastData.IsZero() {
		return 0, false
	}
	return max(now.Sub(t.LastData), 0), true
}

// Quiet reports whether the table has received nothing for longer than after.
func (t Table) Quiet(now time.Time, after time.Duration) bool {
	silence, known := t.QuietFor(now)
	return known && silence > after
}

// IngestionQuery is the KQL: each table's last data, and its size, over window (whole
// hours).
func IngestionQuery(window time.Duration) (string, error) {
	hours := int64(window / time.Hour)
	if hours < 1 || hours > int64(MaxWindow/time.Hour) {
		return "", errs.Inputf("the window must be from 1 hour to 730 days").WithHint("e.g. 30d")
	}
	return fmt.Sprintf(ingestionKQL, hours), nil
}

// ReadIngestion is a result of IngestionQuery as one entry per table, largest first.
func ReadIngestion(result render.QueryResult) []Table {
	var tables []Table
	for _, row := range result.Rows {
		name := fields.Text(row, "DataType")
		if name == "" {
			continue
		}
		megabytes, _ := fields.Number(row["Megabytes"])
		billable, _ := fields.Number(row["BillableMegabytes"])
		var solutions []string
		for _, item := range strings.Split(fields.Text(row, "Solutions"), ",") {
			if item = strings.TrimSpace(item); item != "" {
				solutions = append(solutions, item)
			}
		}
		tables = append(tables, Table{Table: name, LastData: fields.When(row, "LastData"), Gigabytes: megabytes / 1000,
			BillableGigabytes: billable / 1000, Solutions: solutions})
	}
	return tables
}

// ByQuietest is quiet tables first, the longest quiet first; then the rest, largest first.
func ByQuietest(tables []Table, now time.Time, after time.Duration) []Table {
	var quiet, rest []Table
	for _, table := range tables {
		if table.Quiet(now, after) {
			quiet = append(quiet, table)
		} else {
			rest = append(rest, table)
		}
	}
	sort.SliceStable(quiet, func(a, b int) bool { return quiet[a].LastData.Before(quiet[b].LastData) })
	sort.SliceStable(rest, func(a, b int) bool { return rest[a].Gigabytes > rest[b].Gigabytes })
	return append(quiet, rest...)
}
