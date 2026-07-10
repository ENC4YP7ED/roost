package db

import (
	"bufio"
	"context"
	"database/sql"
	"io"
	"strings"
	"time"
)

// SplitStatements splits a SQL script into individual statements on `;`,
// respecting single/double-quoted strings, backtick identifiers, and `--`,
// `#` and `/* */` comments. It does not handle DELIMITER changes (stored
// routines) — those remain a single-statement concern.
func SplitStatements(script string) []string {
	var statements []string
	var cur strings.Builder
	runes := []rune(script)
	n := len(runes)

	flush := func() {
		s := strings.TrimSpace(cur.String())
		if s != "" {
			statements = append(statements, s)
		}
		cur.Reset()
	}

	for i := 0; i < n; i++ {
		c := runes[i]
		switch c {
		case '\'', '"', '`':
			// Consume the quoted span verbatim.
			quote := c
			cur.WriteRune(c)
			for i++; i < n; i++ {
				cur.WriteRune(runes[i])
				if runes[i] == '\\' && quote != '`' && i+1 < n {
					i++
					cur.WriteRune(runes[i])
					continue
				}
				if runes[i] == quote {
					break
				}
			}
		case '-':
			if i+1 < n && runes[i+1] == '-' {
				for i < n && runes[i] != '\n' {
					i++
				}
				cur.WriteRune('\n')
			} else {
				cur.WriteRune(c)
			}
		case '#':
			for i < n && runes[i] != '\n' {
				i++
			}
			cur.WriteRune('\n')
		case '/':
			if i+1 < n && runes[i+1] == '*' {
				i += 2
				for i+1 < n && !(runes[i] == '*' && runes[i+1] == '/') {
					i++
				}
				i++ // skip the closing '/'
			} else {
				cur.WriteRune(c)
			}
		case ';':
			flush()
		default:
			cur.WriteRune(c)
		}
	}
	flush()
	return statements
}

// ImportResult summarises a script run.
type ImportResult struct {
	Statements int     `json:"statements"`
	Executed   int     `json:"executed"`
	Affected   int64   `json:"affected"`
	DurationMS float64 `json:"durationMs"`
	FailedAt   int     `json:"failedAt"` // 1-based index of the failing statement, 0 if none
	Error      string  `json:"error"`
}

// RunScript executes each statement of a script sequentially, stopping at the
// first error and reporting where it failed.
func RunScript(ctx context.Context, db *sql.DB, schema, script string) (*ImportResult, error) {
	stmts := SplitStatements(script)
	res := &ImportResult{Statements: len(stmts)}
	start := time.Now()

	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	if schema != "" {
		if _, err := conn.ExecContext(ctx, "USE "+QuoteIdent(schema)); err != nil {
			return nil, err
		}
	}

	for i, stmt := range stmts {
		r, err := conn.ExecContext(ctx, stmt)
		if err != nil {
			res.FailedAt = i + 1
			res.Error = err.Error()
			res.DurationMS = float64(time.Since(start).Microseconds()) / 1000.0
			return res, nil
		}
		if n, e := r.RowsAffected(); e == nil {
			res.Affected += n
		}
		res.Executed++
	}
	res.DurationMS = float64(time.Since(start).Microseconds()) / 1000.0
	return res, nil
}

// RunScriptStream executes a SQL script read from r, splitting it into
// statements on the fly so an arbitrarily large dump is never buffered whole —
// only the current statement is held in memory. Same comment/quote handling as
// SplitStatements; stops at the first error.
func RunScriptStream(ctx context.Context, db *sql.DB, schema string, r io.Reader) (*ImportResult, error) {
	res := &ImportResult{}
	start := time.Now()

	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	if schema != "" {
		if _, err := conn.ExecContext(ctx, "USE "+QuoteIdent(schema)); err != nil {
			return nil, err
		}
	}

	exec := func(cur *strings.Builder) (stop bool) {
		stmt := strings.TrimSpace(cur.String())
		cur.Reset()
		if stmt == "" {
			return false
		}
		res.Statements++
		out, err := conn.ExecContext(ctx, stmt)
		if err != nil {
			res.FailedAt = res.Statements
			res.Error = err.Error()
			return true
		}
		if n, e := out.RowsAffected(); e == nil {
			res.Affected += n
		}
		res.Executed++
		return false
	}

	br := bufio.NewReaderSize(r, 64*1024)
	var cur strings.Builder
	readErr := error(nil)

loop:
	for {
		c, _, err := br.ReadRune()
		if err != nil {
			if err != io.EOF {
				readErr = err
			}
			break
		}
		switch c {
		case '\'', '"', '`':
			cur.WriteRune(c)
			for {
				n, _, e := br.ReadRune()
				if e != nil {
					break
				}
				cur.WriteRune(n)
				if n == '\\' && c != '`' {
					if n2, _, e2 := br.ReadRune(); e2 == nil {
						cur.WriteRune(n2)
					}
					continue
				}
				if n == c {
					break
				}
			}
		case '-':
			if n, _, e := br.ReadRune(); e == nil && n == '-' {
				skipLine(br)
				cur.WriteRune('\n')
			} else {
				if e == nil {
					_ = br.UnreadRune()
				}
				cur.WriteRune(c)
			}
		case '#':
			skipLine(br)
			cur.WriteRune('\n')
		case '/':
			if n, _, e := br.ReadRune(); e == nil && n == '*' {
				skipBlockComment(br)
			} else {
				if e == nil {
					_ = br.UnreadRune()
				}
				cur.WriteRune(c)
			}
		case ';':
			if exec(&cur) {
				break loop
			}
		default:
			cur.WriteRune(c)
		}
	}

	if readErr != nil {
		return nil, readErr
	}
	if res.Error == "" {
		exec(&cur) // trailing statement without a semicolon
	}
	res.DurationMS = float64(time.Since(start).Microseconds()) / 1000.0
	return res, nil
}

func skipLine(br *bufio.Reader) {
	for {
		c, _, err := br.ReadRune()
		if err != nil || c == '\n' {
			return
		}
	}
}

func skipBlockComment(br *bufio.Reader) {
	prevStar := false
	for {
		c, _, err := br.ReadRune()
		if err != nil {
			return
		}
		if prevStar && c == '/' {
			return
		}
		prevStar = c == '*'
	}
}
