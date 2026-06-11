package sql

import "strings"

// formatSQL pretty-prints a T-SQL statement: major clauses start new lines,
// AND/OR conditions indent under their clause, top-level select-list commas
// break, and known keywords are uppercased. Strings, quoted identifiers, and
// comments pass through untouched. Best-effort by design — it never errors,
// and unparseable input just comes back mostly intact.
func formatSQL(in string) string {
	tokens := tokenizeSQL(in)
	if len(tokens) == 0 {
		return in
	}

	var b strings.Builder
	depth := 0
	lineStart := true
	prev := ""

	newline := func(extra int) {
		b.WriteString("\n" + strings.Repeat("  ", min(max(depth+extra, 0), 8)))
		lineStart = true
	}

	for i, tok := range tokens {
		up := strings.ToUpper(tok)

		switch {
		case tok == "(":
			glue := lineStart || prev == "(" ||
				(isWordToken(prev) && !sqlKeywords[strings.ToUpper(prev)]) // function call
			if !glue {
				b.WriteString(" ")
			}
			b.WriteString("(")
			depth++
			lineStart = false
			prev = tok
			continue
		case tok == ")":
			depth--
			b.WriteString(")")
			lineStart = false
			prev = tok
			continue
		case tok == ",":
			b.WriteString(",")
			if depth == 0 {
				newline(1)
			} else {
				b.WriteString(" ")
			}
			prev = tok
			continue
		case tok == ";":
			b.WriteString(";")
			if i < len(tokens)-1 {
				newline(0)
			}
			prev = tok
			continue
		}

		out := tok
		if sqlKeywords[up] {
			out = up
		}

		isClause := clauseStarts[up]
		switch up {
		case "LEFT", "RIGHT", "INNER", "FULL", "CROSS":
			// Join modifiers start a clause only as part of a JOIN — not as
			// functions like LEFT(x, 1).
			isClause = nextWordIs(tokens, i, "JOIN") || nextWordIs(tokens, i, "OUTER")
		case "JOIN":
			isClause = !prevWordIs(tokens, i, "INNER", "LEFT", "RIGHT", "FULL", "CROSS", "OUTER")
		case "GROUP", "ORDER":
			isClause = nextWordIs(tokens, i, "BY")
		}
		switch {
		case depth == 0 && isClause && !lineStart:
			newline(0)
		case depth == 0 && (up == "AND" || up == "OR" || up == "ON") && !lineStart:
			newline(1)
		}

		if !lineStart && !noSpaceBefore(tok) && prev != "(" && prev != "," {
			b.WriteString(" ")
		}
		b.WriteString(out)
		lineStart = false
		prev = tok
	}
	return strings.TrimSpace(b.String())
}

var clauseStarts = map[string]bool{
	"SELECT": true, "FROM": true, "WHERE": true, "GROUP": true, "ORDER": true,
	"HAVING": true, "UNION": true, "INSERT": true, "UPDATE": true, "DELETE": true,
	"SET": true, "VALUES": true, "JOIN": true, "INNER": true, "LEFT": true,
	"RIGHT": true, "FULL": true, "CROSS": true, "WITH": true, "OPTION": true,
	"OUTPUT": true, "MERGE": true,
}

var sqlKeywords = map[string]bool{
	"SELECT": true, "FROM": true, "WHERE": true, "GROUP": true, "BY": true,
	"ORDER": true, "HAVING": true, "UNION": true, "ALL": true, "INSERT": true,
	"UPDATE": true, "DELETE": true, "SET": true, "VALUES": true, "INTO": true,
	"JOIN": true, "INNER": true, "LEFT": true, "RIGHT": true, "FULL": true,
	"CROSS": true, "OUTER": true, "ON": true, "AND": true, "OR": true,
	"NOT": true, "NULL": true, "AS": true, "TOP": true, "DISTINCT": true,
	"CASE": true, "WHEN": true, "THEN": true, "ELSE": true, "END": true,
	"IN": true, "EXISTS": true, "LIKE": true, "BETWEEN": true, "IS": true,
	"WITH": true, "ASC": true, "DESC": true, "OPTION": true, "OUTPUT": true,
	"MERGE": true, "DECLARE": true, "EXEC": true,
}

func noSpaceBefore(tok string) bool {
	switch tok {
	case ",", ")", ";", ".":
		return true
	}
	return false
}

func isWordToken(tok string) bool {
	if tok == "" {
		return false
	}
	c := tok[0]
	return c == '_' || c == '@' || c == '#' || c == '[' ||
		(c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}

func prevWordIs(tokens []string, i int, words ...string) bool {
	if i == 0 {
		return false
	}
	prev := strings.ToUpper(tokens[i-1])
	for _, w := range words {
		if prev == w {
			return true
		}
	}
	return false
}

func nextWordIs(tokens []string, i int, word string) bool {
	return i+1 < len(tokens) && strings.EqualFold(tokens[i+1], word)
}

// tokenizeSQL splits SQL into tokens, keeping strings ('...'), quoted
// identifiers ("..." and [...]), and comments as single tokens, and
// discarding the original whitespace.
func tokenizeSQL(in string) []string {
	var tokens []string
	i := 0
	for i < len(in) {
		c := in[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			i++
		case c == '\'':
			j := i + 1
			for j < len(in) {
				if in[j] == '\'' {
					if j+1 < len(in) && in[j+1] == '\'' { // escaped quote
						j += 2
						continue
					}
					j++
					break
				}
				j++
			}
			tokens = append(tokens, in[i:j])
			i = j
		case c == '[':
			j := strings.IndexByte(in[i:], ']')
			if j < 0 {
				tokens = append(tokens, in[i:])
				return tokens
			}
			tokens = append(tokens, in[i:i+j+1])
			i += j + 1
		case c == '"':
			j := strings.IndexByte(in[i+1:], '"')
			if j < 0 {
				tokens = append(tokens, in[i:])
				return tokens
			}
			tokens = append(tokens, in[i:i+j+2])
			i += j + 2
		case c == '-' && i+1 < len(in) && in[i+1] == '-':
			j := strings.IndexByte(in[i:], '\n')
			if j < 0 {
				j = len(in) - i
			}
			tokens = append(tokens, strings.TrimRight(in[i:i+j], "\r"))
			i += j
		case c == '/' && i+1 < len(in) && in[i+1] == '*':
			j := strings.Index(in[i:], "*/")
			if j < 0 {
				j = len(in) - i
			} else {
				j += 2
			}
			tokens = append(tokens, in[i:i+j])
			i += j
		case c == '(' || c == ')' || c == ',' || c == ';':
			tokens = append(tokens, string(c))
			i++
		case isWordByte(c):
			j := i
			for j < len(in) && isWordByte(in[j]) {
				j++
			}
			tokens = append(tokens, in[i:j])
			i = j
		default: // operators and anything else: take a run of symbol chars
			j := i
			for j < len(in) && !isWordByte(in[j]) && !strings.ContainsRune(" \t\n\r()',;\"[", rune(in[j])) {
				j++
			}
			if j == i {
				j++
			}
			tokens = append(tokens, in[i:j])
			i = j
		}
	}
	return tokens
}

func isWordByte(c byte) bool {
	return c == '_' || c == '@' || c == '#' || c == '$' || c == '.' ||
		(c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}
