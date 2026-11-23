package schemadiff

// Reports: plain text for the terminal, Markdown, JSON and a self-contained HTML page.

import (
	"encoding/json"
	"fmt"
	"html"
	"regexp"
	"strings"
)

// ReportInput is everything a report shows.
type ReportInput struct {
	Result       Result
	Script       string
	SourceLabel  string
	TargetLabel  string
	Database     string // target database, for pt-online-schema-change commands
	IncludeDrops bool
	ShowSame     bool
	DDLDiff      bool
	Verify       *VerifyResult
	Title        string
}

var severityMarkers = map[string]string{SeverityCritical: "🛑", SeverityWarning: "⚠️", SeveritySuggestion: "💡", SeverityOK: "✅"}
var severityLabels = map[string]string{SeverityCritical: "CRITICAL", SeverityWarning: "WARNING", SeveritySuggestion: "SUGGESTION", SeverityOK: "OK"}

func (in ReportInput) summaryLine() string {
	c := in.Result.Summary()
	return fmt.Sprintf("%d tables: %d to create, %d to change, %d only in the target, %d identical. "+
		"Changes: %d critical, %d warnings, %d suggestions, %d safe.",
		c.Tables, c.Create, c.Alter, c.Drop, c.Same, c.Critical, c.Warning, c.Suggestion, c.OK)
}

// VerifyText describes the result of testing the script.
func (v *VerifyResult) Text() string {
	switch {
	case v == nil:
		return "Not tested."
	case v.Error != "":
		return fmt.Sprintf("Stopped on MySQL %s: %s", v.Version, v.Error)
	case v.OK:
		return fmt.Sprintf("Passed on MySQL %s (scratch copy of the target): all %d statements ran and the result matches the source.", v.Version, v.Count)
	default:
		return fmt.Sprintf("Ran on MySQL %s, but %d difference(s) remain.", v.Version, len(v.Remaining))
	}
}

var reBold = regexp.MustCompile(`\*\*(.+?)\*\*`)

func plain(text string) string { return reBold.ReplaceAllString(text, "$1") }

func wrap(text string, width int, indent string) string {
	var lines []string
	line := ""
	for _, w := range strings.Fields(text) {
		if line != "" && len([]rune(line))+1+len([]rune(w)) > width {
			lines = append(lines, line)
			line = w
		} else if line == "" {
			line = w
		} else {
			line += " " + w
		}
	}
	if line != "" {
		lines = append(lines, line)
	}
	return indent + strings.Join(lines, "\n"+indent)
}

func indentLines(text, indent string) string {
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	for i, l := range lines {
		if l != "" {
			lines[i] = indent + l
		}
	}
	return strings.Join(lines, "\n")
}

// TextReport is the report for a terminal.
func TextReport(in ReportInput) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Source: %s (the schema to match)\n", in.SourceLabel)
	fmt.Fprintf(&b, "# Target: %s (the schema the script changes)\n", in.TargetLabel)
	fmt.Fprintf(&b, "# %s\n", in.summaryLine())
	for _, n := range in.Result.Notes {
		fmt.Fprintf(&b, "# Note: %s\n", plain(n))
	}
	if !in.Result.HasDifferences() {
		b.WriteString("\nThe schemas match: no differences in tables, columns, indexes, foreign keys or table options.\n")
	}
	for _, e := range in.Result.SortedByRisk(in.ShowSame) {
		algo := TableAlgorithm(e.Changes)
		head := fmt.Sprintf("\n%s %s — %s", severityLabels[WorstSeverity(e.Changes)], e.Name, StatusText[e.Status])
		if e.Status == StatusSame {
			head = fmt.Sprintf("\nOK %s — identical", e.Name)
		}
		if algo != "" {
			head += " · MySQL: " + RunLabels[algo]
		}
		b.WriteString(head + "\n")
		for _, c := range e.Changes {
			t := plain(c.Text)
			if c.Clause != "" && c.Label != "" {
				t += " [MySQL: " + c.Label + "]"
			}
			fmt.Fprintf(&b, "  %-10s %s\n", severityLabels[c.Severity], strings.TrimSpace(wrap(t, 100, "             ")))
		}
		if cmd := OnlineSchemaChangeCommand(e, in.Database); cmd != "" && hasKind(e.Changes, "large_table") {
			b.WriteString("  Online alternative (Percona Toolkit; foreign keys are left to the main script):\n")
			b.WriteString(indentLines(cmd, "    ") + "\n")
		}
		if in.DDLDiff && e.Source != nil && e.Target != nil {
			if d := DDLDiff(e); d != "" {
				b.WriteString("  DDL diff:\n" + indentLines(d, "    ") + "\n")
			}
		}
	}
	if in.Verify != nil {
		b.WriteString("\n# Script test: " + in.Verify.Text() + "\n")
		writeVerifyDetails(&b, in.Verify, "#   ")
	}
	if in.Script != "" {
		b.WriteString("\n" + in.Script)
	}
	return b.String()
}

func writeVerifyDetails(b *strings.Builder, v *VerifyResult, prefix string) {
	if v.Statement != "" {
		b.WriteString(prefix + "Failed statement:\n" + indentLines(v.Statement, prefix+"  ") + "\n")
	}
	for _, r := range v.Remaining {
		b.WriteString(prefix + "Remaining: " + plain(r) + "\n")
	}
	for _, r := range v.Expected {
		b.WriteString(prefix + "Expected (excluded drops or not scripted): " + plain(r) + "\n")
	}
}

func hasKind(changes []Change, kind string) bool {
	for _, c := range changes {
		if c.Kind == kind {
			return true
		}
	}
	return false
}

// MarkdownReport is the report as Markdown.
func MarkdownReport(in ReportInput) string {
	out := []string{"# " + orDefault(in.Title, "Schema diff"), "",
		"- **Source** (the schema to match): " + in.SourceLabel,
		"- **Target** (the schema the script changes): " + in.TargetLabel,
		"- **Generated:** " + Now().Format("2006-01-02 15:04")}
	if in.Verify != nil {
		out = append(out, "- **Script test:** "+in.Verify.Text())
	}
	out = append(out, "", in.summaryLine(), "")
	for _, e := range in.Result.SortedByRisk(in.ShowSame) {
		algo := TableAlgorithm(e.Changes)
		head := fmt.Sprintf("## `%s` — %s", e.Name, e.Status)
		if algo != "" {
			head += " (MySQL: " + RunLabels[algo] + ")"
		}
		out = append(out, head, "")
		for _, c := range e.Changes {
			line := "- " + severityMarkers[c.Severity] + " " + c.Text
			if c.Clause != "" && c.Label != "" {
				line += " *MySQL: " + c.Label + ".*"
			}
			out = append(out, line)
		}
		out = append(out, "")
		if stmts := TableStatements(e, in.IncludeDrops); len(stmts) > 0 {
			out = append(out, "```sql", statementsText(stmts), "```", "")
		}
		if hasKind(e.Changes, "large_table") {
			if cmd := OnlineSchemaChangeCommand(e, in.Database); cmd != "" {
				out = append(out, "Online alternative (Percona Toolkit):", "", "```bash", cmd, "```", "")
			}
		}
		if in.DDLDiff && e.Source != nil && e.Target != nil {
			if d := DDLDiff(e); d != "" {
				out = append(out, "```diff", d, "```", "")
			}
		}
	}
	if len(in.Result.Notes) > 0 {
		out = append(out, "## Notes", "")
		for _, n := range in.Result.Notes {
			out = append(out, "- "+n)
		}
		out = append(out, "")
	}
	if v := in.Verify; v != nil && (v.Statement != "" || len(v.Remaining) > 0) {
		out = append(out, "## Script test", "", v.Text(), "")
		if v.Statement != "" {
			out = append(out, "```sql", v.Statement, "```", "")
		}
		for _, r := range v.Remaining {
			out = append(out, "- "+r)
		}
		out = append(out, "")
	}
	if in.Script != "" {
		out = append(out, "## Migration script", "", "```sql", strings.TrimRight(in.Script, "\n"), "```", "")
	}
	return strings.Join(out, "\n")
}

func statementsText(stmts []Statement) string {
	parts := make([]string, len(stmts))
	for i, s := range stmts {
		if s.Included {
			parts[i] = s.SQL
		} else {
			parts[i] = Commented(s.SQL)
		}
	}
	return strings.Join(parts, "\n\n")
}

func orDefault(v, d string) string {
	if v == "" {
		return d
	}
	return v
}

// JSONReport is the report as JSON, for other programs.
func JSONReport(in ReportInput) ([]byte, error) {
	type table struct {
		TableDiff
		Worst         string      `json:"worst_severity"`
		Algorithm     string      `json:"algorithm,omitempty"`
		Statements    []Statement `json:"statements"`
		OnlineCommand string      `json:"pt_online_schema_change,omitempty"`
		DDLDiff       string      `json:"ddl_diff,omitempty"`
		SourceCreate  string      `json:"source_create,omitempty"`
		TargetCreate  string      `json:"target_create,omitempty"`
	}
	var tables []table
	for _, e := range in.Result.SortedByRisk(true) {
		t := table{TableDiff: e, Worst: WorstSeverity(e.Changes), Algorithm: TableAlgorithm(e.Changes),
			Statements: TableStatements(e, in.IncludeDrops)}
		if t.Statements == nil {
			t.Statements = []Statement{}
		}
		if hasKind(e.Changes, "large_table") {
			t.OnlineCommand = OnlineSchemaChangeCommand(e, in.Database)
		}
		if e.Source != nil && e.Target != nil {
			t.DDLDiff = DDLDiff(e)
		}
		if e.Source != nil {
			t.SourceCreate = CreateStatement(e.Source, true)
		}
		if e.Target != nil {
			t.TargetCreate = CreateStatement(e.Target, true)
		}
		tables = append(tables, t)
	}
	if tables == nil {
		tables = []table{}
	}
	return json.MarshalIndent(map[string]any{
		"source":        in.SourceLabel,
		"target":        in.TargetLabel,
		"generated":     Now().Format("2006-01-02T15:04:05"),
		"summary":       in.Result.Summary(),
		"differences":   in.Result.HasDifferences(),
		"include_drops": in.IncludeDrops,
		"tables":        tables,
		"notes":         in.Result.Notes,
		"statements":    in.Result.ExecutableStatements(in.IncludeDrops),
		"script":        in.Script,
		"verify":        in.Verify,
	}, "", "  ")
}

var (
	reMdBold = regexp.MustCompile(`\*\*(.+?)\*\*`)
	reMdCode = regexp.MustCompile("`([^`]+)`")
)

func inlineMarkdownHTML(text string) string {
	h := html.EscapeString(text)
	h = reMdBold.ReplaceAllString(h, "<b>$1</b>")
	h = reMdCode.ReplaceAllString(h, `<code>$1</code>`)
	return strings.ReplaceAll(strings.TrimSpace(h), "\n", "<br>")
}

const htmlCSS = `
:root { --bg:#fff; --fg:#1f2328; --muted:#59636e; --line:#d1d9e0; --soft:#f6f8fa; --brand:#0e5fb4;
  --critical:#b42318; --critical-bg:#fde8e6; --warning:#9a6700; --warning-bg:#fff4d6;
  --suggestion:#0e5fb4; --suggestion-bg:#e3effc; --ok:#1a7f37; --ok-bg:#e2f5e7; --code-bg:#f6f8fa; }
@media (prefers-color-scheme: dark) { :root { --bg:#0d1117; --fg:#e6edf3; --muted:#9198a1; --line:#30363d;
  --soft:#161b22; --brand:#4493f8; --critical:#ff7b72; --critical-bg:#3d1214; --warning:#d29922; --warning-bg:#3a2a07;
  --suggestion:#79c0ff; --suggestion-bg:#0c2d4f; --ok:#3fb950; --ok-bg:#0f2e19; --code-bg:#161b22; } }
* { box-sizing: border-box; }
body { margin:0; background:var(--bg); color:var(--fg); font:14px/1.5 -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, Helvetica, Arial, sans-serif; }
main { max-width: 1000px; margin: 0 auto; padding: 24px 16px 48px; }
header { border-bottom: 3px solid var(--brand); padding-bottom: 12px; margin-bottom: 20px; }
header .brand { color: var(--brand); font-weight: 700; letter-spacing: .08em; font-size: 12px; }
header h1 { margin: 4px 0; font-size: 26px; }
header .meta { color: var(--muted); }
h2 { font-size: 19px; margin: 28px 0 10px; border-bottom: 1px solid var(--line); padding-bottom: 4px; }
h3 { font-size: 16px; margin: 24px 0 8px; }
.kpis { display: grid; grid-template-columns: repeat(auto-fit, minmax(140px, 1fr)); gap: 10px; }
.kpi { border: 1px solid var(--line); border-radius: 8px; padding: 10px 12px; background: var(--soft); }
.kpi .v { font-size: 26px; font-weight: 700; } .kpi .l { font-size: 11px; color: var(--muted); letter-spacing: .05em; }
.kpi.critical .v { color: var(--critical); } .kpi.warning .v { color: var(--warning); }
table.grid { width: 100%; border-collapse: collapse; }
table.grid th, table.grid td { border: 1px solid var(--line); padding: 6px 8px; text-align: left; vertical-align: top; }
table.grid th { background: var(--soft); }
.finding { display: flex; gap: 10px; padding: 8px 10px; border-left: 4px solid; margin: 6px 0; border-radius: 4px; }
.finding .sev { font-size: 11px; font-weight: 700; min-width: 86px; letter-spacing: .04em; padding-top: 2px; }
.finding.critical { border-color: var(--critical); background: var(--critical-bg); } .finding.critical .sev { color: var(--critical); }
.finding.warning { border-color: var(--warning); background: var(--warning-bg); } .finding.warning .sev { color: var(--warning); }
.finding.suggestion { border-color: var(--suggestion); background: var(--suggestion-bg); } .finding.suggestion .sev { color: var(--suggestion); }
.finding.ok { border-color: var(--ok); background: var(--ok-bg); } .finding.ok .sev { color: var(--ok); }
.run { color: var(--muted); font-size: 12px; }
code { font: 12.5px/1.4 ui-monospace, SFMono-Regular, Menlo, Consolas, monospace; background: var(--code-bg); padding: 1px 4px; border-radius: 4px; }
pre { font: 12.5px/1.45 ui-monospace, SFMono-Regular, Menlo, Consolas, monospace; background: var(--code-bg); border: 1px solid var(--line);
  border-radius: 6px; padding: 10px 12px; overflow-x: auto; white-space: pre-wrap; word-break: break-word; }
.muted { color: var(--muted); }
.diff-add { color: var(--ok); } .diff-del { color: var(--critical); }
@media print { pre { white-space: pre-wrap; } .finding { break-inside: avoid; } }
`

func pre(code string) string {
	return "<pre>" + html.EscapeString(strings.TrimRight(code, "\n")) + "</pre>"
}

func diffPre(d string) string {
	var lines []string
	for _, l := range strings.Split(d, "\n") {
		e := html.EscapeString(l)
		switch {
		case strings.HasPrefix(l, "+") && !strings.HasPrefix(l, "+++"):
			e = `<span class="diff-add">` + e + "</span>"
		case strings.HasPrefix(l, "-") && !strings.HasPrefix(l, "---"):
			e = `<span class="diff-del">` + e + "</span>"
		}
		lines = append(lines, e)
	}
	return "<pre>" + strings.Join(lines, "\n") + "</pre>"
}

// HTMLReport is the report as a self-contained HTML page (print it to PDF from a browser).
func HTMLReport(in ReportInput) string {
	c := in.Result.Summary()
	title := orDefault(in.Title, "Schema Diff Report")
	var b strings.Builder
	b.WriteString(htmlOpen(title, fmt.Sprintf("Makes %s match %s", in.TargetLabel, in.SourceLabel)))
	b.WriteString(htmlKPIs([][2]any{{c.Critical, "CRITICAL (DATA LOSS)"}, {c.Warning, "WARNINGS"}, {c.Create, "TABLES TO CREATE"},
		{c.Alter, "TABLES TO CHANGE"}, {c.Drop, "ONLY IN TARGET"}}))
	facts := [][2]string{
		{"Source (the schema to match)", in.SourceLabel},
		{"Target (the schema the script changes)", in.TargetLabel},
		{"Tables compared", fmt.Sprintf("%d: %d to create, %d to change, %d only in the target, %d identical", c.Tables, c.Create, c.Alter, c.Drop, c.Same)},
		{"Statements that delete data", dropsText(in.IncludeDrops)},
		{"Script test", in.Verify.Text()},
	}
	b.WriteString(`<h2>Comparison</h2>` + htmlFacts(facts) + htmlLegend())
	b.WriteString(htmlVerify(in.Verify))
	b.WriteString(htmlTables(in, "h2", "h3"))
	b.WriteString(htmlScript(in.Script))
	return b.String() + htmlClose
}

func dropsText(include bool) string {
	if include {
		return "Included"
	}
	return "Commented out in the script (DROP TABLE, DROP COLUMN)"
}

const htmlClose = "</main></body></html>\n"

func htmlOpen(title, meta string) string {
	esc := html.EscapeString
	return fmt.Sprintf(`<!DOCTYPE html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">`+
		`<title>%s</title><style>%s</style></head><body><main>`, esc(title), htmlCSS) +
		fmt.Sprintf(`<header><div class="brand">PT-SCHEMA-DIFF</div><h1>%s</h1><div class="meta">%s · Generated %s</div></header>`,
			esc(title), esc(meta), Now().Format("02 Jan 2006, 15:04"))
}

func htmlKPIs(kpis [][2]any) string {
	var b strings.Builder
	b.WriteString(`<div class="kpis">`)
	for i, k := range kpis {
		class := "kpi"
		if i == 0 {
			class += " critical"
		} else if i == 1 {
			class += " warning"
		}
		fmt.Fprintf(&b, `<div class="%s"><div class="v">%v</div><div class="l">%s</div></div>`, class, k[0], html.EscapeString(fmt.Sprint(k[1])))
	}
	return b.String() + "</div>"
}

func htmlFacts(facts [][2]string) string {
	var b strings.Builder
	b.WriteString(`<table class="grid">`)
	for _, f := range facts {
		fmt.Fprintf(&b, `<tr><th style="width:32%%">%s</th><td>%s</td></tr>`, html.EscapeString(f[0]), html.EscapeString(f[1]))
	}
	return b.String() + "</table>"
}

func htmlLegend() string {
	var help []string
	for _, h := range RunHelp {
		help = append(help, "<b>"+html.EscapeString(h[0])+"</b> = "+html.EscapeString(h[1]))
	}
	return `<p class="muted">Severity: CRITICAL = data can be lost; WARNING = the change can fail or blocks writes; ` +
		`SUGGESTION = worth a look; OK = safe. "MySQL:" says how MySQL 8.0+ runs each change: ` + strings.Join(help, "; ") + ".</p>"
}

func htmlVerify(v *VerifyResult) string {
	if v == nil || (v.Statement == "" && len(v.Remaining) == 0) {
		return ""
	}
	out := "<h2>Script test</h2><p>" + html.EscapeString(v.Text()) + "</p>"
	if v.Statement != "" {
		out += pre(v.Statement)
	}
	for _, r := range v.Remaining {
		out += `<div class="finding warning"><span class="sev">REMAINS</span><span>` + inlineMarkdownHTML(r) + "</span></div>"
	}
	return out
}

// htmlTables is the table overview and the findings and SQL of each table; h1 and h2 are the heading tags to use.
func htmlTables(in ReportInput, h1, h2 string) string {
	esc := html.EscapeString
	var b strings.Builder
	entries := in.Result.SortedByRisk(in.ShowSame)
	if len(entries) > 0 {
		fmt.Fprintf(&b, `<%s>Tables</%s><table class="grid"><tr><th>Table</th><th>Status</th><th>Changes</th><th>MySQL runs it as</th></tr>`, h1, h1)
		for _, e := range entries {
			algo := "–"
			if a := TableAlgorithm(e.Changes); a != "" {
				algo = RunLabels[a]
			}
			fmt.Fprintf(&b, "<tr><td><b>%s</b></td><td>%s</td><td>%d</td><td>%s</td></tr>", esc(e.Name), esc(StatusText[e.Status]), len(e.Changes), esc(algo))
		}
		b.WriteString("</table>")
	} else {
		b.WriteString("<p>The schemas match: no differences in tables, columns, indexes, foreign keys or table options.</p>")
	}
	for _, n := range in.Result.Notes {
		b.WriteString(`<p class="muted">` + inlineMarkdownHTML(n) + "</p>")
	}
	for _, e := range entries {
		fmt.Fprintf(&b, "<%s>%s · %s</%s>", h2, esc(e.Name), esc(StatusText[e.Status]), h2)
		for _, ch := range e.Changes {
			run := ""
			if ch.Clause != "" && ch.Label != "" {
				run = ` <span class="run">(MySQL: ` + esc(ch.Label) + ")</span>"
			}
			fmt.Fprintf(&b, `<div class="finding %s"><span class="sev">%s</span><span>%s%s</span></div>`,
				ch.Severity, severityLabels[ch.Severity], inlineMarkdownHTML(ch.Text), run)
		}
		if stmts := TableStatements(e, in.IncludeDrops); len(stmts) > 0 {
			b.WriteString(pre(statementsText(stmts)))
		}
		if hasKind(e.Changes, "large_table") {
			if cmd := OnlineSchemaChangeCommand(e, in.Database); cmd != "" {
				b.WriteString("<p><b>Online alternative</b> (Percona Toolkit):</p>" + pre(cmd))
			}
		}
		if in.DDLDiff && e.Source != nil && e.Target != nil {
			if d := DDLDiff(e); d != "" {
				b.WriteString("<p><b>DDL diff</b> (target → source):</p>" + diffPre(d))
			}
		}
	}
	return b.String()
}

func htmlScript(script string) string {
	return `<h2>Migration script</h2><p class="muted">Review every statement and test on a copy first. Run during low traffic: ` +
		`changes marked “blocks writes” stop writes to their table while they run.</p>` + pre(script)
}
