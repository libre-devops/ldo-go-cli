package render

// Report is what -o html has gathered so far: every table the command wrote, and the
// notes and warnings around them, to write as one page when the command finishes. A nil
// Report gathers nothing.
type Report struct {
	Tables []ReportTable
	Notes  [][2]string
	// Heading is the command's name (xdr machines); Command is its whole command line.
	Heading string
	Command string
}

// ReportTable is one table of a report.
type ReportTable struct {
	Headers []string
	Rows    [][]Cell
}

func (r *Report) add(kind, text string) {
	if r != nil {
		r.Notes = append(r.Notes, [2]string{kind, text})
	}
}

func (r *Report) gather(headers []string, rows [][]Cell) {
	// The page is written when the command finishes, so that notes after the table (its
	// summary, most often) are on it too.
	if r != nil {
		r.Tables = append(r.Tables, ReportTable{Headers: headers, Rows: rows})
	}
}
