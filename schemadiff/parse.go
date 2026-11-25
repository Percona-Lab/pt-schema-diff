package schemadiff

// Parsing CREATE TABLE statements into columns, indexes, foreign keys, CHECK constraints and table options.

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Index is a primary key, index, unique, full-text or spatial index.
type Index struct {
	Kind      string   `json:"kind"` // PRIMARY, INDEX, UNIQUE, FULLTEXT, SPATIAL
	Name      string   `json:"name"`
	Parts     []string `json:"parts"`   // SQL of each key part: `col`(10) DESC, (expression)
	Columns   []string `json:"columns"` // lower-case column names of the plain key parts
	KeyParts  []string `json:"-"`       // comparable form of each key part
	Extras    string   `json:"extras,omitempty"`
	Invisible bool     `json:"invisible,omitempty"`
	Implicit  bool     `json:"implicit,omitempty"` // added by MySQL for a foreign key
}

func (i Index) keyBase() string { return i.Kind + "\x01" + strings.Join(i.KeyParts, "\x02") }
func (i Index) key() string     { return i.keyBase() + "\x01" + strings.ToLower(i.Extras) }

// ForeignKey is a FOREIGN KEY constraint.
type ForeignKey struct {
	Name        string   `json:"name"`
	Columns     []string `json:"columns"` // lower case
	ColumnNames []string `json:"-"`
	Body        string   `json:"definition"`
	RefTable    string   `json:"ref_table"`
	AutoName    bool     `json:"auto_name,omitempty"`
	keyRest     string
}

func (f ForeignKey) keyWith(cols []string) string {
	return strings.Join(cols, "\x02") + "\x01" + f.keyRest
}

// Check is a CHECK constraint.
type Check struct {
	Name     string `json:"name"`
	Body     string `json:"definition"`
	AutoName bool   `json:"auto_name,omitempty"`
	key      string
}

// ColumnAttrs are the comparable attributes of a column.
type ColumnAttrs struct {
	Type          string `json:"type"`
	Args          string `json:"args,omitempty"`
	Unsigned      bool   `json:"unsigned,omitempty"`
	Zerofill      bool   `json:"zerofill,omitempty"`
	NotNull       bool   `json:"not_null,omitempty"`
	HasDefault    bool   `json:"-"`
	Default       string `json:"default,omitempty"`
	HasComment    bool   `json:"-"`
	Comment       string `json:"comment,omitempty"`
	Other         string `json:"other,omitempty"`
	Charset       string `json:"charset,omitempty"`
	Collation     string `json:"collation,omitempty"`
	AutoIncrement bool   `json:"auto_increment,omitempty"`
	typeEnd       int
}

// Column is a column definition.
type Column struct {
	Name       string      `json:"name"`
	Definition string      `json:"definition"`
	Attrs      ColumnAttrs `json:"attrs"`
}

type tableItem struct{ kind, text string }

// Table is a parsed CREATE TABLE statement.
type Table struct {
	Name        string            `json:"name"`
	Columns     []Column          `json:"columns"`
	Indexes     []Index           `json:"indexes"`
	ForeignKeys []ForeignKey      `json:"foreign_keys"`
	Checks      []Check           `json:"checks"`
	Options     map[string]string `json:"options"`
	Partition   string            `json:"partition,omitempty"`
	items       []tableItem
	inlineKeys  []Index
	optionsText string
}

var (
	reCreateHead = regexp.MustCompile(`(?i)^\s*CREATE\s+(?:TEMPORARY\s+)?TABLE\s+(?:IF\s+NOT\s+EXISTS\s+)?(` + qualifiedRe + `)\s*`)

	indexKinds        = `(PRIMARY\s+KEY|UNIQUE(?:\s+(?:KEY|INDEX))?|FULLTEXT(?:\s+(?:KEY|INDEX))?|SPATIAL(?:\s+(?:KEY|INDEX))?|KEY|INDEX)`
	indexTail         = `(?:\s+(` + identRe + `))?(?:\s+USING\s+\w+)?\s*\(`
	reIndexHead       = regexp.MustCompile(`(?i)^(?:CONSTRAINT(?:\s+(` + identRe + `))?\s+)?` + indexKinds + indexTail)
	reIndexHeadNoName = regexp.MustCompile(`(?i)^(?:CONSTRAINT()\s+)?` + indexKinds + indexTail)
	reFKHead          = regexp.MustCompile(`(?i)^(?:CONSTRAINT(?:\s+(` + identRe + `))?\s+)?FOREIGN\s+KEY(?:\s+(` + identRe + `))?\s*\(`)
	reCheckHead       = regexp.MustCompile(`(?i)^(?:CONSTRAINT(?:\s+(` + identRe + `))?\s+)?CHECK\s*\(`)
	reColumnHead      = regexp.MustCompile(`(?s)^(` + identRe + `)\s+(\S.*)$`)
	reNotColumn       = regexp.MustCompile(`(?i)^(CONSTRAINT|PRIMARY|KEY|INDEX|UNIQUE|FOREIGN|CHECK|FULLTEXT|SPATIAL)\b`)
	reFKAction        = regexp.MustCompile(`(?i)\bON\s+(DELETE|UPDATE)\s+(RESTRICT|CASCADE|SET\s+NULL|SET\s+DEFAULT|NO\s+ACTION)`)
	reReferences      = regexp.MustCompile(`(?i)^\s*REFERENCES\s+(` + qualifiedRe + `)\s*\(`)
	reTableOption     = regexp.MustCompile(`(?i)\b(ENGINE|AUTO_INCREMENT|(?:DEFAULT\s+)?(?:CHARSET|CHARACTER\s+SET)|(?:DEFAULT\s+)?COLLATE|ROW_FORMAT|COMMENT|` +
		`KEY_BLOCK_SIZE|COMPRESSION|ENCRYPTION|STATS_PERSISTENT|STATS_AUTO_RECALC|STATS_SAMPLE_PAGES)\s*=?\s*('(?:[^'\\]|\\.|'')*'|\w+)`)
	rePartitionBy  = regexp.MustCompile(`(?i)\bPARTITION\s+BY\b`)
	reKeyPart      = regexp.MustCompile(`(?i)^(` + identRe + `)\s*(?:\(\s*(\d+)\s*\))?\s*(ASC|DESC)?`)
	reDescAtEnd    = regexp.MustCompile(`(?i)\bDESC\s*$`)
	reIdxComment   = regexp.MustCompile("(?i)\\bCOMMENT\\s*=?\\s*(\x00\\d+\x00)")
	reIdxParser    = regexp.MustCompile(`(?i)\bWITH\s+PARSER\s+(\w+)`)
	reInvisible    = regexp.MustCompile(`(?i)\bINVISIBLE\b`)
	reNotEnforced  = regexp.MustCompile(`(?i)\bNOT\s+ENFORCED\b`)
	reInlinePK     = regexp.MustCompile(`(?i)\bPRIMARY\s+KEY\b`)
	reInlineUnique = regexp.MustCompile(`(?i)\bUNIQUE(?:\s+KEY)?\b`)
	reInlineRef    = regexp.MustCompile(`(?is)\bREFERENCES\b.*$`)
	reAutoIncOpt   = regexp.MustCompile(`(?i)\bAUTO_INCREMENT\s*=?\s*\d+\s*`)
)

// ParseDump reads {table name: CREATE TABLE statement} from text (mysqldump --no-data, SHOW CREATE TABLE output, or
// hand-written DDL), and notes about what was skipped.
func ParseDump(dump string) (map[string]string, []string) {
	tables := map[string]string{}
	var notes []string
	for _, stmt := range SplitTopLevel(StripComments(dump), ';') {
		m := reCreateHead.FindStringSubmatchIndex(stmt)
		if m == nil {
			continue
		}
		q, _ := group(stmt, m, 1)
		name := lastName(q)
		if !strings.HasPrefix(stmt[m[1]:], "(") {
			notes = append(notes, fmt.Sprintf("`%s`: CREATE TABLE ... LIKE / AS SELECT has no column list to compare; skipped.", name))
			continue
		}
		for t := range tables {
			if strings.EqualFold(t, name) {
				notes = append(notes, fmt.Sprintf("`%s` is defined more than once; the last definition is used.", name))
				delete(tables, t)
			}
		}
		tables[name] = strings.TrimSpace(stmt)
	}
	return tables, notes
}

func keyPart(part string) (sql, key, column string) {
	part = strings.TrimSpace(part)
	if strings.HasPrefix(part, "(") {
		expr, end, ok := parenGroup(part, 0)
		desc := ""
		if !ok {
			expr = part
		} else if reDescAtEnd.MatchString(part[end:]) {
			desc = " DESC"
		}
		return "(" + strings.TrimSpace(expr) + ")" + desc, "(" + normExpr(expr) + ")" + strings.ToLower(desc), ""
	}
	m := reKeyPart.FindStringSubmatchIndex(part)
	if m == nil {
		return part, strings.ToLower(part), ""
	}
	raw, _ := group(part, m, 1)
	name := Unquote(raw)
	length := ""
	if n, ok := group(part, m, 2); ok {
		length = "(" + n + ")"
	}
	desc := ""
	if d, _ := group(part, m, 3); strings.EqualFold(d, "DESC") {
		desc = " DESC"
	}
	return Quote(name) + length + desc, strings.ToLower(name) + length + strings.ToLower(desc), name
}

// matchIndexHead matches an index definition head; CONSTRAINT PRIMARY KEY / CONSTRAINT UNIQUE KEY have no name.
func matchIndexHead(item string) []int {
	m := reIndexHead.FindStringSubmatchIndex(item)
	if m == nil {
		return nil
	}
	if name, ok := group(item, m, 1); ok && (strings.EqualFold(name, "PRIMARY") || strings.EqualFold(name, "UNIQUE")) {
		return reIndexHeadNoName.FindStringSubmatchIndex(item)
	}
	return m
}

func parseIndex(item string, m []int) *Index {
	inner, end, ok := parenGroup(item, m[1]-1)
	if !ok {
		return nil
	}
	kindText, _ := group(item, m, 2)
	kind := strings.ToUpper(collapse(kindText))
	switch kind {
	case "PRIMARY KEY":
		kind = "PRIMARY"
	case "KEY":
		kind = "INDEX"
	default:
		kind = strings.Fields(kind)[0]
	}
	idx := &Index{Kind: kind}
	for _, p := range SplitTopLevel(inner, ',') {
		sql, key, col := keyPart(p)
		idx.Parts = append(idx.Parts, sql)
		idx.KeyParts = append(idx.KeyParts, key)
		if col != "" {
			idx.Columns = append(idx.Columns, strings.ToLower(col))
		}
	}
	tail, lits := protect(item[end:], reLiteral)
	if pm := reIdxParser.FindStringSubmatch(tail); pm != nil {
		idx.Extras += " WITH PARSER " + strings.ToLower(pm[1])
	}
	if cm := reIdxComment.FindStringSubmatch(tail); cm != nil {
		idx.Extras += " COMMENT " + restore(cm[1], lits)
	}
	if reInvisible.MatchString(tail) {
		idx.Invisible = true
		idx.Extras += " INVISIBLE"
	}
	name, ok := group(item, m, 3)
	if !ok {
		name, _ = group(item, m, 1)
	}
	idx.Name = Unquote(name)
	if kind == "PRIMARY" {
		idx.Name = "PRIMARY"
	}
	return idx
}

// IndexSQL is the definition of an index for CREATE TABLE / ALTER TABLE ... ADD.
func IndexSQL(i Index) string {
	cols := strings.Join(i.Parts, ", ")
	if i.Kind == "PRIMARY" {
		return "PRIMARY KEY (" + cols + ")"
	}
	kind := map[string]string{"INDEX": "INDEX", "UNIQUE": "UNIQUE INDEX", "FULLTEXT": "FULLTEXT INDEX", "SPATIAL": "SPATIAL INDEX"}[i.Kind]
	return fmt.Sprintf("%s %s (%s)%s", kind, Quote(i.Name), cols, i.Extras)
}

func parseForeignKey(item string, m []int, database string) *ForeignKey {
	own, ownEnd, ok := parenGroup(item, m[1]-1)
	if !ok {
		return nil
	}
	rest := item[ownEnd:]
	rm := reReferences.FindStringSubmatchIndex(rest)
	if rm == nil {
		return nil
	}
	refCols, refEnd, ok := parenGroup(rest, rm[1]-1)
	if !ok {
		return nil
	}
	refQ, _ := group(rest, rm, 1)
	names := splitQualified(refQ)
	refDB := ""
	if len(names) == 2 {
		refDB = names[0]
	}
	if refDB != "" && database != "" && strings.EqualFold(refDB, database) {
		refDB = ""
	}
	actions := map[string]string{}
	for _, am := range reFKAction.FindAllStringSubmatch(rest[refEnd:], -1) {
		action := strings.ToUpper(collapse(am[2]))
		if action != "RESTRICT" && action != "NO ACTION" { // the default; SHOW CREATE TABLE leaves it out
			actions[strings.ToUpper(am[1])] = action
		}
	}
	fk := &ForeignKey{RefTable: names[len(names)-1]}
	var refColumns []string
	for _, c := range SplitTopLevel(own, ',') {
		fk.ColumnNames = append(fk.ColumnNames, Unquote(c))
		fk.Columns = append(fk.Columns, strings.ToLower(Unquote(c)))
	}
	for _, c := range SplitTopLevel(refCols, ',') {
		refColumns = append(refColumns, Unquote(c))
	}
	refTable := Quote(fk.RefTable)
	if refDB != "" {
		refTable = Quote(refDB) + "." + refTable
	}
	actionsSQL, actionKeys := "", []string{}
	for _, when := range []string{"DELETE", "UPDATE"} {
		if a, ok := actions[when]; ok {
			actionsSQL += " ON " + when + " " + a
			actionKeys = append(actionKeys, when+"="+a)
		}
	}
	sort.Strings(actionKeys)
	lowerRef := make([]string, len(refColumns))
	quotedOwn := make([]string, len(fk.ColumnNames))
	quotedRef := make([]string, len(refColumns))
	for i, c := range refColumns {
		lowerRef[i] = strings.ToLower(c)
		quotedRef[i] = Quote(c)
	}
	for i, c := range fk.ColumnNames {
		quotedOwn[i] = Quote(c)
	}
	fk.keyRest = strings.ToLower(refDB) + "." + strings.ToLower(fk.RefTable) + "\x01" + strings.Join(lowerRef, "\x02") + "\x01" + strings.Join(actionKeys, ",")
	fk.Body = fmt.Sprintf("FOREIGN KEY (%s) REFERENCES %s (%s)%s", strings.Join(quotedOwn, ", "), refTable, strings.Join(quotedRef, ", "), actionsSQL)
	if n, ok := group(item, m, 1); ok {
		fk.Name = Unquote(n)
	} else if n, ok := group(item, m, 2); ok {
		fk.Name = Unquote(n)
	}
	return fk
}

func parseCheck(item string, m []int) *Check {
	inner, end, ok := parenGroup(item, m[1]-1)
	if !ok {
		return nil
	}
	enforced := !reNotEnforced.MatchString(item[end:])
	c := &Check{key: fmt.Sprintf("%s\x01%v", normExpr(inner), enforced), Body: "CHECK (" + strings.TrimSpace(inner) + ")"}
	if !enforced {
		c.Body += " NOT ENFORCED"
	}
	if n, ok := group(item, m, 1); ok {
		c.Name = Unquote(n)
	}
	return c
}

var (
	intTypes    = map[string]int{"tinyint": 1, "smallint": 2, "mediumint": 3, "int": 4, "bigint": 8}
	textTypes   = map[string]int64{"tinytext": 255, "text": 65535, "mediumtext": 16777215, "longtext": 4294967295}
	stringTypes = map[string]bool{"char": true, "varchar": true, "enum": true, "set": true,
		"tinytext": true, "text": true, "mediumtext": true, "longtext": true}
	typeAliases = map[string]string{"integer": "int", "bool": "tinyint", "boolean": "tinyint", "dec": "decimal",
		"numeric": "decimal", "fixed": "decimal", "real": "double", "int1": "tinyint", "int2": "smallint",
		"int3": "mediumint", "int4": "int", "int8": "bigint", "middleint": "mediumint",
		"character": "char", "nchar": "char", "nvarchar": "varchar"}
	charsetMaxBytes = map[string]int{"latin1": 1, "ascii": 1, "binary": 1, "utf8mb3": 3, "ucs2": 2, "utf16": 4, "utf32": 4}

	reType           = regexp.MustCompile(`(?i)^(\w+)(?:\s+(?:precision|varying))?(?:\s*\(([^)]*)\))?((?:\s+(?:unsigned|signed|zerofill))*)`)
	reCharVarying    = regexp.MustCompile(`(?i)^character\s+varying`)
	reColCharset     = regexp.MustCompile(`\b(?:character set|charset) (\w+)`)
	reColCollate     = regexp.MustCompile(`\bcollate (\w+)`)
	reColCharsetAny  = regexp.MustCompile(`\b(?:character set|charset) \w+|\bcollate \w+`)
	reNotNull        = regexp.MustCompile(`\bnot null\b`)
	reNullWords      = regexp.MustCompile(`\bnot null\b|\bdefault null\b|\bnull\b`)
	reNowCall        = regexp.MustCompile(`\b(?:current_timestamp|now|localtimestamp|localtime)\s*\(\s*\)`)
	reNowWord        = regexp.MustCompile(`\b(?:now|localtimestamp|localtime)\b`)
	reNumDefault     = regexp.MustCompile(`\bdefault (-?\d+(?:\.\d+)?)\b`)
	reGeneratedAs    = regexp.MustCompile(`\bgenerated always as\b`)
	reParenSpaces    = regexp.MustCompile(`\s*([(),])\s*`)
	defaultValueRe   = "\\bdefault ('[^']*'|\x00\\d+\x00|\\([^)]*\\)|[\\w.-]+(?:\\(\\d*\\))?)"
	reDefault        = regexp.MustCompile(defaultValueRe)
	reComment        = regexp.MustCompile("\\bcomment (\x00\\d+\x00)")
	reDefaultComment = regexp.MustCompile(defaultValueRe + "|\\bcomment \x00\\d+\x00")
)

type parsedType struct {
	typ, args          string
	unsigned, zerofill bool
	rest               string
	typeEnd            int
}

func parseType(definition string) parsedType {
	p, lits := protect(strings.TrimSpace(definition), reLiteral)
	m := reType.FindStringSubmatchIndex(p)
	if m == nil {
		return parsedType{rest: definition}
	}
	word, _ := group(p, m, 1)
	base := strings.ToLower(word)
	if base == "character" && reCharVarying.MatchString(p) {
		base = "varchar"
	}
	if a, ok := typeAliases[base]; ok {
		base = a
	}
	flags, _ := group(p, m, 3)
	flags = strings.ToLower(flags)
	args := ""
	if a, ok := group(p, m, 2); ok {
		args = reSpaces.ReplaceAllString(restore(a, lits), "")
	}
	if w := strings.ToLower(word); w == "bool" || w == "boolean" {
		args = "1"
	}
	return parsedType{typ: base, args: args,
		unsigned: strings.Contains(flags, "unsigned"), zerofill: strings.Contains(flags, "zerofill"),
		rest: restore(p[m[1]:], lits), typeEnd: len(restore(p[:m[1]], lits))}
}

func columnAttrs(definition, tableCharset, tableCollation string) ColumnAttrs {
	t := parseType(definition)
	p, lits := protect(t.rest, reLiteral)
	low := collapse(strings.ToLower(p))

	charset, collation := "", ""
	if m := reColCharset.FindStringSubmatch(low); m != nil {
		charset = normCharset(m[1])
	}
	if m := reColCollate.FindStringSubmatch(low); m != nil {
		collation = normCharset(m[1])
	}
	low = reColCharsetAny.ReplaceAllString(low, " ")
	if stringTypes[t.typ] {
		if collation != "" && charset == "" {
			charset = strings.Split(collation, "_")[0]
		}
		if charset == "" {
			charset = normCharset(tableCharset)
		}
		if collation == "" && tableCollation != "" && charset == strings.Split(normCharset(tableCollation), "_")[0] {
			collation = normCharset(tableCollation)
		}
	} else {
		charset, collation = "", ""
	}

	a := ColumnAttrs{Type: t.typ, Unsigned: t.unsigned, Zerofill: t.zerofill, Charset: charset, Collation: collation,
		typeEnd: t.typeEnd}
	a.NotNull = reNotNull.MatchString(low)
	low = reNullWords.ReplaceAllString(low, " ")
	low = reNowCall.ReplaceAllString(low, "current_timestamp")
	low = reNowWord.ReplaceAllString(low, "current_timestamp")
	low = reNumDefault.ReplaceAllString(low, "default '${1}'")
	low = reGeneratedAs.ReplaceAllString(low, "as")
	low = reParenSpaces.ReplaceAllString(low, "${1}")
	low = collapse(low)
	if m := reDefault.FindStringSubmatch(low); m != nil {
		a.HasDefault, a.Default = true, restore(m[1], lits)
	}
	if m := reComment.FindStringSubmatch(low); m != nil {
		a.HasComment, a.Comment = true, restore(m[1], lits)
	}
	other := collapse(reDefaultComment.ReplaceAllString(low, " "))
	a.Other = restore(other, lits)
	a.AutoIncrement = strings.Contains(other, "auto_increment")

	a.Args = t.args
	switch {
	case intTypes[t.typ] > 0 && !t.zerofill:
		a.Args = "" // display widths are deprecated and not shown by MySQL 8.0.19+
	case t.typ == "decimal" && a.Args == "":
		a.Args = "10,0"
	case t.typ == "decimal" && !strings.Contains(a.Args, ","):
		a.Args += ",0"
	case (t.typ == "char" || t.typ == "binary") && a.Args == "":
		a.Args = "1"
	}
	return a
}

// sameColumn is true when two columns are the same, ignoring a charset/collation one side leaves to a default.
func sameColumn(a, b Column) bool {
	x, y := a.Attrs, b.Attrs
	if x.Type != y.Type || x.Args != y.Args || x.Unsigned != y.Unsigned || x.Zerofill != y.Zerofill ||
		x.NotNull != y.NotNull || x.HasDefault != y.HasDefault || x.Default != y.Default ||
		x.HasComment != y.HasComment || x.Comment != y.Comment || x.Other != y.Other {
		return false
	}
	if x.Charset != "" && y.Charset != "" && x.Charset != y.Charset {
		return false
	}
	if x.Collation != "" && y.Collation != "" && x.Collation != y.Collation {
		return false
	}
	return true
}

// ParseTable parses a CREATE TABLE statement, or returns nil when it can't. database is the schema the table lives in,
// so foreign keys to the same schema written with a qualified name compare equal to unqualified ones.
func ParseTable(ddl, database string) *Table {
	ddl = StripComments(ddl)
	hm := reCreateHead.FindStringSubmatchIndex(ddl)
	if hm == nil || !strings.HasPrefix(ddl[hm[1]:], "(") {
		return nil
	}
	body, bodyEnd, ok := parenGroup(ddl, hm[1])
	if !ok {
		return nil
	}
	q, _ := group(ddl, hm, 1)
	name := lastName(q)
	optionsText := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(ddl[bodyEnd:]), ";"))

	options := map[string]string{}
	partition := ""
	p, lits := protect(optionsText, reLiteral)
	if loc := rePartitionBy.FindStringIndex(p); loc != nil {
		partition = strings.TrimSpace(restore(p[loc[0]:], lits))
		p = p[:loc[0]]
	}
	for _, m := range reTableOption.FindAllStringSubmatch(restore(p, lits), -1) {
		key := strings.ReplaceAll(strings.ToUpper(collapse(m[1])), "DEFAULT ", "")
		if key == "CHARACTER SET" {
			key = "CHARSET"
		}
		if key == "COMMENT" {
			options[key] = m[2]
		} else {
			options[key] = strings.ToLower(m[2])
		}
	}
	if v, ok := options["CHARSET"]; ok {
		options["CHARSET"] = normCharset(v)
	}
	if v, ok := options["COLLATE"]; ok {
		options["COLLATE"] = normCharset(v)
		if _, ok := options["CHARSET"]; !ok {
			options["CHARSET"] = strings.Split(options["COLLATE"], "_")[0]
		}
	}

	t := &Table{Name: name, Options: options, Partition: partition, optionsText: optionsText}
	var inline, indexes []Index
	for _, item := range SplitTopLevel(body, ',') {
		if m := reFKHead.FindStringSubmatchIndex(item); m != nil {
			if fk := parseForeignKey(item, m, database); fk != nil {
				t.ForeignKeys = append(t.ForeignKeys, *fk)
				t.items = append(t.items, tableItem{"fk", item})
			}
			continue
		}
		if m := reCheckHead.FindStringSubmatchIndex(item); m != nil {
			if c := parseCheck(item, m); c != nil {
				t.Checks = append(t.Checks, *c)
				t.items = append(t.items, tableItem{"check", item})
			}
			continue
		}
		if m := matchIndexHead(item); m != nil {
			if idx := parseIndex(item, m); idx != nil {
				indexes = append(indexes, *idx)
				t.items = append(t.items, tableItem{"index", item})
			}
			continue
		}
		cm := reColumnHead.FindStringSubmatch(item)
		if cm == nil || reNotColumn.MatchString(item) {
			continue
		}
		colName := Unquote(cm[1])
		lower := strings.ToLower(colName)
		masked, mlits := protect(strings.TrimSpace(cm[2]), reLiteral)
		// Hand-written inline PRIMARY KEY / UNIQUE become table keys; inline REFERENCES are ignored by MySQL.
		if reInlinePK.MatchString(masked) {
			inline = append([]Index{{Kind: "PRIMARY", Name: "PRIMARY", Parts: []string{Quote(colName)},
				Columns: []string{lower}, KeyParts: []string{lower}}}, inline...)
			masked = reInlinePK.ReplaceAllString(masked, " ")
		}
		if reInlineUnique.MatchString(masked) {
			inline = append(inline, Index{Kind: "UNIQUE", Name: colName, Parts: []string{Quote(colName)},
				Columns: []string{lower}, KeyParts: []string{lower}})
			masked = reInlineUnique.ReplaceAllString(masked, " ")
		}
		masked = reInlineRef.ReplaceAllString(masked, " ")
		definition := collapse(restore(masked, mlits))
		t.Columns = append(t.Columns, Column{Name: colName, Definition: definition})
		t.items = append(t.items, tableItem{"column", Quote(colName) + " " + definition})
	}
	t.inlineKeys = inline
	t.Indexes = append(append([]Index{}, inline...), indexes...)
	pkColumns := map[string]bool{}
	for _, i := range t.Indexes {
		if i.Kind == "PRIMARY" {
			for _, c := range i.Columns {
				pkColumns[c] = true
			}
		}
	}
	for i := range t.Columns {
		t.Columns[i].Attrs = columnAttrs(t.Columns[i].Definition, options["CHARSET"], options["COLLATE"])
		// MySQL makes primary key columns NOT NULL even when the definition doesn't say so.
		if pkColumns[strings.ToLower(t.Columns[i].Name)] {
			t.Columns[i].Attrs.NotNull = true
		}
	}
	// Names MySQL gives unnamed indexes (first column, then _2, _3...), foreign keys and CHECK constraints.
	taken := map[string]bool{}
	for _, i := range t.Indexes {
		if i.Name != "" {
			taken[strings.ToLower(i.Name)] = true
		}
	}
	for n := range t.Indexes {
		if t.Indexes[n].Name != "" {
			continue
		}
		base := "functional_index"
		if len(t.Indexes[n].Columns) > 0 {
			base = t.Indexes[n].Columns[0]
		}
		candidate := base
		for k := 2; taken[strings.ToLower(candidate)]; k++ {
			candidate = fmt.Sprintf("%s_%d", base, k)
		}
		t.Indexes[n].Name = candidate
		taken[strings.ToLower(candidate)] = true
	}
	// MySQL adds an index for a foreign key that no index starts with, named after the constraint (or first column).
	for _, fk := range t.ForeignKeys {
		covered := false
		for _, i := range t.Indexes {
			if hasPrefix(i.Columns, fk.Columns) {
				covered = true
				break
			}
		}
		if !covered {
			indexName := fk.Name
			if indexName == "" {
				indexName = fk.Columns[0]
			}
			parts := make([]string, len(fk.ColumnNames))
			for n, c := range fk.ColumnNames {
				parts[n] = Quote(c)
			}
			t.Indexes = append(t.Indexes, Index{Kind: "INDEX", Name: indexName, Parts: parts,
				Columns: fk.Columns, KeyParts: append([]string{}, fk.Columns...), Implicit: true})
		}
	}
	for n := range t.ForeignKeys {
		t.ForeignKeys[n].AutoName = t.ForeignKeys[n].Name == ""
		if t.ForeignKeys[n].AutoName {
			t.ForeignKeys[n].Name = fmt.Sprintf("%s_ibfk_%d", name, n+1)
		}
	}
	for n := range t.Checks {
		t.Checks[n].AutoName = t.Checks[n].Name == ""
		if t.Checks[n].AutoName {
			t.Checks[n].Name = fmt.Sprintf("%s_chk_%d", name, n+1)
		}
	}
	return t
}

func hasPrefix(list, prefix []string) bool {
	if len(list) < len(prefix) {
		return false
	}
	for i := range prefix {
		if list[i] != prefix[i] {
			return false
		}
	}
	return true
}

// CreateStatement is the CREATE TABLE of a parsed table: unqualified name, no AUTO_INCREMENT counter.
func CreateStatement(t *Table, withForeignKeys bool) string {
	var items []string
	for _, it := range t.items {
		if it.kind == "fk" && !withForeignKeys {
			continue
		}
		items = append(items, it.text)
	}
	// Hand-written inline PRIMARY KEY / UNIQUE were removed from their column definitions; add them back.
	for _, i := range t.inlineKeys {
		items = append(items, IndexSQL(i))
	}
	options := strings.TrimSpace(reAutoIncOpt.ReplaceAllString(t.optionsText, ""))
	out := "CREATE TABLE " + Quote(t.Name) + " (\n  " + strings.Join(items, ",\n  ") + "\n)"
	if options != "" {
		out += " " + options
	}
	return out
}
