package complete

import (
	"sort"

	"github.com/p-v-q/tdb/catalog"
	"github.com/p-v-q/tdb/dialect"
	"github.com/p-v-q/tdb/sql"
)

// DefaultLimit is the default maximum length of the candidate list.
const DefaultLimit = 50

// Request is a single completion request.
type Request struct {
	// Context is the syntactic context at the cursor (obtained from sql.AnalyzeContext).
	Context sql.Context
	// Catalog is the metadata snapshot; when nil, catalog-aware candidates are
	// simply absent.
	Catalog *catalog.Catalog
	// Dialect supplies keywords and functions.
	Dialect dialect.Dialect
	// Limit caps the number returned; 0 means use DefaultLimit.
	Limit int
}

// Source is a provider of completion candidates. A source is only responsible for
// what to offer, not for sorting or truncation.
type Source interface {
	Complete(req Request) []Candidate
}

// Engine aggregates multiple Sources and produces the final candidate list.
type Engine struct {
	sources []Source
}

// NewEngine constructs an engine.
func NewEngine(sources ...Source) *Engine {
	return &Engine{sources: sources}
}

// NewDefaultEngine constructs an engine with all default sources built in.
func NewDefaultEngine(d dialect.Dialect) *Engine {
	return NewEngine(
		keywordSource{d: d},
		functionSource{d: d},
		resourceSource{},
		snippetSource{},
		tableSource{},
		columnSource{},
	)
}

// Complete runs one completion and returns the score-sorted, truncated candidate list.
func (e *Engine) Complete(req Request) []Candidate {
	limit := req.Limit
	if limit <= 0 {
		limit = DefaultLimit
	}

	var out []Candidate
	index := map[string]int{} // key -> index into out, for cross-source deduplication

	for _, src := range e.sources {
		for _, c := range src.Complete(req) {
			score := MatchScore(req.Context.Prefix, c.Label)
			if score < 0 {
				continue
			}
			c.Score = score

			key := candidateKey(c)
			if i, ok := index[key]; ok {
				// When the same candidate appears more than once, keep the
				// higher-scoring copy.
				if c.Score > out[i].Score {
					out[i] = c
				}
				continue
			}
			index[key] = len(out)
			out = append(out, c)
		}
	}

	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		if out[i].Kind.priority() != out[j].Kind.priority() {
			return out[i].Kind.priority() > out[j].Kind.priority()
		}
		return out[i].Label < out[j].Label
	})

	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

// candidateKey is used for deduplication: kind + actual inserted text.
func candidateKey(c Candidate) string {
	return c.Kind.String() + "\x00" + c.insertText()
}
