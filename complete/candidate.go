// Package complete implements the SQL completion engine.
//
// The engine itself is database-agnostic: it is only responsible for
//  1. dispatching a completion request (context + prefix + metadata snapshot)
//     to each Source;
//  2. scoring, deduplicating, sorting, and truncating the candidates.
//
// What exactly gets suggested is decided by the Source, so adding support for a
// new database requires no changes to this package.
package complete

import "strings"

// Kind identifies the candidate type.
type Kind int

// Candidate kinds.
const (
	KindKeyword Kind = iota
	KindFunction
	KindSnippet
	KindSchema
	KindTable
	KindView
	KindColumn
	// Non-relation catalog objects. These are appended after the existing
	// kinds so that the iota value of every kind above never changes.
	KindProcedure
	KindTrigger
	KindIndex
	KindSequence
)

// String returns the type name (used for the status bar and debugging).
func (k Kind) String() string {
	switch k {
	case KindKeyword:
		return "keyword"
	case KindFunction:
		return "function"
	case KindSnippet:
		return "snippet"
	case KindSchema:
		return "schema"
	case KindTable:
		return "table"
	case KindView:
		return "view"
	case KindColumn:
		return "column"
	case KindProcedure:
		return "procedure"
	case KindTrigger:
		return "trigger"
	case KindIndex:
		return "index"
	case KindSequence:
		return "sequence"
	default:
		return "unknown"
	}
}

// priority is the tie-break ordering priority (higher ranks earlier).
func (k Kind) priority() int {
	switch k {
	case KindTable:
		return 60
	case KindView:
		return 55
	case KindColumn:
		return 50
	case KindSchema:
		return 45
	case KindFunction:
		return 40
	// Non-relation catalog objects rank between callable functions and
	// snippets: they are real catalog objects worth naming, but never more
	// important than a relation or a column. A procedure deliberately shares
	// the function tier (both are callable; the label breaks the tie).
	case KindProcedure:
		return 40
	case KindSequence:
		return 39
	case KindTrigger:
		return 38
	case KindIndex:
		return 37
	case KindSnippet:
		return 30
	default:
		return 20
	}
}

// Candidate is a single completion candidate.
type Candidate struct {
	Kind Kind
	// Label is the text shown and inserted by default.
	Label string
	// Insert is the actual inserted text; when empty it equals Label.
	Insert string
	// Detail is a short note on the right of the list (for example, a column type).
	Detail string
	// Doc is the long description shown in the detail panel.
	Doc string
	// TailOffset is how many runes of the inserted text end up after the caret.
	// The zero value leaves the caret at the end of the inserted text, which is
	// what plain candidates want; a snippet places the caret at its placeholder
	// by reporting how much of the template follows it.
	TailOffset int
	// Score is computed by the engine; sources do not need to care about it.
	Score int
}

// insertText returns the text actually inserted.
func (c Candidate) insertText() string {
	if c.Insert != "" {
		return c.Insert
	}
	return c.Label
}

// MatchScore computes the match score of prefix against candidate; -1 means no
// match.
//
// Design principles:
//   - A prefix match ranks far above a fuzzy match.
//   - For the same prefix, a shorter candidate wins (users beats user_settings).
//   - A candidate whose case matches exactly gets a small bonus (SQL keywords are
//     conventionally uppercase).
func MatchScore(prefix, candidate string) int {
	if candidate == "" {
		return -1
	}
	if prefix == "" {
		return 500
	}
	p := strings.ToLower(prefix)
	c := strings.ToLower(candidate)
	if strings.HasPrefix(c, p) {
		score := 2000 - len(c)
		if c == p {
			score += 500
		}
		if strings.HasPrefix(candidate, prefix) {
			score += 100 // case matches the input
		}
		return score
	}
	if score, ok := fuzzyScore(p, c); ok {
		return score
	}
	return -1
}

// fuzzyScore scores a subsequence match (allowing abbreviations such as "so"
// matching "status_omg").
func fuzzyScore(p, c string) (int, bool) {
	score := 0
	pi := 0
	streak := 0
	for ci := 0; ci < len(c) && pi < len(p); ci++ {
		if c[ci] == p[pi] {
			score += 10
			if streak > 0 {
				score += 8 // consecutive hit
			}
			if ci == 0 || isWordBoundary(c[ci-1]) {
				score += 12 // word-boundary hit
			}
			streak++
			pi++
			continue
		}
		streak = 0
		score-- // small penalty for a missed character
	}
	if pi < len(p) {
		return 0, false
	}
	return 300 + score, true
}

func isWordBoundary(b byte) bool {
	switch b {
	case '.', '_', '-', ' ':
		return true
	}
	return false
}
