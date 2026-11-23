package schemadiff

// Comparing two schemas. The source is the schema to match (e.g. production); the target is the one the script
// changes. The diff is deterministic: it compares the parsed columns, indexes, foreign keys, CHECK constraints and
// table options. Views, routines, triggers and events are not compared. Partitioning and column order differences are
// reported but not changed. Foreign keys and CHECK constraints are matched by definition, not by name, because unnamed
// ones get generated names (orders_ibfk_1) that differ between servers.

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Phases of the migration script, in the order they run.
const (
	PhaseDropFK = "drop_fk"
	PhaseCreate = "create"
	PhaseAlter  = "alter"
	PhaseDrop   = "drop"
	PhaseAddFK  = "add_fk"
)

// Change is one finding. Clause is the ALTER TABLE clause (or statement) that applies it, empty when the script does
// not change it.
type Change struct {
	Kind      string `json:"kind"`
	Severity  string `json:"severity"`
	Text      string `json:"text"`
	Clause    string `json:"clause,omitempty"`
	Algorithm string `json:"algorithm,omitempty"`
	Phase     string `json:"phase"`
	DataLoss  bool   `json:"data_loss"`
	Label     string `json:"label,omitempty"`
}

func newChange(kind, severity, text, clause, algorithm string) Change {
	return Change{Kind: kind, Severity: severity, Text: text, Clause: clause, Algorithm: algorithm, Phase: PhaseAlter,
		Label: RunLabels[algorithm]}
}

func (c Change) withPhase(p string) Change { c.Phase = p; return c }
func (c Change) withLabel(l string) Change { c.Label = l; return c }
func (c Change) withDataLoss() Change      { c.DataLoss = true; return c }

// Size is the approximate size of a table.
type Size struct {
	Rows  int64 `json:"rows"`
	Bytes int64 `json:"bytes"`
}

// Sizes maps lower-case table names to their size.
type Sizes map[string]Size

// columnSQL is `name` definition, with an explicit CHARACTER SET / COLLATE for string columns that rely on the table
// default, so the column ends up with the source's character set whatever the target table's default is.
func columnSQL(c Column) string {
	def := c.Definition
	a := c.Attrs
	if stringTypes[a.Type] && a.Charset != "" {
		masked, _ := protect(def, reLiteral)
		add := ""
		if !reHasCharset.MatchString(masked) {
			add += " CHARACTER SET " + a.Charset
		}
		if a.Collation != "" && !reHasCollate.MatchString(masked) {
			add += " COLLATE " + a.Collation
		}
		if add != "" && a.typeEnd <= len(def) {
			def = def[:a.typeEnd] + add + def[a.typeEnd:]
		}
	}
	return Quote(c.Name) + " " + def
}

var (
	reHasCharset = regexp.MustCompile(`(?i)\b(?:CHARACTER\s+SET|CHARSET)\b`)
	reHasCollate = regexp.MustCompile(`(?i)\bCOLLATE\b`)
	rePartName   = regexp.MustCompile(`(?s)^([^()\s]+)(.*)$`)
	reStored     = regexp.MustCompile(`\bas\b.*\bstored\b`)
)

func joinParts(parts []string) string { return strings.Join(parts, ", ") }

// DiffTables returns the changes that turn table tgt into src. Clauses of phase "alter" go into one ALTER TABLE;
// "drop_fk" and "add_fk" run before and after all tables are created and altered, so foreign keys never point at a
// missing table or index.
func DiffTables(src, tgt *Table, sizes Sizes) []Change {
	var changes []Change
	srcCols, tgtCols := map[string]Column{}, map[string]Column{}
	for _, c := range src.Columns {
		srcCols[strings.ToLower(c.Name)] = c
	}
	for _, c := range tgt.Columns {
		tgtCols[strings.ToLower(c.Name)] = c
	}
	var added, dropped []Column
	for _, c := range src.Columns {
		if _, ok := tgtCols[strings.ToLower(c.Name)]; !ok {
			added = append(added, c)
		}
	}
	for _, c := range tgt.Columns {
		if _, ok := srcCols[strings.ToLower(c.Name)]; !ok {
			dropped = append(dropped, c)
		}
	}

	// One added and one dropped column with the same definition: most likely a rename, which keeps the data.
	renames := map[string]Column{}
	for _, a := range added {
		var same []Column
		for _, d := range dropped {
			if sameColumn(a, d) {
				same = append(same, d)
			}
		}
		if len(same) != 1 {
			continue
		}
		n := 0
		for _, x := range added {
			if sameColumn(x, same[0]) {
				n++
			}
		}
		if n == 1 {
			renames[strings.ToLower(a.Name)] = same[0]
		}
	}
	renamedFrom := map[string]bool{}
	for _, d := range renames {
		renamedFrom[strings.ToLower(d.Name)] = true
	}

	for pos, col := range src.Columns {
		key := strings.ToLower(col.Name)
		place := " FIRST"
		if pos > 0 {
			place = " AFTER " + Quote(src.Columns[pos-1].Name)
		}
		if old, ok := renames[key]; ok {
			changes = append(changes, newChange("rename_column", SeverityWarning,
				msg(fmt.Sprintf("Rename column `%s` → `%s`", old.Name, col.Name),
					fmt.Sprintf("The data is kept. Treated as a rename because `%s` exists only in the target, "+
						"`%s` only in the source, and both have the same definition. If they are really two "+
						"different columns, replace this CHANGE COLUMN with a DROP COLUMN and an ADD COLUMN.", old.Name, col.Name),
					runText[AlgoInstant]),
				"CHANGE COLUMN "+Quote(old.Name)+" "+columnSQL(col), AlgoInstant))
		} else if old, ok := tgtCols[key]; !ok {
			a := col.Attrs
			data, algorithm, sev := "No existing data changes.", AlgoInstant, SeverityOK
			run := "Instant on MySQL 8.0.29 and later. Older versions rebuild the table (reads and writes continue), " +
				"because the column goes in the middle of the table."
			if pos == len(src.Columns)-1 {
				run = "Instant on MySQL 8.0.12 and later."
			}
			if a.NotNull && !a.HasDefault && !contains(strings.Fields(a.Other), "as") && !a.AutoIncrement {
				data = "Existing rows get 0, '' or the zero date in this column, because it is NOT NULL without a DEFAULT."
			}
			if a.AutoIncrement {
				algorithm, sev = AlgoCopy, SeverityWarning
				data = "MySQL numbers every existing row."
				run = runText[AlgoCopy]
			} else if reStored.MatchString(a.Other) {
				algorithm, sev = AlgoCopy, SeverityWarning
				data = "A STORED generated column: MySQL computes and stores it for every existing row."
				run = runText[AlgoCopy]
			}
			where := "as the first column"
			if pos > 0 {
				where = fmt.Sprintf("after `%s`", src.Columns[pos-1].Name)
			}
			changes = append(changes, newChange("add_column", sev,
				msg(fmt.Sprintf("Add column `%s` `%s` %s", col.Name, typeText(a), where), data, run),
				"ADD COLUMN "+columnSQL(col)+place, algorithm))
		} else if !sameColumn(col, old) {
			severity, headline, data, algorithm, run := modifyRisk(src.Name, col.Name, old.Attrs, col.Attrs)
			c := newChange("modify_column", severity, msg(headline, append(data, run)...),
				"MODIFY COLUMN "+columnSQL(col), algorithm)
			if run == metadataOnly {
				c = c.withLabel("instant")
			}
			changes = append(changes, c)
		}
	}

	for _, col := range dropped {
		if renamedFrom[strings.ToLower(col.Name)] {
			continue
		}
		changes = append(changes, newChange("drop_column", SeverityCritical,
			msg(fmt.Sprintf("Drop column `%s` `%s`", col.Name, typeText(col.Attrs)),
				"Everything stored in this column is deleted for good. It exists only in the target. The script "+
					"leaves this commented out unless --include-drops is given.",
				"Instant on MySQL 8.0.29 and later; older versions rebuild the table."),
			"DROP COLUMN "+Quote(col.Name), AlgoInstant).withDataLoss())
	}

	var kept []string
	for _, c := range src.Columns {
		k := strings.ToLower(c.Name)
		if _, ok := tgtCols[k]; ok {
			kept = append(kept, k)
		} else if _, ok := renames[k]; ok {
			kept = append(kept, k)
		}
	}
	tgtPos := map[string]int{}
	for i, c := range tgt.Columns {
		tgtPos[strings.ToLower(c.Name)] = i
	}
	tgtKept := append([]string{}, kept...)
	posOf := func(k string) int {
		if r, ok := renames[k]; ok {
			return tgtPos[strings.ToLower(r.Name)]
		}
		return tgtPos[k]
	}
	sort.SliceStable(tgtKept, func(i, j int) bool { return posOf(tgtKept[i]) < posOf(tgtKept[j]) })
	if strings.Join(kept, ",") != strings.Join(tgtKept, ",") {
		changes = append(changes, newChange("column_order", SeveritySuggestion,
			msg("Column order differs (not changed by the script)",
				"Queries that name their columns are not affected, and moving columns would mean copying the whole table."),
			"", ""))
	}

	// Indexes: by name; an unmatched pair with the same definition is a rename. Target keys are compared with the
	// renamed columns' new names, since CHANGE COLUMN renames them inside the indexes and foreign keys too.
	newNames := map[string]string{}
	for n, d := range renames {
		newNames[strings.ToLower(d.Name)] = n
	}
	renamed := func(names []string) []string {
		out := make([]string, len(names))
		for i, n := range names {
			if r, ok := newNames[n]; ok {
				out[i] = r
			} else {
				out[i] = n
			}
		}
		return out
	}
	renamedPart := func(part string) string {
		m := rePartName.FindStringSubmatch(part)
		if m == nil {
			return part
		}
		if r, ok := newNames[m[1]]; ok {
			return r + m[2]
		}
		return part
	}

	srcIdx, tgtIdx := map[string]Index{}, map[string]Index{}
	var srcOrder, tgtOrder []string
	for _, i := range src.Indexes {
		k := strings.ToLower(i.Name)
		if _, ok := srcIdx[k]; !ok {
			srcOrder = append(srcOrder, k)
		}
		srcIdx[k] = i
	}
	for _, i := range tgt.Indexes {
		k := strings.ToLower(i.Name)
		parts := make([]string, len(i.KeyParts))
		for n, p := range i.KeyParts {
			parts[n] = renamedPart(p)
		}
		i.KeyParts = parts
		if _, ok := tgtIdx[k]; !ok {
			tgtOrder = append(tgtOrder, k)
		}
		tgtIdx[k] = i
	}
	var onlySrc, onlyTgt []Index
	for _, k := range srcOrder {
		if _, ok := tgtIdx[k]; !ok {
			onlySrc = append(onlySrc, srcIdx[k])
		}
	}
	for _, k := range tgtOrder {
		if _, ok := srcIdx[k]; !ok {
			onlyTgt = append(onlyTgt, tgtIdx[k])
		}
	}
	var stillSrc []Index
	for _, index := range onlySrc {
		match := -1
		if index.Kind != "PRIMARY" {
			for n, t := range onlyTgt {
				if t.key() == index.key() {
					match = n
					break
				}
			}
		}
		if match < 0 {
			stillSrc = append(stillSrc, index)
			continue
		}
		t := onlyTgt[match]
		onlyTgt = append(onlyTgt[:match:match], onlyTgt[match+1:]...)
		changes = append(changes, newChange("rename_index", SeverityOK,
			msg(fmt.Sprintf("Rename index `%s` → `%s`", t.Name, index.Name), "Same columns; only the name changes.", metadataOnly),
			"RENAME INDEX "+Quote(t.Name)+" TO "+Quote(index.Name), AlgoInplace).withLabel("instant"))
	}
	onlySrc = stillSrc

	type pair struct{ s, t Index }
	var changed []pair
	for _, k := range srcOrder {
		if t, ok := tgtIdx[k]; ok && srcIdx[k].key() != t.key() {
			s := srcIdx[k]
			if s.keyBase() == t.keyBase() &&
				strings.ReplaceAll(strings.ToLower(s.Extras), " invisible", "") == strings.ReplaceAll(strings.ToLower(t.Extras), " invisible", "") {
				text := msg(fmt.Sprintf("Make index `%s` visible", s.Name), "The optimizer can use it again.", runText[AlgoInstant])
				sev, clause := SeverityOK, "ALTER INDEX "+Quote(s.Name)+" VISIBLE"
				if s.Invisible {
					text = msg(fmt.Sprintf("Make index `%s` invisible", s.Name),
						"The optimizer stops using it, but MySQL keeps it up to date, so it can be made visible again instantly.",
						runText[AlgoInstant])
					sev, clause = SeveritySuggestion, "ALTER INDEX "+Quote(s.Name)+" INVISIBLE"
				}
				changes = append(changes, newChange("index_visibility", sev, text, clause, AlgoInstant))
				continue
			}
			changed = append(changed, pair{s, t})
		}
	}

	srcHasPK := false
	for _, s := range src.Indexes {
		if s.Kind == "PRIMARY" {
			srcHasPK = true
		}
	}
	oldIndex := map[string]Index{}
	for _, p := range changed {
		oldIndex[strings.ToLower(p.t.Name)] = p.t
	}
	drops := append([]Index{}, onlyTgt...)
	for _, p := range changed {
		drops = append(drops, p.t)
	}
	for _, t := range drops {
		if t.Kind == "PRIMARY" {
			if !srcHasPK {
				changes = append(changes, newChange("drop_index", SeverityWarning,
					msg(fmt.Sprintf("Drop the primary key (%s)", joinParts(t.Parts)),
						"No data is lost, but InnoDB falls back to a hidden row id, and row-based replication of a table "+
							"without a primary key is very slow.", runText[AlgoCopy]),
					"DROP PRIMARY KEY", AlgoCopy))
			}
			continue
		}
		if _, replaced := oldIndex[strings.ToLower(t.Name)]; replaced {
			continue
		}
		changes = append(changes, newChange("drop_index", SeveritySuggestion,
			msg(fmt.Sprintf("Drop index `%s` (%s)", t.Name, joinParts(t.Parts)),
				fmt.Sprintf("No data is lost, but queries that use this index may get slower. To test first, hide it with "+
					"`ALTER TABLE %s ALTER INDEX %s INVISIBLE` and watch performance.", tgt.Name, t.Name), metadataOnly),
			"DROP INDEX "+Quote(t.Name), AlgoInplace).withLabel("instant"))
	}

	adds := append([]Index{}, onlySrc...)
	for _, p := range changed {
		adds = append(adds, p.s)
	}
	for _, s := range adds {
		cols := joinParts(s.Parts)
		old, hasOld := oldIndex[strings.ToLower(s.Name)]
		if s.Kind == "PRIMARY" {
			var oldPK *Index
			for n := range tgt.Indexes {
				if tgt.Indexes[n].Kind == "PRIMARY" {
					oldPK = &tgt.Indexes[n]
					break
				}
			}
			headline, clause := fmt.Sprintf("Add a primary key (%s)", cols), "ADD "+IndexSQL(s)
			if oldPK != nil {
				headline = fmt.Sprintf("Change the primary key: (%s) → (%s)", joinParts(oldPK.Parts), cols)
				clause = "DROP PRIMARY KEY, " + clause
			}
			changes = append(changes, newChange("add_index", SeverityWarning,
				msg(headline, "Fails if those columns hold duplicate values or NULLs.",
					"MySQL rebuilds the table, because InnoDB stores rows in primary key order: reads and writes "+
						"continue, but it takes time and disk space on a big table."),
				clause, AlgoRebuild))
			continue
		}
		drop := ""
		if hasOld {
			drop = "DROP INDEX " + Quote(s.Name) + ", "
		}
		kind := map[string]string{"UNIQUE": "unique index", "FULLTEXT": "full-text index", "SPATIAL": "spatial index"}[s.Kind]
		if kind == "" {
			kind = "index"
		}
		var headline, data string
		if hasOld {
			what := fmt.Sprintf("%s → %s", strings.ToLower(old.Kind), kind)
			if joinParts(old.Parts) != cols {
				what = fmt.Sprintf("(%s) → (%s)", joinParts(old.Parts), cols)
			}
			headline = fmt.Sprintf("Change %s `%s`: %s", kind, s.Name, what)
			data = "No data is lost. The old index is dropped and built again with the new definition."
		} else {
			headline = fmt.Sprintf("Add %s `%s` (%s)", kind, s.Name, cols)
			data = "No data changes."
		}
		switch s.Kind {
		case "UNIQUE":
			names := strings.Join(s.Columns, ", ")
			if names == "" {
				names = strings.ReplaceAll(cols, "`", "")
			}
			changes = append(changes, newChange("add_index", SeverityWarning,
				msg(headline, fmt.Sprintf("The ALTER fails if the table already holds duplicate values. Check first: "+
					"`SELECT %s, COUNT(*) FROM %s GROUP BY %s HAVING COUNT(*) > 1`.", names, tgt.Name, names),
					"Reads and writes continue while it builds; the build reads the whole table."),
				drop+"ADD "+IndexSQL(s), AlgoInplace))
		case "FULLTEXT", "SPATIAL":
			run := "Writes to the table are blocked while it builds."
			if s.Kind == "FULLTEXT" {
				run = "Writes to the table are blocked while it builds (the table's first full-text index also rebuilds the table)."
			}
			changes = append(changes, newChange("add_index", SeverityWarning, msg(headline, data, run),
				drop+"ADD "+IndexSQL(s), AlgoRebuild).withLabel("blocks writes"))
		default:
			changes = append(changes, newChange("add_index", SeveritySuggestion,
				msg(headline, data, "Reads and writes continue while it builds; the build reads the whole table, so it "+
					"takes a while on a big one."),
				drop+"ADD "+IndexSQL(s), AlgoInplace))
		}
	}

	// Foreign keys and CHECK constraints, matched by definition.
	srcFKKeys := map[string]bool{}
	for _, f := range src.ForeignKeys {
		srcFKKeys[f.keyWith(f.Columns)] = true
	}
	tgtFKKeys := map[string]bool{}
	for _, f := range tgt.ForeignKeys {
		k := f.keyWith(renamed(f.Columns))
		tgtFKKeys[k] = true
		if !srcFKKeys[k] {
			cols := strings.Join(f.Columns, ", ")
			changes = append(changes, newChange("drop_fk", SeveritySuggestion,
				msg(fmt.Sprintf("Drop foreign key `%s` (%s → `%s`)", f.Name, cols, f.RefTable),
					fmt.Sprintf("No data is lost, but MySQL stops checking that %s values exist in `%s`.", cols, f.RefTable),
					runText[AlgoInstant]),
				"DROP FOREIGN KEY "+Quote(f.Name), AlgoInstant).withPhase(PhaseDropFK))
		}
	}
	for _, f := range src.ForeignKeys {
		if tgtFKKeys[f.keyWith(f.Columns)] {
			continue
		}
		changes = append(changes, newChange("add_fk", SeverityWarning,
			msg(fmt.Sprintf("Add foreign key `%s` (%s → `%s`)", f.Name, strings.Join(f.Columns, ", "), f.RefTable),
				fmt.Sprintf("The ALTER fails if any existing row points at a row missing from `%s`.", f.RefTable),
				"MySQL checks every row and blocks writes to the table while it runs."),
			"ADD "+constraintPrefix(f.AutoName, f.Name)+f.Body, AlgoCopy).withPhase(PhaseAddFK))
	}
	srcChecks, tgtChecks := map[string]bool{}, map[string]bool{}
	for _, c := range src.Checks {
		srcChecks[c.key] = true
	}
	for _, c := range tgt.Checks {
		tgtChecks[c.key] = true
	}
	for _, c := range tgt.Checks {
		if !srcChecks[c.key] {
			changes = append(changes, newChange("drop_check", SeveritySuggestion,
				msg(fmt.Sprintf("Drop CHECK constraint `%s` `%s`", c.Name, strings.ReplaceAll(c.Body, "`", "")),
					"No data is lost, but MySQL stops enforcing the rule.", runText[AlgoInstant]),
				"DROP CHECK "+Quote(c.Name), AlgoInstant))
		}
	}
	for _, c := range src.Checks {
		if !tgtChecks[c.key] {
			changes = append(changes, newChange("add_check", SeverityWarning,
				msg(fmt.Sprintf("Add CHECK constraint `%s` `%s`", c.Name, strings.ReplaceAll(c.Body, "`", "")),
					"The ALTER fails if any existing row breaks the rule.",
					"MySQL checks every row and blocks writes to the table while it runs."),
				"ADD "+constraintPrefix(c.AutoName, c.Name)+c.Body, AlgoCopy))
		}
	}

	changes = append(changes, optionChanges(src, tgt)...)
	if normExpr(src.Partition) != normExpr(tgt.Partition) {
		changes = append(changes, newChange("partition", SeverityWarning,
			msg("Partitioning differs (not changed by the script)",
				fmt.Sprintf("Source: `%s`. Target: `%s`.", shortPartition(src.Partition), shortPartition(tgt.Partition)),
				"Repartitioning copies the whole table, so plan it as a separate change."), "", ""))
	}

	// A large table that is rebuilt or copied: suggest an online schema change tool.
	if size, ok := sizes[strings.ToLower(tgt.Name)]; ok && (size.Bytes >= LargeTableBytes || size.Rows >= LargeTableRows) {
		heavy := false
		for _, c := range changes {
			if c.Phase == PhaseAlter && c.Clause != "" && algorithmOrder[c.Algorithm] >= algorithmOrder[AlgoRebuild] {
				heavy = true
			}
		}
		if heavy {
			changes = append(changes, newChange("large_table", SeverityWarning,
				msg(fmt.Sprintf("Large table: ~%s rows, %s", Thousands(size.Rows), HumanBytes(size.Bytes)),
					"This ALTER rebuilds or copies the table, which can take a long time and makes replicas fall behind. "+
						"Run it with pt-online-schema-change or gh-ost instead (a ready pt-online-schema-change command is "+
						"shown with this table's SQL), so writes are not blocked."), "", ""))
		}
	}
	return changes
}

func shortPartition(p string) string {
	if p == "" {
		p = "none"
	}
	s := strings.Join(strings.Fields(strings.ReplaceAll(p, "`", "")), " ")
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}

func constraintPrefix(auto bool, name string) string {
	if auto {
		return ""
	}
	return "CONSTRAINT " + Quote(name) + " "
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// optionChanges compares table options; only when both sides state them (hand-written DDL often leaves them out).
func optionChanges(src, tgt *Table) []Change {
	so, to := src.Options, tgt.Options
	var clauses, texts []string
	algorithm := AlgoInstant
	if so["ENGINE"] != "" && to["ENGINE"] != "" && so["ENGINE"] != to["ENGINE"] {
		engine := strings.ToUpper(so["ENGINE"])
		if so["ENGINE"] == "innodb" {
			engine = "InnoDB"
		}
		clauses = append(clauses, "ENGINE="+engine)
		texts = append(texts, fmt.Sprintf("engine %s → %s", to["ENGINE"], so["ENGINE"]))
		algorithm = AlgoCopy
	}
	charsetDiff := so["CHARSET"] != "" && to["CHARSET"] != "" && so["CHARSET"] != to["CHARSET"]
	collateDiff := so["COLLATE"] != "" && to["COLLATE"] != "" && so["COLLATE"] != to["COLLATE"]
	or := func(v, d string) string {
		if v == "" {
			return d
		}
		return v
	}
	if charsetDiff || collateDiff {
		clause := "DEFAULT CHARSET=" + so["CHARSET"]
		if so["COLLATE"] != "" {
			clause += " COLLATE=" + so["COLLATE"]
		}
		clauses = append(clauses, clause)
		texts = append(texts, fmt.Sprintf("default character set %s / %s → %s / %s", or(to["CHARSET"], "none"),
			or(to["COLLATE"], "default"), or(so["CHARSET"], "none"), or(so["COLLATE"], "default")))
	}
	for _, od := range [][2]string{{"ROW_FORMAT", "default"}, {"COMMENT", "''"}, {"KEY_BLOCK_SIZE", "0"},
		{"COMPRESSION", "'none'"}, {"STATS_PERSISTENT", "default"}, {"STATS_AUTO_RECALC", "default"},
		{"STATS_SAMPLE_PAGES", "default"}} {
		opt := od[0]
		sv, tv := or(so[opt], od[1]), or(to[opt], od[1])
		if _, ok := so[opt]; ok {
			sv = so[opt]
		}
		if _, ok := to[opt]; ok {
			tv = to[opt]
		}
		if sv != tv {
			v := sv
			if opt == "ROW_FORMAT" {
				v = strings.ToUpper(sv)
			}
			clauses = append(clauses, opt+"="+v)
			texts = append(texts, fmt.Sprintf("%s %s → %s", strings.ToLower(opt), tv, sv))
			if opt == "ROW_FORMAT" || opt == "KEY_BLOCK_SIZE" {
				algorithm = maxAlgo(algorithm, AlgoRebuild)
			}
		}
	}
	if len(clauses) == 0 {
		return nil
	}
	note := ""
	if charsetDiff || collateDiff {
		note = "The default character set applies only to columns added later; existing columns are changed by their own " +
			"MODIFY COLUMN above."
	}
	data := ""
	if algorithm != AlgoCopy {
		data = "No data changes."
	}
	sev := SeverityOK
	if algorithm != AlgoInstant {
		sev = SeverityWarning
	}
	return []Change{newChange("table_options", sev,
		msg("Change table options: "+strings.Join(texts, "; "), data, note, runText[algorithm]),
		strings.Join(clauses, ", "), algorithm)}
}

// TableAlgorithm is the most expensive algorithm among a table's ALTER clauses, or "".
func TableAlgorithm(changes []Change) string {
	a := ""
	for _, c := range changes {
		if c.Clause != "" && c.Phase == PhaseAlter {
			a = maxAlgo(a, c.Algorithm)
		}
	}
	return a
}

// WorstSeverity is the most severe severity among changes ("ok" when there are none).
func WorstSeverity(changes []Change) string {
	s := SeverityOK
	for _, c := range changes {
		s = worse(s, c.Severity)
	}
	return s
}

// Table statuses.
const (
	StatusCreate = "create" // only in the source
	StatusDrop   = "drop"   // only in the target
	StatusAlter  = "alter"  // changed
	StatusNotes  = "notes"  // differs, but nothing to script
	StatusSame   = "same"
)

// StatusText describes a status in a few words.
var StatusText = map[string]string{StatusCreate: "only in source · create", StatusDrop: "only in target",
	StatusAlter: "changed", StatusNotes: "differs (not scripted)", StatusSame: "identical"}

// TableDiff is the comparison of one table.
type TableDiff struct {
	Name      string   `json:"name"`
	Status    string   `json:"status"`
	Changes   []Change `json:"changes"`
	Source    *Table   `json:"-"`
	Target    *Table   `json:"-"`
	SourceDDL string   `json:"-"`
	TargetDDL string   `json:"-"`
}

// Result is the comparison of two schemas.
type Result struct {
	Tables []TableDiff `json:"tables"`
	Notes  []string    `json:"notes"`
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// DiffSchemas compares two {table name: CREATE TABLE} maps.
func DiffSchemas(source, target map[string]string, targetSizes Sizes, sourceDB, targetDB string) Result {
	var notes []string
	type parsed struct {
		t   *Table
		ddl string
	}
	parseAll := func(tables map[string]string, database, side string) map[string]parsed {
		out := map[string]parsed{}
		for _, name := range sortedKeys(tables) {
			if t := ParseTable(tables[name], database); t != nil {
				out[strings.ToLower(t.Name)] = parsed{t, tables[name]}
			} else {
				notes = append(notes, fmt.Sprintf("%s: could not read the CREATE TABLE of `%s`; skipped.", side, name))
			}
		}
		return out
	}
	src := parseAll(source, sourceDB, "Source")
	tgt := parseAll(target, targetDB, "Target")
	keySet := map[string]string{}
	for k := range src {
		keySet[k] = k
	}
	for k := range tgt {
		keySet[k] = k
	}
	var results []TableDiff
	for _, key := range sortedKeys(keySet) {
		s, sok := src[key]
		t, tok := tgt[key]
		var e TableDiff
		switch {
		case sok && !tok:
			e = TableDiff{Name: s.t.Name, Source: s.t, SourceDDL: s.ddl, Status: StatusCreate}
			e.Changes = append(e.Changes, newChange("create_table", SeverityOK,
				msg(fmt.Sprintf("Create table `%s` (%d columns, %d indexes)", s.t.Name, len(s.t.Columns), len(s.t.Indexes)),
					"It exists only in the source. A new, empty table: nothing else is affected."),
				CreateStatement(s.t, false), "").withPhase(PhaseCreate))
			for _, f := range s.t.ForeignKeys {
				e.Changes = append(e.Changes, newChange("add_fk", SeverityOK,
					msg(fmt.Sprintf("Add foreign key `%s` (%s → `%s`)", f.Name, strings.Join(f.Columns, ", "), f.RefTable),
						"Added after all tables exist; the new table is empty, so nothing can fail."),
					"ADD "+constraintPrefix(f.AutoName, f.Name)+f.Body, AlgoInstant).withPhase(PhaseAddFK))
			}
		case tok && !sok:
			extra := ""
			if size, ok := targetSizes[key]; ok {
				extra = fmt.Sprintf(" (~%s rows, %s)", Thousands(size.Rows), HumanBytes(size.Bytes))
			}
			e = TableDiff{Name: t.t.Name, Target: t.t, TargetDDL: t.ddl, Status: StatusDrop}
			e.Changes = []Change{newChange("drop_table", SeverityCritical,
				msg(fmt.Sprintf("Drop table `%s`%s", t.t.Name, extra),
					"It exists only in the target. Everything stored in it is deleted for good. The script leaves this "+
						"commented out unless --include-drops is given."),
				"DROP TABLE "+Quote(t.t.Name), "").withPhase(PhaseDrop).withDataLoss()}
		default:
			e = TableDiff{Name: s.t.Name, Source: s.t, Target: t.t, SourceDDL: s.ddl, TargetDDL: t.ddl}
			e.Changes = DiffTables(s.t, t.t, targetSizes)
			e.Status = StatusSame
			if len(e.Changes) > 0 {
				e.Status = StatusNotes
			}
			for _, c := range e.Changes {
				if c.Clause != "" {
					e.Status = StatusAlter
				}
			}
		}
		if e.Changes == nil {
			e.Changes = []Change{}
		}
		results = append(results, e)
	}
	if notes == nil {
		notes = []string{}
	}
	return Result{Tables: results, Notes: notes}
}

// Counts summarizes a result.
type Counts struct {
	Tables     int `json:"tables"`
	Create     int `json:"create"`
	Drop       int `json:"drop"`
	Alter      int `json:"alter"`
	Same       int `json:"same"`
	Critical   int `json:"critical"`
	Warning    int `json:"warning"`
	Suggestion int `json:"suggestion"`
	OK         int `json:"ok"`
}

// Summary counts tables by status and changes by severity.
func (r Result) Summary() Counts {
	c := Counts{Tables: len(r.Tables)}
	for _, t := range r.Tables {
		switch t.Status {
		case StatusCreate:
			c.Create++
		case StatusDrop:
			c.Drop++
		case StatusAlter:
			c.Alter++
		default:
			c.Same++
		}
		for _, ch := range t.Changes {
			switch ch.Severity {
			case SeverityCritical:
				c.Critical++
			case SeverityWarning:
				c.Warning++
			case SeveritySuggestion:
				c.Suggestion++
			default:
				c.OK++
			}
		}
	}
	return c
}

// HasDifferences is true when the script has something to change, or a table differs in a way it doesn't script.
func (r Result) HasDifferences() bool {
	for _, t := range r.Tables {
		if t.Status != StatusSame {
			return true
		}
	}
	return false
}

// SortedByRisk returns the tables that differ (or all, with includeSame), most severe first, then by name.
func (r Result) SortedByRisk(includeSame bool) []TableDiff {
	var out []TableDiff
	for _, t := range r.Tables {
		if includeSame || t.Status != StatusSame {
			out = append(out, t)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := SeverityOrder[WorstSeverity(out[i].Changes)], SeverityOrder[WorstSeverity(out[j].Changes)]
		if a != b {
			return a < b
		}
		return out[i].Name < out[j].Name
	})
	return out
}
