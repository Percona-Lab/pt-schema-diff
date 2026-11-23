package schemadiff

// Low-level text helpers for reading MySQL DDL: quoting, comments, literals and balanced parentheses.
// Go's regexp package (RE2) has no lookaround, so the few places that need it are done by hand.

import (
	"regexp"
	"strconv"
	"strings"
)

const (
	identRe       = "(?:`(?:[^`]|``)+`|[\\w$]+)"
	qualifiedRe   = identRe + `(?:\s*\.\s*` + identRe + `)?`
	textLiteralRe = `'(?:[^'\\]|\\.|'')*'|"(?:[^"\\]|\\.|"")*"`
	literalRe     = textLiteralRe + "|`(?:[^`]|``)*`"
)

var (
	// Literals and quoted names are kept; versioned comments (/*!50100 PARTITION BY ... */, /*!80023 INVISIBLE */) are
	// unwrapped because mysqldump and SHOW CREATE TABLE put real definitions in them; other comments are dropped.
	reLiteralOrComment = regexp.MustCompile(`(?s)` + literalRe + `|/\*!\d*(.*?)\*/|/\*.*?\*/|--(?:[^\S\n][^\n]*|\n|$)|#[^\n]*`)
	reLiteral          = regexp.MustCompile(`(?s)` + literalRe)
	reTextLiteral      = regexp.MustCompile(`(?s)` + textLiteralRe)
	rePlaceholder      = regexp.MustCompile("\x00(\\d+)\x00")
	reSpaces           = regexp.MustCompile(`\s+`)
	reBacktickName     = regexp.MustCompile("`((?:[^`]|``)+)`")
)

// replaceFunc is regexp.ReplaceAllStringFunc with access to the submatch indexes.
func replaceFunc(re *regexp.Regexp, s string, f func(m []int) string) string {
	var b strings.Builder
	last := 0
	for _, m := range re.FindAllStringSubmatchIndex(s, -1) {
		b.WriteString(s[last:m[0]])
		b.WriteString(f(m))
		last = m[1]
	}
	b.WriteString(s[last:])
	return b.String()
}

// group returns submatch n of m in s, and whether it took part in the match.
func group(s string, m []int, n int) (string, bool) {
	if 2*n+1 >= len(m) || m[2*n] < 0 {
		return "", false
	}
	return s[m[2*n]:m[2*n+1]], true
}

func collapse(s string) string { return strings.TrimSpace(reSpaces.ReplaceAllString(s, " ")) }

// StripComments removes SQL comments, keeping string literals, quoted names and the contents of versioned comments.
func StripComments(sql string) string {
	return replaceFunc(reLiteralOrComment, sql, func(m []int) string {
		whole := sql[m[0]:m[1]]
		if strings.ContainsRune("'\"`", rune(whole[0])) {
			return whole
		}
		if inner, ok := group(sql, m, 1); ok {
			return " " + inner + " "
		}
		return " "
	})
}

// SplitTopLevel splits on a separator outside quotes and parentheses; empty trailing parts are dropped.
func SplitTopLevel(text string, sep byte) []string {
	var parts []string
	var buf strings.Builder
	depth := 0
	var quote byte
	for i := 0; i < len(text); i++ {
		ch := text[i]
		switch {
		case quote != 0:
			buf.WriteByte(ch)
			if ch == '\\' && quote != '`' && i+1 < len(text) {
				buf.WriteByte(text[i+1])
				i++
			} else if ch == quote {
				quote = 0
			}
		case ch == '\'' || ch == '"' || ch == '`':
			quote = ch
			buf.WriteByte(ch)
		case ch == '(':
			depth++
			buf.WriteByte(ch)
		case ch == ')':
			depth--
			buf.WriteByte(ch)
		case ch == sep && depth == 0:
			parts = append(parts, strings.TrimSpace(buf.String()))
			buf.Reset()
		default:
			buf.WriteByte(ch)
		}
	}
	if rest := strings.TrimSpace(buf.String()); rest != "" {
		parts = append(parts, rest)
	}
	return parts
}

// Unquote removes the backticks around an identifier.
func Unquote(name string) string {
	name = strings.TrimSpace(name)
	if strings.HasPrefix(name, "`") {
		inner := name[1:]
		if strings.HasSuffix(inner, "`") {
			inner = inner[:len(inner)-1]
		}
		return strings.ReplaceAll(inner, "``", "`")
	}
	return name
}

// Quote puts backticks around an identifier.
func Quote(name string) string { return "`" + strings.ReplaceAll(name, "`", "``") + "`" }

// splitQualified splits `db`.`table` (or db.table) into its unquoted parts.
func splitQualified(q string) []string {
	var parts []string
	var buf strings.Builder
	inQuote := false
	for i := 0; i < len(q); i++ {
		ch := q[i]
		switch {
		case ch == '`':
			inQuote = !inQuote
			buf.WriteByte(ch)
		case ch == '.' && !inQuote:
			parts = append(parts, Unquote(buf.String()))
			buf.Reset()
		default:
			buf.WriteByte(ch)
		}
	}
	return append(parts, Unquote(buf.String()))
}

func lastName(q string) string {
	p := splitQualified(q)
	return p[len(p)-1]
}

// parenGroup returns the text inside the parenthesis opening at text[start] and the index after its closing one.
func parenGroup(text string, start int) (string, int, bool) {
	depth := 0
	var quote byte
	for i := start; i < len(text); i++ {
		ch := text[i]
		switch {
		case quote != 0:
			if ch == '\\' && quote != '`' {
				i++
			} else if ch == quote {
				quote = 0
			}
		case ch == '\'' || ch == '"' || ch == '`':
			quote = ch
		case ch == '(':
			depth++
		case ch == ')':
			depth--
			if depth == 0 {
				return text[start+1 : i], i + 1, true
			}
		}
	}
	return "", 0, false
}

// protect replaces the matches of re (string literals, quoted names) with placeholders, so later regexes can't match
// inside them. restore puts them back.
func protect(text string, re *regexp.Regexp) (string, []string) {
	var lits []string
	out := re.ReplaceAllStringFunc(text, func(s string) string {
		lits = append(lits, s)
		return "\x00" + strconv.Itoa(len(lits)-1) + "\x00"
	})
	return out, lits
}

func restore(text string, lits []string) string {
	return rePlaceholder.ReplaceAllStringFunc(text, func(s string) string {
		n, err := strconv.Atoi(s[1 : len(s)-1])
		if err != nil || n >= len(lits) {
			return s
		}
		return lits[n]
	})
}

func normCharset(name string) string {
	name = strings.ToLower(name)
	if name == "utf8" || strings.HasPrefix(name, "utf8_") {
		return "utf8mb3" + name[4:]
	}
	return name
}

// normExpr is the comparable form of an expression (CHECK, functional index part): lower case, no quotes around
// names, no spaces, no redundant outer parentheses.
func normExpr(expr string) string {
	p, lits := protect(expr, reTextLiteral)
	p = strings.ToLower(p)
	p = reBacktickName.ReplaceAllStringFunc(p, func(s string) string {
		return strings.ReplaceAll(s[1:len(s)-1], "``", "`")
	})
	p = reSpaces.ReplaceAllString(p, "")
	for strings.HasPrefix(p, "(") {
		inner, end, ok := parenGroup(p, 0)
		if !ok || end != len(p) {
			break
		}
		p = inner
	}
	return restore(p, lits)
}

var (
	reClientNoise      = regexp.MustCompile(`(?im)^\s*(?:\d+ rows? in set.*|Empty set.*|Query OK.*|Records: \d+.*|\*+ \d+\. row \*+|Table: \S+|mysql>\s*)$`)
	reCreateTableLabel = regexp.MustCompile(`(?im)^\s*Create Table:\s*`)
	reStatementStart   = regexp.MustCompile(`(?im)^[ \t]*(?:CREATE\s+(?:TEMPORARY\s+)?TABLE|CREATE\s+(?:UNIQUE\s+)?INDEX|ALTER\s+TABLE|INSERT\s+INTO|DROP\s+(?:TABLE|INDEX)|SELECT|WITH)\b`)
)

// CleanPastedDDL drops mysql client output ("1 row in set", \G row headers) and adds missing semicolons between
// statements, so SHOW CREATE TABLE output copied from a terminal can be read.
func CleanPastedDDL(text string) string {
	text = reClientNoise.ReplaceAllString(text, "")
	text = reCreateTableLabel.ReplaceAllString(text, "")
	text = strings.ReplaceAll(text, `\G`, ";")
	starts := reStatementStart.FindAllStringIndex(text, -1)
	var b strings.Builder
	last := 0
	for _, s := range starts {
		if s[0] == 0 {
			continue
		}
		b.WriteString(text[last:s[0]])
		last = s[0]
		cur := b.String()
		if strings.TrimSpace(cur) != "" && !strings.HasSuffix(strings.TrimRight(cur, " \t\r\n"), ";") {
			trimmed := strings.TrimRight(cur, " \t\r\n")
			b.Reset()
			b.WriteString(trimmed + ";\n")
		}
	}
	b.WriteString(text[last:])
	return b.String()
}
