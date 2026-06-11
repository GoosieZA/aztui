package sql

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/microsoft/go-mssqldb/azuread"

	"github.com/GoosieZA/aztui/internal/azure"
	"github.com/GoosieZA/aztui/internal/ui"
)

// topQuery is one row of Query Performance Insight: an aggregated query from
// the database's Query Store.
type topQuery struct {
	text     string
	execs    int64
	cpuMS    float64
	durMS    float64
	lastExec time.Time
}

type queriesMsg struct {
	rows []topQuery
	err  error
}

var queryOrders = []struct{ label, column string }{
	{"cpu", "cpu_ms"},
	{"duration", "dur_ms"},
	{"executions", "executions"},
}

var queryWindows = []struct {
	label string
	hours int
}{
	{"1h", 1},
	{"24h", 24},
	{"7d", 168},
}

// topQueriesSQL aggregates Query Store runtime stats — the same source the
// portal's Query Performance Insight reads. Order column and window come
// from fixed tables above, never from user input.
const topQueriesSQL = `
SELECT TOP 15
  MAX(qt.query_sql_text)                              AS query_text,
  SUM(rs.count_executions)                            AS executions,
  SUM(rs.avg_cpu_time * rs.count_executions) / 1000.0 AS cpu_ms,
  SUM(rs.avg_duration * rs.count_executions) / 1000.0 AS dur_ms,
  MAX(rs.last_execution_time)                         AS last_exec
FROM sys.query_store_query_text qt
JOIN sys.query_store_query q  ON q.query_text_id = qt.query_text_id
JOIN sys.query_store_plan p   ON p.query_id = q.query_id
JOIN sys.query_store_runtime_stats rs ON rs.plan_id = p.plan_id
JOIN sys.query_store_runtime_stats_interval i ON i.runtime_stats_interval_id = rs.runtime_stats_interval_id
WHERE i.start_time >= DATEADD(HOUR, -%d, SYSUTCDATETIME())
GROUP BY q.query_text_id
ORDER BY %s DESC`

// queriesView is Query Performance Insight in the terminal: the database's
// most expensive queries, via a T-SQL connection with your AAD identity.
type queriesView struct {
	server azure.Resource
	dbName string

	orderIdx, windowIdx int

	table   ui.Table
	spin    spinner.Model
	loading bool
	loadErr error

	rows []topQuery

	width, height int
}

func newQueriesView(server azure.Resource, dbName string) *queriesView {
	sp := spinner.New(spinner.WithSpinner(spinner.Dot))
	sp.Style = ui.TitleStyle
	t := ui.NewTable(
		ui.Column{Title: "#", Width: 2},
		ui.Column{Title: "CPU", Width: 9},
		ui.Column{Title: "DURATION", Width: 9},
		ui.Column{Title: "EXECS", Width: 8},
		ui.Column{Title: "LAST RUN", Width: 8},
		ui.Column{Title: "QUERY", Weight: 10},
	)
	t.Empty = "no queries in this window — Query Store may be empty or disabled"
	return &queriesView{server: server, dbName: dbName, windowIdx: 1, table: t, spin: sp, loading: true}
}

func (v *queriesView) fqdn() string {
	if f := v.server.Property("fullyQualifiedDomainName"); f != "" {
		return f
	}
	return v.server.Name + ".database.windows.net"
}

func (v *queriesView) load() tea.Cmd {
	fqdn, dbName := v.fqdn(), v.dbName
	order := queryOrders[v.orderIdx].column
	hours := queryWindows[v.windowIdx].hours
	return tea.Batch(v.spin.Tick, func() tea.Msg {
		opID := ui.BeginOp("top queries on %s", dbName)
		defer ui.EndOp(opID)
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()

		dsn := fmt.Sprintf("server=%s;database=%s;fedauth=ActiveDirectoryDefault;dial timeout=15", fqdn, dbName)
		db, err := sql.Open(azuread.DriverName, dsn)
		if err != nil {
			return queriesMsg{err: err}
		}
		defer db.Close()

		query := fmt.Sprintf(topQueriesSQL, hours, order)
		sqlRows, err := db.QueryContext(ctx, query)
		if err != nil {
			return queriesMsg{err: err}
		}
		defer sqlRows.Close()

		var rows []topQuery
		for sqlRows.Next() {
			var r topQuery
			if err := sqlRows.Scan(&r.text, &r.execs, &r.cpuMS, &r.durMS, &r.lastExec); err != nil {
				return queriesMsg{err: err}
			}
			rows = append(rows, r)
		}
		return queriesMsg{rows: rows, err: sqlRows.Err()}
	})
}

func fmtMS(ms float64) string {
	switch {
	case ms >= 60_000:
		return fmt.Sprintf("%.1fm", ms/60_000)
	case ms >= 1_000:
		return fmt.Sprintf("%.1fs", ms/1_000)
	default:
		return fmt.Sprintf("%.0fms", ms)
	}
}

func (v *queriesView) setRows() {
	rows := make([][]string, len(v.rows))
	for i, r := range v.rows {
		rows[i] = []string{
			strconv.Itoa(i + 1),
			fmtMS(r.cpuMS),
			fmtMS(r.durMS),
			strconv.FormatInt(r.execs, 10),
			ui.Ago(r.lastExec),
			r.text,
		}
	}
	v.table.SetRows(rows)
}

// explainQueryErr translates the usual connection failures into fixes.
func explainQueryErr(err error) string {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "Login failed"), strings.Contains(msg, "login error"):
		return "Login failed — your Azure AD identity has no user in this database.\n" +
			" A db admin can fix it with:\n" +
			"   CREATE USER [you@yourdomain] FROM EXTERNAL PROVIDER;\n" +
			"   ALTER ROLE db_datareader ADD MEMBER [you@yourdomain];"
	case strings.Contains(msg, "i/o timeout"), strings.Contains(msg, "dial"), strings.Contains(msg, "refused"):
		return "Cannot reach the server — check that its firewall allows your\n" +
			" client IP (or that you're on an allowed VNet), port 1433 outbound."
	case strings.Contains(msg, "query_store"):
		return "Query Store appears to be disabled on this database — enable it with:\n" +
			"   ALTER DATABASE [<database>] SET QUERY_STORE = ON;"
	default:
		return msg
	}
}

func (v *queriesView) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		v.width, v.height = msg.Width, msg.Height
		v.table.SetSize(msg.Width, max(1, msg.Height-2))
		return v, nil

	case queriesMsg:
		v.loading = false
		v.loadErr = msg.err
		if msg.err != nil {
			return v, ui.Warnf("top queries unavailable")
		}
		v.rows = msg.rows
		v.setRows()
		return v, nil

	case ui.ActivatedMsg:
		if v.loading {
			return v, v.load()
		}
		return v, nil

	case spinner.TickMsg:
		if !v.loading {
			return v, nil
		}
		var cmd tea.Cmd
		v.spin, cmd = v.spin.Update(msg)
		return v, cmd

	case tea.KeyMsg:
		if !v.table.InputActive() {
			switch msg.String() {
			case "o":
				v.orderIdx = (v.orderIdx + 1) % len(queryOrders)
				v.loading = true
				return v, v.load()
			case "1", "2", "3":
				idx := int(msg.String()[0] - '1')
				if idx != v.windowIdx {
					v.windowIdx = idx
					v.loading = true
					return v, v.load()
				}
				return v, nil
			case "enter":
				if idx := v.table.CursorRow(); idx >= 0 && idx < len(v.rows) {
					return v, ui.Push(newQueryTextView(v.dbName, idx+1, v.rows[idx]))
				}
				return v, nil
			case "y":
				if idx := v.table.CursorRow(); idx >= 0 && idx < len(v.rows) {
					return v, ui.Yank(fmt.Sprintf("query #%d", idx+1), v.rows[idx].text)
				}
				return v, nil
			case "R":
				v.loading = true
				return v, v.load()
			}
		}
		return v, v.table.Update(msg)
	}
	return v, nil
}

func (v *queriesView) Init() tea.Cmd { return v.load() }

func (v *queriesView) View() string {
	var windows []string
	for i, w := range queryWindows {
		if i == v.windowIdx {
			windows = append(windows, ui.SelectedRowStyle.Render(" "+w.label+" "))
		} else {
			windows = append(windows, ui.DimStyle.Render(" "+w.label+" "))
		}
	}
	title := ui.TitleStyle.Render(" "+v.dbName) +
		ui.DimStyle.Render("  top queries by ") + ui.WarnStyle.Render(queryOrders[v.orderIdx].label) +
		ui.DimStyle.Render(" (o)  ·  ") + strings.Join(windows, " ")
	if v.loading {
		return title + "\n\n " + v.spin.View() + ui.DimStyle.Render(" asking Query Store (connects with your AAD identity)...")
	}
	if v.loadErr != nil {
		return title + "\n\n" + ui.WarnStyle.Render(" "+explainQueryErr(v.loadErr)) + "\n\n" +
			ui.DimStyle.Render(" R retries")
	}
	return title + "\n" + v.table.View()
}

func (v *queriesView) InputActive() bool { return v.table.InputActive() }

func (v *queriesView) Breadcrumb() string { return v.dbName + " queries" }

func (v *queriesView) KeyHints() []ui.KeyHint {
	return []ui.KeyHint{
		{Keys: "o", Desc: "order: cpu / duration / executions"},
		{Keys: "1/2/3", Desc: "window: 1h / 24h / 7d"},
		{Keys: "enter", Desc: "full query text"},
		{Keys: "y", Desc: "yank query text"},
		{Keys: "R", Desc: "refresh"},
	}
}

// --- full query text -------------------------------------------------------

type queryTextView struct {
	dbName string
	rank   int
	row    topQuery

	vp            viewport.Model
	width, height int
}

func newQueryTextView(dbName string, rank int, row topQuery) *queryTextView {
	return &queryTextView{dbName: dbName, rank: rank, row: row}
}

func (v *queryTextView) Init() tea.Cmd { return nil }

func (v *queryTextView) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		v.width, v.height = msg.Width, msg.Height
		v.vp = viewport.New(msg.Width, max(1, msg.Height-2))
		stats := ui.DimStyle.Render(fmt.Sprintf(" cpu %s · duration %s · %d executions · last run %s\n\n",
			fmtMS(v.row.cpuMS), fmtMS(v.row.durMS), v.row.execs, ui.Ago(v.row.lastExec)))
		// Format the SQL and soft-wrap whatever lines are still too long.
		body := lipgloss.NewStyle().Width(max(20, msg.Width-2)).Render(formatSQL(v.row.text))
		v.vp.SetContent(stats + body)
		return v, nil
	case tea.KeyMsg:
		switch msg.String() {
		case "y":
			return v, ui.Yank(fmt.Sprintf("query #%d", v.rank), v.row.text)
		case "g":
			v.vp.GotoTop()
			return v, nil
		case "G":
			v.vp.GotoBottom()
			return v, nil
		}
	}
	var cmd tea.Cmd
	v.vp, cmd = v.vp.Update(msg)
	return v, cmd
}

func (v *queryTextView) View() string {
	return ui.TitleStyle.Render(fmt.Sprintf(" query #%d", v.rank)) +
		ui.DimStyle.Render("  on "+v.dbName) + "\n" + v.vp.View()
}

func (v *queryTextView) Breadcrumb() string { return fmt.Sprintf("query #%d", v.rank) }

func (v *queryTextView) KeyHints() []ui.KeyHint {
	return []ui.KeyHint{
		{Keys: "y", Desc: "yank query text"},
		{Keys: "j/k", Desc: "scroll"},
	}
}
