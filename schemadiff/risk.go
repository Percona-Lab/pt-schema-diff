package schemadiff

// Rating a change for data-loss risk and for how MySQL 8.0 runs it:
//
//	INSTANT  metadata only, no rebuild
//	INPLACE  online, reads and writes continue (an index build still reads the whole table)
//	REBUILD  online, but the table is rebuilt (time and disk space grow with the table)
//	COPY     the table is copied and writes are blocked for the whole copy

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Severities, most severe first.
const (
	SeverityCritical   = "critical"
	SeverityWarning    = "warning"
	SeveritySuggestion = "suggestion"
	SeverityOK         = "ok"
)

// Algorithms MySQL uses to run an ALTER, cheapest first.
const (
	AlgoInstant = "INSTANT"
	AlgoInplace = "INPLACE"
	AlgoRebuild = "REBUILD"
	AlgoCopy    = "COPY"
)

var (
	// SeverityOrder ranks severities, most severe first.
	SeverityOrder  = map[string]int{SeverityCritical: 0, SeverityWarning: 1, SeveritySuggestion: 2, SeverityOK: 3}
	Severities     = []string{SeverityCritical, SeverityWarning, SeveritySuggestion, SeverityOK}
	algorithmOrder = map[string]int{"": 0, AlgoInstant: 1, AlgoInplace: 2, AlgoRebuild: 3, AlgoCopy: 4}

	// RunLabels says in a few words how MySQL runs a change.
	RunLabels = map[string]string{AlgoInstant: "instant", AlgoInplace: "online", AlgoRebuild: "online, rebuilds table",
		AlgoCopy: "blocks writes"}
	// RunHelp explains the labels.
	RunHelp = [][2]string{
		{"instant", "only the table definition changes"},
		{"online", "reads and writes continue while it runs"},
		{"online, rebuilds table", "reads and writes continue, but the whole table is rewritten"},
		{"blocks writes", "the table is copied or checked row by row, and writes wait until it finishes"},
	}
	runText = map[string]string{
		AlgoInstant: "MySQL only updates the table definition, so it is instant.",
		AlgoInplace: "Reads and writes continue while it runs.",
		AlgoRebuild: "MySQL rebuilds the table: reads and writes continue, but it takes time and disk space on a big table.",
		AlgoCopy:    "MySQL copies the whole table and blocks writes to it until the copy finishes.",
	}
)

const (
	metadataOnly = "MySQL only updates the table definition (no rebuild); reads and writes continue."
	safe         = "Safe for the data."
	// A table this large is worth changing with pt-online-schema-change or gh-ost when the ALTER rebuilds or copies it.
	LargeTableBytes = 1 << 30
	LargeTableRows  = 1_000_000
)

func maxAlgo(a, b string) string {
	if algorithmOrder[b] > algorithmOrder[a] {
		return b
	}
	return a
}

func worse(a, b string) string {
	if SeverityOrder[b] < SeverityOrder[a] {
		return b
	}
	return a
}

// HumanBytes formats a byte count: 512 B, 1.5 GB.
func HumanBytes(n int64) string {
	f := float64(n)
	for _, unit := range []string{"B", "KB", "MB", "GB", "TB"} {
		if f < 1024 || unit == "TB" {
			if unit == "B" {
				return fmt.Sprintf("%.0f %s", f, unit)
			}
			return fmt.Sprintf("%.1f %s", f, unit)
		}
		f /= 1024
	}
	return ""
}

// Thousands formats an integer with thousands separators.
func Thousands(n int64) string {
	s := strconv.FormatInt(n, 10)
	neg := strings.HasPrefix(s, "-")
	if neg {
		s = s[1:]
	}
	var b strings.Builder
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(c)
	}
	if neg {
		return "-" + b.String()
	}
	return b.String()
}

func atoi(s string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(s))
	return n
}

func stringBytes(a ColumnAttrs) int {
	n, err := strconv.Atoi(a.Args)
	if err != nil {
		n = 0
	}
	per, ok := charsetMaxBytes[a.Charset]
	if !ok {
		per = 4
	}
	return n * per
}

func charsetBytes(cs string) int {
	if n, ok := charsetMaxBytes[cs]; ok {
		return n
	}
	return 4
}

func typeText(a ColumnAttrs) string {
	s := a.Type
	if a.Args != "" {
		s += "(" + a.Args + ")"
	}
	if a.Unsigned {
		s += " unsigned"
	}
	return s
}

// msg is a finding: a bold headline, then plain sentences (what happens to the data, how MySQL runs it).
func msg(headline string, sentences ...string) string {
	var kept []string
	for _, s := range sentences {
		if s != "" {
			kept = append(kept, s)
		}
	}
	return "**" + headline + ".** " + strings.Join(kept, " ")
}

type risk struct{ severity, text string }

func orNone(has bool, v string) string {
	if !has || v == "" {
		return "none"
	}
	return v
}

// modifyRisk rates changing column name of table from the old to the new attributes. It returns the severity, the
// headline, the data sentences, the algorithm MySQL 8.0 uses and the sentence describing how it runs.
func modifyRisk(table, name string, o, n ColumnAttrs) (string, string, []string, string, string) {
	var risks []risk
	var what []string
	algorithm, run := AlgoInstant, ""
	typeChanged := o.Type != n.Type || o.Args != n.Args || o.Unsigned != n.Unsigned || o.Zerofill != n.Zerofill
	tooLong := fmt.Sprintf("Values longer than %s characters are rejected (strict mode) or cut off. Check first: "+
		"`SELECT MAX(CHAR_LENGTH(%s)) FROM %s`.", n.Args, name, table)
	if typeChanged {
		algorithm = AlgoCopy
		what = append(what, fmt.Sprintf("`%s` → `%s`", typeText(o), typeText(n)))
		ot, nt := o.Type, n.Type
		switch {
		case intTypes[ot] > 0 && intTypes[nt] > 0:
			if intTypes[nt] < intTypes[ot] || o.Unsigned != n.Unsigned {
				risks = append(risks, risk{SeverityCritical, "Values outside the new range are rejected (strict mode) or clipped. " +
					fmt.Sprintf("Check first: `SELECT MIN(%s), MAX(%s) FROM %s`.", name, name, table)})
			} else {
				risks = append(risks, risk{SeverityWarning, safe + " The new type holds every current value."})
			}
		case (ot == "varchar" || ot == "char") && nt == ot:
			switch {
			case atoi(n.Args) < atoi(o.Args):
				risks = append(risks, risk{SeverityCritical, tooLong})
			case ot == "varchar" && (stringBytes(o) <= 255) == (stringBytes(n) <= 255):
				algorithm, run = AlgoInplace, metadataOnly
				risks = append(risks, risk{SeverityOK, safe + " The limit only gets bigger."})
			case ot == "varchar":
				risks = append(risks, risk{SeverityWarning, safe + " But MySQL has to copy the table: past 255 bytes, every value " +
					"needs a 2-byte length field instead of 1."})
			default:
				risks = append(risks, risk{SeverityWarning, safe + " The limit only gets bigger."})
			}
		case (ot == "enum" || ot == "set") && nt == ot:
			ov, nv := SplitTopLevel(o.Args, ','), SplitTopLevel(n.Args, ',')
			if hasPrefix(nv, ov) {
				algorithm = AlgoInstant
				risks = append(risks, risk{SeverityOK, safe + " Values are only added at the end of the list."})
			} else if removed := missing(ov, nv); len(removed) > 0 {
				risks = append(risks, risk{SeverityCritical, fmt.Sprintf("Removes %s: rows holding them become '' (or the ALTER fails in "+
					"strict mode).", strings.Join(removed, ", "))})
			} else {
				risks = append(risks, risk{SeverityWarning, "Values are reordered or inserted in the middle of the list, so MySQL " +
					"renumbers every stored value."})
			}
		case textTypes[ot] > 0 && textTypes[nt] > 0:
			if textTypes[nt] < textTypes[ot] {
				risks = append(risks, risk{SeverityCritical, "Values longer than the new type allows are rejected (strict mode) or cut " +
					fmt.Sprintf("off. Check first: `SELECT MAX(LENGTH(%s)) FROM %s`.", name, table)})
			} else {
				risks = append(risks, risk{SeverityWarning, safe + " The limit only gets bigger."})
			}
		case textTypes[ot] > 0 && (nt == "varchar" || nt == "char"):
			risks = append(risks, risk{SeverityCritical, tooLong})
		case ot == "decimal" && nt == "decimal":
			op, os, ok1 := decimalArgs(o.Args)
			np, ns, ok2 := decimalArgs(n.Args)
			if !ok1 || !ok2 {
				op, os, np, ns = 0, 0, 0, 0
			}
			if np-ns < op-os || ns < os {
				risks = append(risks, risk{SeverityCritical, "Fewer digits: values that don't fit are rounded or rejected."})
			} else {
				risks = append(risks, risk{SeverityWarning, safe + " The new type holds every current value."})
			}
		case (ot == "datetime" || ot == "timestamp" || ot == "time") && nt == ot:
			if atoi(n.Args) < atoi(o.Args) {
				risks = append(risks, risk{SeverityWarning, "Fewer fractional-second digits: stored values are rounded."})
			} else {
				risks = append(risks, risk{SeveritySuggestion, safe + " More fractional-second digits."})
			}
		default:
			risks = append(risks, risk{SeverityCritical, "A different kind of type: every value is converted and can change, be cut " +
				"off or be rejected. Test on a copy first."})
		}
	}

	if o.NotNull != n.NotNull {
		algorithm = maxAlgo(algorithm, AlgoRebuild)
		if n.NotNull {
			what = append(what, "NULL allowed → NOT NULL")
			risks = append(risks, risk{SeverityWarning, "The ALTER fails (strict mode) if any row holds NULL. Check first: " +
				fmt.Sprintf("`SELECT COUNT(*) FROM %s WHERE %s IS NULL`.", table, name)})
		} else {
			what = append(what, "NOT NULL → NULL allowed")
			risks = append(risks, risk{SeveritySuggestion, safe})
		}
	}

	if (o.Charset != "" && n.Charset != "" && o.Charset != n.Charset) ||
		(o.Collation != "" && n.Collation != "" && o.Collation != n.Collation) {
		algorithm = AlgoCopy
		if o.Charset != n.Charset {
			what = append(what, fmt.Sprintf("character set %s → %s", o.Charset, n.Charset))
		} else {
			what = append(what, fmt.Sprintf("collation %s → %s", o.Collation, n.Collation))
		}
		if charsetBytes(n.Charset) < charsetBytes(o.Charset) {
			risks = append(risks, risk{SeverityCritical, "Characters the new character set can't store (for example emoji) are " +
				"replaced or rejected."})
		} else {
			risks = append(risks, risk{SeverityWarning, "Sorting, comparisons and unique checks follow the new rules, and joins to " +
				"columns that keep the old collation can no longer use an index."})
		}
	}

	if o.AutoIncrement != n.AutoIncrement {
		algorithm = AlgoCopy
		if n.AutoIncrement {
			what = append(what, "adds AUTO_INCREMENT")
		} else {
			what = append(what, "removes AUTO_INCREMENT")
		}
		risks = append(risks, risk{SeverityWarning, ""})
	}

	oo := strings.Fields(strings.ReplaceAll(o.Other, "auto_increment", ""))
	no := strings.Fields(strings.ReplaceAll(n.Other, "auto_increment", ""))
	if strings.Join(oo, " ") != strings.Join(no, " ") && !typeChanged {
		algorithm = maxAlgo(algorithm, AlgoCopy)
		dash := func(s string) string {
			s = strings.ReplaceAll(s, "`", "")
			if s == "" {
				return "-"
			}
			return s
		}
		what = append(what, fmt.Sprintf("`%s` → `%s`", dash(o.Other), dash(n.Other)))
		risks = append(risks, risk{SeverityWarning, "Other column attributes change; check them before running."})
	}

	if o.HasDefault != n.HasDefault || o.Default != n.Default {
		what = append(what, fmt.Sprintf("default %s → %s", orNone(o.HasDefault, o.Default), orNone(n.HasDefault, n.Default)))
	}
	if o.HasComment != n.HasComment || o.Comment != n.Comment {
		what = append(what, "comment changes")
	}
	if len(risks) == 0 {
		risks = append(risks, risk{SeverityOK, safe + " Existing rows keep their values."})
	}
	severity := SeverityOK
	var data []string
	for _, r := range risks {
		severity = worse(severity, r.severity)
		if r.text != "" {
			data = append(data, r.text)
		}
	}
	if len(what) == 0 {
		what = []string{"same meaning, written differently"}
	}
	// A longer VARCHAR alone is metadata-only, but a NOT NULL or character set change in the same MODIFY makes
	// MySQL rebuild or copy the table, so the metadata-only sentence no longer applies.
	if run == metadataOnly && algorithm != AlgoInplace {
		run = ""
	}
	if run == "" {
		run = runText[algorithm]
	}
	return severity, fmt.Sprintf("Change column `%s`: %s", name, strings.Join(what, ", ")), data, algorithm, run
}

func decimalArgs(args string) (int, int, bool) {
	parts := strings.Split(args, ",")
	if len(parts) != 2 {
		return 0, 0, false
	}
	p, err1 := strconv.Atoi(strings.TrimSpace(parts[0]))
	s, err2 := strconv.Atoi(strings.TrimSpace(parts[1]))
	return p, s, err1 == nil && err2 == nil
}

// missing returns the values of a that are not in b, sorted.
func missing(a, b []string) []string {
	in := map[string]bool{}
	for _, v := range b {
		in[v] = true
	}
	seen := map[string]bool{}
	var out []string
	for _, v := range a {
		if !in[v] && !seen[v] {
			out = append(out, v)
			seen[v] = true
		}
	}
	sort.Strings(out)
	return out
}
